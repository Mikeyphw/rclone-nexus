package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
)

func supervisorPaths(t *testing.T, fail bool) (paths.Paths, string) {
	t.Helper()
	root := t.TempDir()
	providerDir := filepath.Join(root, "provider")
	if err := os.MkdirAll(filepath.Join(providerDir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	mountInfo := filepath.Join(root, "mountinfo")
	if err := os.WriteFile(mountInfo, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = version ]; then echo 'rclone vtest'; exit 0; fi
if [ "$1" = mount ]; then
  if [ "${RNEXUS_FAKE_FAIL:-0}" = 1 ]; then exit 7; fi
  mp="$3"
  printf '36 25 0:32 / %s rw - fuse.rclone rclone rw\n' "$mp" >"$RNEXUS_MOUNTINFO_PATH"
  trap 'exit 0' TERM INT
  while :; do sleep 1; done
fi
exit 0
`
	rclone := filepath.Join(providerDir, "rclone")
	if err := os.WriteFile(rclone, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	fuse := filepath.Join(providerDir, "fusermount3")
	if err := os.WriteFile(fuse, []byte("#!/bin/sh\n: >\"$RNEXUS_MOUNTINFO_PATH\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "conf", "rclone.conf"), []byte("[fake]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	mountpoint := filepath.Join(root, "storage", "Drive")
	if err := os.MkdirAll(filepath.Dir(mountpoint), 0o755); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{StateDir: state, ModuleDir: filepath.Join(root, "module"), ProviderModuleDir: providerDir, RcloneConfig: filepath.Join(providerDir, "conf", "rclone.conf"), FuseDevice: "/dev/null"}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	conf := fmt.Sprintf("enabled=true\nremote=fake:\nmountpoint=%s\nvfs_cache_mode=full\nallow_other=true\nrequire_network=true\n", mountpoint)
	if err := os.WriteFile(filepath.Join(p.MountsDir, "drive.conf"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", rclone)
	t.Setenv("RNEXUS_FUSERMOUNT_BIN", fuse)
	t.Setenv("RNEXUS_MOUNTINFO_PATH", mountInfo)
	t.Setenv("RNEXUS_BOOT_COMPLETED", "1")
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	t.Setenv("RNEXUS_START_GRACE_SECONDS", "0.05")
	t.Setenv("RNEXUS_STOP_TIMEOUT_SECONDS", "1")
	t.Setenv("RNEXUS_RETRY_BASE_MS", "10")
	t.Setenv("RNEXUS_RETRY_MAX_MS", "20")
	if fail {
		t.Setenv("RNEXUS_FAKE_FAIL", "1")
	}
	return p, mountInfo
}

func stopIfRunning(t *testing.T, p paths.Paths) {
	t.Helper()
	obs, err := mounts.ObserveRuntime(p, "drive")
	if err == nil && obs.ProcessAlive {
		_, _ = mounts.ReconcileStop(context.Background(), p, "drive")
	}
}

func TestSupervisorRepairsStaleOwnedMount(t *testing.T) {
	p, _ := supervisorPaths(t, false)
	defer stopIfRunning(t, p)
	report, err := ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Health) != 1 || report.Health[0].State != Running {
		t.Fatalf("expected running: %+v", report)
	}
	obs, err := mounts.ObserveRuntime(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if !obs.ProcessAlive || !obs.MountAlive {
		t.Fatalf("expected live process+mount: %+v", obs)
	}
	if err := syscall.Kill(obs.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	report, err = ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Health) != 1 || report.Health[0].State != Running {
		t.Fatalf("expected repaired running mount: %+v", report)
	}
	if len(report.Changed) < 2 {
		t.Fatalf("expected stale cleanup + restart actions: %+v", report.Changed)
	}
}

func TestOfflineDoesNotDestroyValidMount(t *testing.T) {
	p, _ := supervisorPaths(t, false)
	defer stopIfRunning(t, p)
	if _, err := ReconcileOnce(context.Background(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := mounts.ObserveRuntime(p, "drive")
	t.Setenv("RNEXUS_NETWORK_STATE", "offline")
	report, err := ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := mounts.ObserveRuntime(p, "drive")
	if report.Health[0].State != RemoteOffline {
		t.Fatalf("expected REMOTE_OFFLINE: %+v", report.Health[0])
	}
	if !after.ProcessAlive || !after.MountAlive || after.PID != before.PID {
		t.Fatalf("valid VFS mount was destroyed: before=%+v after=%+v", before, after)
	}
}

func TestRestartBudgetExhaustionIsPersistent(t *testing.T) {
	p, _ := supervisorPaths(t, true)
	t.Setenv("RNEXUS_RESTART_BUDGET", "2")
	report, err := ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Health[0].Attempts != 1 {
		t.Fatalf("expected first failure: %+v", report.Health[0])
	}
	time.Sleep(25 * time.Millisecond)
	report, err = ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Health[0].State != Degraded || report.Health[0].Reason != "restart_budget_exhausted" || report.Health[0].Attempts != 2 {
		t.Fatalf("budget not exhausted: %+v", report.Health[0])
	}
	persisted := readHealth(p, "drive")
	if persisted.Attempts != 2 {
		t.Fatalf("retry truth not persistent: %+v", persisted)
	}
}

func TestBootWaitCancelsCleanly(t *testing.T) {
	p, _ := supervisorPaths(t, false)
	t.Setenv("RNEXUS_BOOT_COMPLETED", "0")
	t.Setenv("RNEXUS_BOOT_WAIT_SECONDS", "10")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := Reconcile(ctx, p, true, nil)
	if err == nil {
		t.Fatal("expected cancelled boot wait")
	}
}
