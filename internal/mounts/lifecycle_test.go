package mounts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
    if [ "${2:-}" = "--help" ]; then
      cat <<'HELP'
Flags:
      --config string
      --vfs-cache-mode string
      --cache-dir string
      --log-file string
      --log-level string
      --vfs-cache-max-size string
      --vfs-cache-max-age duration
      --dir-cache-time duration
      --poll-interval duration
      --allow-other
      --read-only
      --rc
      --rc-addr string
      --rc-user string
      --rc-pass string
HELP
      exit 0
    fi
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
    if [ "${2:-}" = "--help" ]; then
      cat <<'HELP'
Flags:
      --config string
      --vfs-cache-mode string
      --cache-dir string
      --log-file string
      --log-level string
      --vfs-cache-max-size string
      --vfs-cache-max-age duration
      --dir-cache-time duration
      --poll-interval duration
      --allow-other
      --read-only
      --rc
      --rc-addr string
      --rc-user string
      --rc-pass string
HELP
      exit 0
    fi
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

func TestCoreG1StopNeverUnmountsUnownedOrReusedPIDMount(t *testing.T) {
	p := lifecycleTestPaths(t)
	mountpoint := filepath.Join(t.TempDir(), "foreign")
	writeLegacyMount(t, p, "drive", "fake:", mountpoint, true)
	mountInfo := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(mountInfo, []byte("36 25 0:32 / "+mountpoint+" rw - fuse.rclone rclone rw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "unmounted")
	fusermount := filepath.Join(t.TempDir(), "fusermount3")
	if err := os.WriteFile(fusermount, []byte("#!/bin/sh\nprintf called >\"$RNEXUS_UNMOUNT_SENTINEL\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_MOUNTINFO_PATH", mountInfo)
	t.Setenv("RNEXUS_FUSERMOUNT_BIN", fusermount)
	t.Setenv("RNEXUS_UNMOUNT_SENTINEL", sentinel)

	// A configured path with no Nexus process identity is never enough authority
	// to unmount whatever another actor has mounted there.
	if _, err := Stop(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("foreign mount was unmounted without ownership: %v", err)
	}

	// A live PID with a different start time models PID reuse. The stale record
	// must not turn a foreign rclone/FUSE mount into Nexus-owned state.
	cfg, err := Parse(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	record, err := newProcessRecord(os.Getpid(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	record.StartTicks++
	if err := writeProcessRecord(p, "drive", record); err != nil {
		t.Fatal(err)
	}
	obs, err := ObserveRuntime(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if obs.OwnedMount {
		t.Fatalf("PID-reused mount incorrectly claimed as owned: %+v", obs)
	}
	if _, err := Stop(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("PID reuse authorized foreign unmount: %v", err)
	}
}

func TestCoreG1DeadMatchingProcessRecordCanCleanOwnedStaleMount(t *testing.T) {
	p := lifecycleTestPaths(t)
	mountpoint := filepath.Join(t.TempDir(), "owned")
	writeLegacyMount(t, p, "drive", "fake:", mountpoint, true)
	cfg, err := Parse(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "sleep 0.05")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	record, err := newProcessRecord(cmd.Process.Pid, cfg)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	if err := writeProcessRecord(p, "drive", record); err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	_ = cmd.Wait()

	mountInfo := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(mountInfo, []byte("36 25 0:32 / "+mountpoint+" rw - fuse.rclone rclone rw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "unmounted")
	fusermount := filepath.Join(t.TempDir(), "fusermount3")
	if err := os.WriteFile(fusermount, []byte("#!/bin/sh\nprintf called >\"$RNEXUS_UNMOUNT_SENTINEL\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_MOUNTINFO_PATH", mountInfo)
	t.Setenv("RNEXUS_FUSERMOUNT_BIN", fusermount)
	t.Setenv("RNEXUS_UNMOUNT_SENTINEL", sentinel)

	obs, err := ObserveRuntime(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if !obs.OwnedMount || obs.ProcessAlive {
		t.Fatalf("dead matching record should prove stale mount ownership: %+v", obs)
	}
	if _, err := Stop(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("owned stale mount was not cleaned: %v", err)
	}
}

func TestCoreG1ReconcileHelpersHonorCurrentDesiredState(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	defer cleanupMount(t, p, "drive")

	if err := setDesiredState(p, "drive", DesiredStopped); err != nil {
		t.Fatal(err)
	}
	result, err := ReconcileStart(context.Background(), p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Noop || StatusOne(p, "drive").State == "running" {
		t.Fatalf("stale reconcile start crossed explicit stop: %+v status=%+v", result, StatusOne(p, "drive"))
	}

	if _, err := Start(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if err := setDesiredState(p, "drive", DesiredRunning); err != nil {
		t.Fatal(err)
	}
	before := StatusOne(p, "drive")
	result, err = ReconcileStop(context.Background(), p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	after := StatusOne(p, "drive")
	if !result.Noop || after.State != "running" || after.PID != before.PID {
		t.Fatalf("stale reconcile stop crossed explicit start: result=%+v before=%+v after=%+v", result, before, after)
	}
}

func TestCoreG1RepeatedLifecycleAndReconcileCycles(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	defer cleanupMount(t, p, "drive")
	for cycle := 0; cycle < 8; cycle++ {
		started, err := Start(context.Background(), p, "drive")
		if err != nil {
			t.Fatalf("cycle %d start: %v", cycle, err)
		}
		if started.PID <= 0 || StatusOne(p, "drive").State != "running" {
			t.Fatalf("cycle %d failed running state: %+v", cycle, StatusOne(p, "drive"))
		}
		report, err := Reconcile(context.Background(), p, nil)
		if err != nil || len(report.Failures) != 0 || len(report.Changed) != 0 {
			t.Fatalf("cycle %d running reconcile changed stable state: report=%+v err=%v", cycle, report, err)
		}
		if _, err := Stop(context.Background(), p, "drive"); err != nil {
			t.Fatalf("cycle %d stop: %v", cycle, err)
		}
		report, err = Reconcile(context.Background(), p, nil)
		if err != nil || len(report.Failures) != 0 || len(report.Changed) != 0 {
			t.Fatalf("cycle %d stopped reconcile changed stable state: report=%+v err=%v", cycle, report, err)
		}
		if status := StatusOne(p, "drive"); status.State != "stopped" || status.Desired != DesiredStopped {
			t.Fatalf("cycle %d final state=%+v", cycle, status)
		}
	}
}

func TestMountLifecycleCreatesLoopbackRCAndRemovesCredentialsOnStop(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	result, err := Start(context.Background(), p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if result.PID <= 0 {
		t.Fatal(result)
	}
	data, err := os.ReadFile(filepath.Join(p.RCDir, "drive.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct{ Address, Username, Password string }
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Username == "" || record.Password == "" || !strings.HasPrefix(record.Address, "127.0.0.1:") {
		t.Fatalf("invalid rc record: %+v", record)
	}
	if _, err := Stop(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.RCDir, "drive.json")); !os.IsNotExist(err) {
		t.Fatalf("rc credential record survived stop: %v", err)
	}
}

func TestArgsFileCannotOverrideNexusRCBoundary(t *testing.T) {
	p := lifecycleTestPaths(t)
	mountpoint := filepath.Join(t.TempDir(), "drive")
	argsFile := filepath.Join(p.StateDir, "private.args")
	if err := os.WriteFile(argsFile, []byte("--rc-addr\n0.0.0.0:5572\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	text := "enabled=true\nremote=fake:\nmountpoint=" + mountpoint + "\nvfs_cache_mode=full\nallow_other=false\nargs_file=" + argsFile + "\n"
	if err := os.WriteFile(filepath.Join(p.MountsDir, "drive.conf"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Start(context.Background(), p, "drive")
	if err == nil || !strings.Contains(err.Error(), "cannot override Nexus RC options") {
		t.Fatalf("expected RC override rejection, got %v", err)
	}
	lifecycleErr, ok := err.(*LifecycleError)
	if !ok || lifecycleErr.Code != "mount_args_invalid" || lifecycleErr.Retryable || lifecycleErr.Stage != "argv_prepare" {
		t.Fatalf("args-file configuration failure is not terminal/structured: %#v", err)
	}
}

func TestProviderConfigMissingIsTerminalStructuredFailure(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	if err := os.Remove(p.RcloneConfig); err != nil {
		t.Fatal(err)
	}
	_, err := Start(context.Background(), p, "drive")
	if err == nil {
		t.Fatal("expected provider config failure")
	}
	lifecycleErr, ok := err.(*LifecycleError)
	if !ok || lifecycleErr.Code != "runtime_config_unavailable" || lifecycleErr.Retryable || lifecycleErr.Category != "runtime" || lifecycleErr.Stage != "runtime_resolution" {
		t.Fatalf("provider config failure is not terminal/structured: %#v", err)
	}
}

func TestStartupLifecycleDetailRedactsCredentialCanaries(t *testing.T) {
	p := lifecycleTestPaths(t)
	fake := os.Getenv("RNEXUS_RCLONE_BIN")
	script := `#!/bin/sh
case "${1:-}" in
  version) echo 'rclone vTEST'; exit 0 ;;
  mount)
    if [ "${2:-}" = "--help" ]; then
      cat <<'HELP'
Flags:
  --config string
  --vfs-cache-mode string
  --cache-dir string
  --log-file string
  --log-level string
  --vfs-cache-max-size string
  --vfs-cache-max-age duration
  --dir-cache-time duration
  --poll-interval duration
  --allow-other
  --read-only
  --rc
  --rc-addr string
  --rc-user string
  --rc-pass string
HELP
      exit 0
    fi
    echo '2026/10/02 20:19:41 ERROR : Fatal error: Authorization: Bearer NEXUS_SUPER_SECRET' >&2
    exit 7
    ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_START_GRACE_SECONDS", "1")
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	_, err := Start(context.Background(), p, "drive")
	if err == nil {
		t.Fatal("expected startup failure")
	}
	lifecycleErr, ok := err.(*LifecycleError)
	if !ok {
		t.Fatalf("expected LifecycleError, got %T: %v", err, err)
	}
	if strings.Contains(lifecycleErr.Detail, "NEXUS_SUPER_SECRET") || !strings.Contains(lifecycleErr.Detail, "<redacted>") {
		t.Fatalf("startup detail leaked credential canary: %q", lifecycleErr.Detail)
	}
	if lifecycleErr.ExitCode != 7 {
		t.Fatalf("exit code=%d want 7", lifecycleErr.ExitCode)
	}
}

func TestStartupPreflightRejectsUnsupportedGeneratedFlagAsNonRetryableCompatibilityError(t *testing.T) {
	p := lifecycleTestPaths(t)
	bad := filepath.Join(t.TempDir(), "rclone-cli-contract")
	script := `#!/bin/sh
case "${1:-}" in
  version) echo 'rclone v1.75.1'; exit 0 ;;
  mount)
    if [ "${2:-}" = "--help" ]; then
      cat <<'HELP'
Flags:
      --config string
      --vfs-cache-mode string
      --cache-dir string
      --log-file string
      --log-level string
      --vfs-cache-max-size string
      --vfs-cache-max-age duration
      --dir-cache-time duration
      --poll-interval duration
      --allow-other
      --read-only
      --rc
      --rc-addr string
      --rc-user string
      --rc-pass string
HELP
      exit 0
    fi
    trap 'exit 0' TERM INT
    while :; do sleep 1; done
    ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(bad, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", bad)
	argsFile := filepath.Join(p.StateDir, "unsupported.args")
	if err := os.WriteFile(argsFile, []byte("--bad-unsupported\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mountpoint := filepath.Join(t.TempDir(), "drive")
	text := "enabled=true\nremote=fake:\nmountpoint=" + mountpoint + "\nvfs_cache_mode=full\nallow_other=false\nargs_file=" + argsFile + "\n"
	if err := os.WriteFile(filepath.Join(p.MountsDir, "drive.conf"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Start(context.Background(), p, "drive")
	if err == nil {
		t.Fatal("expected startup preflight failure")
	}
	lifecycleErr, ok := err.(*LifecycleError)
	if !ok {
		t.Fatalf("expected LifecycleError, got %T: %v", err, err)
	}
	if lifecycleErr.Code != "rclone_cli_incompatible" || lifecycleErr.Retryable || lifecycleErr.Stage != "argv_preflight" {
		t.Fatalf("unexpected lifecycle error: %+v", lifecycleErr)
	}
	if !strings.Contains(lifecycleErr.Detail, "--bad-unsupported") {
		t.Fatalf("detail=%q", lifecycleErr.Detail)
	}
}
