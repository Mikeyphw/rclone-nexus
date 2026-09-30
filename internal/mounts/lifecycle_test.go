package mounts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"rclone-nexus/internal/paths"
)

func lifecycleTestPaths(t *testing.T) paths.Paths {
	t.Helper()
	base := t.TempDir()
	providerDir := filepath.Join(base, "provider")
	if err := os.MkdirAll(filepath.Join(providerDir, "system", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(providerDir, "conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "conf", "rclone.conf"), []byte("[fake]\ntype=local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(providerDir, "system", "bin", "rclone")
	script := `#!/bin/sh
case "${1:-}" in
  version) echo 'rclone vTEST'; exit 0 ;;
  mount)
    [ "${2:-}" = "fail:" ] && exit 23
    trap 'exit 0' TERM INT
    while :; do sleep 1; done
    ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{
		ModuleDir: filepath.Join(base, "module"), ProviderModuleDir: providerDir,
		StateDir: filepath.Join(base, "state"), RcloneConfig: filepath.Join(providerDir, "conf", "rclone.conf"),
		FuseDevice: "/dev/null",
	}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", fake)
	t.Setenv("RNEXUS_START_GRACE_SECONDS", "0")
	t.Setenv("RNEXUS_STOP_TIMEOUT_SECONDS", "1")
	return p
}

func writeLegacyMount(t *testing.T, p paths.Paths, name, remote, mountpoint string, enabled bool) {
	t.Helper()
	text := "enabled=false\n"
	if enabled {
		text = "enabled=true\n"
	}
	text += "remote=" + remote + "\nmountpoint=" + mountpoint + "\nvfs_cache_mode=full\nallow_other=false\n"
	if err := os.WriteFile(filepath.Join(p.MountsDir, name+".conf"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func cleanupMount(t *testing.T, p paths.Paths, name string) {
	t.Helper()
	_ = withMountLock(p, name, func() error {
		cfg, err := Parse(p, name)
		if err != nil {
			return nil
		}
		_, _ = stopUnlocked(context.Background(), p, cfg)
		return nil
	})
}

func TestStatusIsReadOnlyForStaleProcessIdentity(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	cfg, err := Parse(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	record, err := newProcessRecord(os.Getpid(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	record.StartTicks++ // Simulate PID reuse without ever signalling this process.
	if err := writeProcessRecord(p, "drive", record); err != nil {
		t.Fatal(err)
	}

	status := StatusOne(p, "drive")
	if status.State != "stale" {
		t.Fatalf("state=%s", status.State)
	}
	if _, err := os.Stat(pidPath(p, "drive")); err != nil {
		t.Fatalf("status mutated identity record: %v", err)
	}

	if _, err := Stop(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pidPath(p, "drive")); !os.IsNotExist(err) {
		t.Fatalf("stale record not removed: %v", err)
	}
}

func TestConcurrentStartsConvergeToOneManagedProcess(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	defer cleanupMount(t, p, "drive")
	const workers = 8
	results := make(chan ActionResult, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := Start(context.Background(), p, "drive")
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent start: %v", err)
		}
	}
	pid := 0
	for result := range results {
		if pid == 0 {
			pid = result.PID
		}
		if result.PID != pid {
			t.Fatalf("multiple managed pids: first=%d got=%d", pid, result.PID)
		}
	}
	status := StatusOne(p, "drive")
	if status.State != "running" || !status.Managed || status.PID != pid {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestReconcileIsolatesOneMountFailure(t *testing.T) {
	p := lifecycleTestPaths(t)
	badMountpoint := filepath.Join(t.TempDir(), "bad")
	bad := "enabled=true\nremote=fake:\nmountpoint=" + badMountpoint + "\nvfs_cache_mode=full\nallow_other=false\nargs_file=missing.args\n"
	if err := os.WriteFile(filepath.Join(p.MountsDir, "bad.conf"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLegacyMount(t, p, "good", "fake:", filepath.Join(t.TempDir(), "good"), true)
	defer cleanupMount(t, p, "good")

	report, err := Reconcile(context.Background(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failures) != 1 || report.Failures[0].Name != "bad" {
		t.Fatalf("failures=%+v", report.Failures)
	}
	if len(report.Changed) != 1 || report.Changed[0].Name != "good" {
		t.Fatalf("changed=%+v", report.Changed)
	}
	if status := StatusOne(p, "good"); status.State != "running" {
		t.Fatalf("good mount did not start: %+v", status)
	}
}

func TestDesiredStateIsPersistentAndDistinctFromObservedState(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	if _, err := Start(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if _, err := Stop(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	status := StatusOne(p, "drive")
	if status.Desired != DesiredStopped || status.State != "stopped" {
		t.Fatalf("unexpected status after stop: %+v", status)
	}
	data, err := os.ReadFile(desiredPath(p, "drive"))
	if err != nil {
		t.Fatal(err)
	}
	var record desiredRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.State != DesiredStopped {
		t.Fatalf("desired=%s", record.State)
	}
	report, err := Reconcile(context.Background(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Changed) != 0 {
		t.Fatalf("explicit stop was not preserved: %+v", report)
	}
}

func TestConcurrentStartStopDoesNotCorruptOwnership(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	defer cleanupMount(t, p, "drive")

	for round := 0; round < 6; round++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = Start(context.Background(), p, "drive")
		}()
		go func() {
			defer wg.Done()
			_, _ = Stop(context.Background(), p, "drive")
		}()
		wg.Wait()
		if record, err := readProcessRecord(p, "drive"); err == nil {
			if err := validateProcessRecord(record); err != nil {
				t.Fatalf("round %d left invalid ownership record: %v", round, err)
			}
		}
	}
	if _, err := Stop(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if status := StatusOne(p, "drive"); status.State != "stopped" {
		t.Fatalf("final status=%+v", status)
	}
}

func TestStopUsesBoundedForcedCleanupForStubbornProcess(t *testing.T) {
	p := lifecycleTestPaths(t)
	stubborn := filepath.Join(t.TempDir(), "rclone-stubborn")
	script := `#!/bin/sh
case "${1:-}" in
  version) echo 'rclone vTEST'; exit 0 ;;
  mount)
    trap '' TERM
    while :; do sleep 1; done
    ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(stubborn, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", stubborn)
	t.Setenv("RNEXUS_STOP_TIMEOUT_SECONDS", "0")
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	started, err := Start(context.Background(), p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Stop(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if alive(started.PID) {
		// cmd.Wait reaping is asynchronous; give it a short bounded window.
		for i := 0; i < 50 && alive(started.PID); i++ {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if alive(started.PID) {
		t.Fatalf("stubborn managed process %d survived bounded forced cleanup", started.PID)
	}
}
