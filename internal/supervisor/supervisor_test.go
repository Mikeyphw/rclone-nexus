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
if [ "$1" = mount ] && [ "$2" = --help ]; then
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
if [ "$1" = mount ]; then
  mp="$3"
  printf '36 25 0:32 / %s rw - fuse.rclone rclone rw\n' "$mp" >"$RNEXUS_MOUNTINFO_PATH"
  trap 'exit 0' TERM INT
  while :; do sleep 1; done
fi
exit 0
`
	if fail {
		// A transient network startup failure is retryable and therefore owns
		// restart-budget/backoff behavior. Terminal CLI compatibility failures
		// are covered separately and must not consume this budget.
		script = `#!/bin/sh
if [ "$1" = version ]; then echo 'rclone vtest'; exit 0; fi
if [ "$1" = mount ] && [ "$2" = --help ]; then
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
if [ "$1" = mount ]; then
  echo '2026/10/02 23:19:41 ERROR : Fatal error: network is unreachable' >&2
  exit 2
fi
exit 0
`
	}
	rclone := filepath.Join(root, "module", "system", "bin", "rclone")
	if err := os.MkdirAll(filepath.Dir(rclone), 0o755); err != nil {
		t.Fatal(err)
	}
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
	p := paths.Paths{StateDir: state, ModuleDir: filepath.Join(root, "module"), ProviderModuleDir: providerDir, FuseDevice: "/dev/null"}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneConfig, []byte("[fake]\ntype = local\n"), 0o600); err != nil {
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
	return p, mountInfo
}

func stopIfRunning(t *testing.T, p paths.Paths) {
	t.Helper()
	obs, err := mounts.ObserveRuntime(p, "drive")
	if err == nil && obs.ProcessAlive {
		// Test cleanup is an explicit stop. ReconcileStop intentionally honors
		// desired=running and therefore must not be used as a force-cleanup API.
		_, _ = mounts.Stop(context.Background(), p, "drive")
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
	// Advance the persisted retry state explicitly instead of sleeping. The
	// test owns restart-budget persistence, not wall-clock/backoff timing.
	persisted := readHealth(p, "drive")
	if persisted.NextRetryUnixMS == 0 {
		t.Fatalf("first failure did not persist a retry deadline: %+v", persisted)
	}
	persisted.NextRetryUnixMS = 0
	if err := writeHealth(p, persisted); err != nil {
		t.Fatal(err)
	}
	report, err = ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Health[0].State != Degraded || report.Health[0].Reason != "restart_budget_exhausted" || report.Health[0].Attempts != 2 {
		t.Fatalf("budget not exhausted: %+v", report.Health[0])
	}
	persisted = readHealth(p, "drive")
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

func TestTerminalCLICompatibilityFailureDoesNotConsumeRestartBudget(t *testing.T) {
	p, _ := supervisorPaths(t, false)
	bad := filepath.Join(t.TempDir(), "rclone-bad-cli")
	script := `#!/bin/sh
if [ "$1" = version ]; then echo 'rclone v1.75.1'; exit 0; fi
if [ "$1" = mount ] && [ "$2" = --help ]; then
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
HELP
  exit 0
fi
if [ "$1" = mount ]; then
  exit 99
fi
exit 0
`
	if err := os.WriteFile(bad, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	badBytes, err := os.ReadFile(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ModuleDir, "system", "bin", "rclone"), badBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_START_GRACE_SECONDS", "1")
	report, err := ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Health) != 1 {
		t.Fatalf("health=%+v", report.Health)
	}
	h := report.Health[0]
	if h.State != Degraded || h.Reason != "rclone_cli_incompatible" || h.Retryable || h.Attempts != 0 || h.NextRetryUnixMS != 0 {
		t.Fatalf("terminal failure incorrectly entered retry budget: %+v", h)
	}
	// A second supervisor pass must preserve the terminal diagnosis and must not
	// launch another doomed process automatically.
	report, err = ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	h = report.Health[0]
	if h.Attempts != 0 || h.Reason != "rclone_cli_incompatible" {
		t.Fatalf("terminal failure retried unexpectedly: %+v", h)
	}
}

func TestMissingProviderConfigIsTerminalWithoutRestartBudgetAndRecovers(t *testing.T) {
	p, _ := supervisorPaths(t, false)
	defer stopIfRunning(t, p)
	if err := os.Remove(p.RcloneConfig); err != nil {
		t.Fatal(err)
	}
	report, err := ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Health) != 1 {
		t.Fatalf("health=%+v", report.Health)
	}
	h := report.Health[0]
	if h.State != Degraded || h.Reason != "runtime_config_unavailable" || h.FailureCode != "runtime_config_unavailable" || h.Retryable || h.Attempts != 0 || h.NextRetryUnixMS != 0 {
		t.Fatalf("provider config failure entered retry path: %+v", h)
	}
	if err := os.WriteFile(p.RcloneConfig, []byte("[fake]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	h = report.Health[0]
	if h.State != Running || h.FailureCode != "" || h.Attempts != 0 {
		t.Fatalf("provider repair did not clear readiness-owned terminal state: %+v", h)
	}
}

func TestTransientStartupNetworkFailureConsumesRetryBudget(t *testing.T) {
	p, _ := supervisorPaths(t, true)
	t.Setenv("RNEXUS_RESTART_BUDGET", "3")
	report, err := ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Health) != 1 {
		t.Fatalf("health=%+v", report.Health)
	}
	h := report.Health[0]
	if !h.Retryable || h.Attempts != 1 || h.NextRetryUnixMS == 0 || h.FailureCode != "remote_offline" || h.State != RemoteOffline {
		t.Fatalf("transient network startup failure did not enter retry path: %+v", h)
	}
}
