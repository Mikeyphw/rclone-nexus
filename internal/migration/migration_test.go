package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"rclone-nexus/internal/jobs"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	provider := filepath.Join(root, "provider")
	state := filepath.Join(root, "state")
	bin := filepath.Join(root, "module", "system", "bin", "rclone")
	if err := os.MkdirAll(filepath.Join(provider, "conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = version ]; then echo 'rclone v-test'; exit 0; fi
if [ "$1" = listremotes ]; then
  cfg=""
  while [ $# -gt 0 ]; do if [ "$1" = --config ]; then shift; cfg="$1"; fi; shift; done
  if grep -q ENCRYPTED "$cfg" 2>/dev/null; then echo 'password required' >&2; exit 9; fi
  echo 'demo:'; exit 0
fi
if [ "$1" = mount ] && [ "${2:-}" = --help ]; then
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
  trap 'exit 0' TERM INT
  while :; do sleep 1; done
fi
exit 0
`
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provider, "module.prop"), []byte("id=rclone\nversion=1.75.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provider, "conf", "rclone.conf"), []byte("[demo]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{ModuleDir: filepath.Join(root, "module"), StateDir: state, ProviderModuleDir: provider}.Normalize()
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestDetectProviderConfigJobsAndVersion(t *testing.T) {
	p := testPaths(t)
	syncPath := filepath.Join(p.ProviderModuleDir, "conf", "sync")
	if err := os.WriteFile(syncPath, []byte("demo:src '/tmp/with space'\ndemo:src /tmp/dst --transfers 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(p)
	if err != nil {
		t.Fatal(err)
	}
	if !d.ProviderPresent || !d.ProviderEnabled || d.ProviderConfigSHA256 == "" {
		t.Fatalf("bad detection: %+v", d)
	}
	if d.ProviderModuleVersion != "1.75.0" {
		t.Fatalf("version=%q", d.ProviderModuleVersion)
	}
	if len(d.Jobs) != 2 || !d.Jobs[0].Importable || d.Jobs[0].Destination != "/tmp/with space" {
		t.Fatalf("jobs=%+v", d.Jobs)
	}
	if d.Jobs[1].Importable || !strings.Contains(d.Jobs[1].Reason, "not representable") {
		t.Fatalf("options must fail closed: %+v", d.Jobs[1])
	}
}

func TestApplyCopiesConfigExactlyDisabledAndFinalizeRequiresProviderDisable(t *testing.T) {
	p := testPaths(t)
	if err := os.WriteFile(filepath.Join(p.ProviderModuleDir, "conf", "sync"), []byte("demo:src /tmp/dst\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := PreviewRequest{SelectedJobs: []string{"sync:1"}, JobEvery: "24h"}
	v, err := PreviewMigration(p, req)
	if err != nil {
		t.Fatal(err)
	}
	if !v.CanApply {
		t.Fatalf("conflicts=%v", v.Conflicts)
	}
	s, err := Apply(context.Background(), p, v)
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != PhaseAwaitingProviderDisable {
		t.Fatalf("phase=%s", s.Phase)
	}
	if got := fileSHA(t, p.ManagedRcloneConfig); got != v.Detection.ProviderConfigSHA256 {
		t.Fatalf("config hash %s want %s", got, v.Detection.ProviderConfigSHA256)
	}
	jr, err := jobs.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(jr.Jobs) != 1 || jr.Jobs[0].Enabled {
		t.Fatalf("jobs must import disabled: %+v", jr.Jobs)
	}
	fp, err := PreviewFinalize(p)
	if err != nil {
		t.Fatal(err)
	}
	if fp.CanFinalize || len(fp.Conflicts) == 0 {
		t.Fatalf("finalize should require provider disable: %+v", fp)
	}
	if err := os.WriteFile(filepath.Join(p.ProviderModuleDir, "disable"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fp, err = PreviewFinalize(p)
	if err != nil {
		t.Fatal(err)
	}
	if !fp.CanFinalize {
		t.Fatalf("finalize conflicts=%v", fp.Conflicts)
	}
	s, err = Finalize(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != PhaseCompleted || s.EvidencePath == "" {
		t.Fatalf("state=%+v", s)
	}
	jr, _ = jobs.Load(p)
	if !jr.Jobs[0].Enabled {
		t.Fatal("reviewed job was not enabled")
	}
	if _, err := os.Stat(filepath.Join(p.ProviderModuleDir, "module.prop")); err != nil {
		t.Fatalf("provider mutated/removed: %v", err)
	}
}

func TestEncryptedProviderConfigFailsAndRestoresNexusConfig(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneConfig, []byte("[old]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := fileSHA(t, p.ManagedRcloneConfig)
	if err := os.WriteFile(filepath.Join(p.ProviderModuleDir, "conf", "rclone.conf"), []byte("ENCRYPTED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, _ := PreviewMigration(p, PreviewRequest{})
	s, err := Apply(context.Background(), p, v)
	if err == nil || s.Phase != PhaseRolledBack {
		t.Fatalf("expected rollback: state=%+v err=%v", s, err)
	}
	if got := fileSHA(t, p.ManagedRcloneConfig); got != old {
		t.Fatalf("config not restored: %s != %s", got, old)
	}
}

func TestProviderDisappearsMidMigrationRollsBack(t *testing.T) {
	p := testPaths(t)
	v, err := PreviewMigration(p, PreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	original := afterProviderQuiesce
	afterProviderQuiesce = func() { _ = os.RemoveAll(p.ProviderModuleDir) }
	defer func() { afterProviderQuiesce = original }()
	s, err := Apply(context.Background(), p, v)
	if err == nil || !strings.Contains(err.Error(), "disappeared") || s.Phase != PhaseRolledBack {
		t.Fatalf("state=%+v err=%v", s, err)
	}
}

func TestProviderRefusesStop(t *testing.T) {
	p := testPaths(t)
	script := filepath.Join(p.ProviderModuleDir, "stubborn.sh")
	ready := filepath.Join(p.ProviderModuleDir, "stubborn.ready")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap '' TERM\ntouch \"$1\"\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", script, ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(cmd.Process.Pid, syscall.SIGKILL); _, _ = cmd.Process.Wait() }()
	readyDeadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(readyDeadline) {
			t.Fatal("stubborn provider process did not reach TERM-trap readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Setenv("RNEXUS_MIGRATION_QUIESCE_TIMEOUT_MS", "150")
	deadline := time.Now().Add(2 * time.Second)
	for len(scanProcesses(p.ProviderModuleDir)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	v, err := PreviewMigration(p, PreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Apply(context.Background(), p, v)
	if err == nil || !strings.Contains(err.Error(), "refused stop") || s.Phase != PhaseRolledBack {
		t.Fatalf("state=%+v err=%v", s, err)
	}
}

func TestDuplicateDestinationAndExistingJobConflictFailPreview(t *testing.T) {
	p := testPaths(t)
	cur, err := mounts.LoadRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	cand := []mounts.CandidateConfig{{Name: "existing", Enabled: false, Remote: "demo:", Mountpoint: "/tmp/same", VFSCacheMode: "full", NetworkMode: "any", VFSProfile: "balanced"}}
	pv, err := mounts.PreviewCandidate(p, cand)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mounts.ApplyCandidate(context.Background(), p, cur.Revision, pv.CandidateDigest, cand, nil); err != nil {
		t.Fatal(err)
	}
	// Inject provider mount process through a fake proc tree.
	proc := filepath.Join(t.TempDir(), "123")
	if err := os.MkdirAll(proc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "cmdline"), []byte(p.ProviderModuleDir+"/bin/rclone\x00mount\x00demo:\x00/tmp/same\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_MIGRATION_PROC_ROOT", filepath.Dir(proc))
	if err := os.WriteFile(filepath.Join(p.ProviderModuleDir, "conf", "sync"), []byte("demo:src /tmp/dst\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j0, _ := jobs.Load(p)
	jc := []jobs.Config{{Name: "migrated-sync-001", Enabled: false, Type: jobs.TypeSync, Source: "demo:old", Destination: "/tmp/old", Every: "24h", NetworkMode: "any", ConfirmDestructive: true}}
	jpv, err := jobs.PreviewCandidate(p, jc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.ApplyCandidate(p, j0.Revision, jpv.CandidateDigest, jc); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(p)
	if err != nil || len(d.Mounts) != 1 {
		t.Fatalf("detect mounts=%+v err=%v", d.Mounts, err)
	}
	v, err := PreviewMigration(p, PreviewRequest{SelectedMounts: []string{d.Mounts[0].ID}, SelectedJobs: []string{"sync:1"}, JobEvery: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(v.Conflicts, " | ")
	if v.CanApply || !strings.Contains(joined, "duplicate destination path") || !strings.Contains(joined, "conflicts with existing Nexus job") {
		t.Fatalf("conflicts=%v", v.Conflicts)
	}
}

func TestInterruptedMigrationRecoversPreMigrationState(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("[old]\ntype = local\n")
	if err := os.WriteFile(p.ManagedRcloneConfig, old, 0o600); err != nil {
		t.Fatal(err)
	}
	v, _ := PreviewMigration(p, PreviewRequest{})
	s, err := Apply(context.Background(), p, v)
	if err != nil {
		t.Fatal(err)
	}
	s.Phase = PhaseApplying
	if err := saveState(p, s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneConfig, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, present, err := Recover(context.Background(), p)
	if err != nil || !present {
		t.Fatalf("recover=%+v present=%v err=%v", got, present, err)
	}
	if got.Phase != PhaseRolledBack {
		t.Fatalf("phase=%s", got.Phase)
	}
	b, _ := os.ReadFile(p.ManagedRcloneConfig)
	if string(b) != string(old) {
		t.Fatalf("config=%q", b)
	}
}

func TestProviderReactivationAfterCompletedMigrationCreatesConflict(t *testing.T) {
	p := testPaths(t)
	v, _ := PreviewMigration(p, PreviewRequest{})
	if _, err := Apply(context.Background(), p, v); err != nil {
		t.Fatal(err)
	}
	disable := filepath.Join(p.ProviderModuleDir, "disable")
	if err := os.WriteFile(disable, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Finalize(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(disable); err != nil {
		t.Fatal(err)
	}
	s, present, err := Recover(context.Background(), p)
	if err == nil || !present || s.Phase != PhaseConflict {
		t.Fatalf("state=%+v present=%v err=%v", s, present, err)
	}
}

func TestFinalizeFailureRestoresPreMigrationNexusState(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("[old]\ntype = local\n")
	if err := os.WriteFile(p.ManagedRcloneConfig, old, 0o600); err != nil {
		t.Fatal(err)
	}
	v, _ := PreviewMigration(p, PreviewRequest{})
	if _, err := Apply(context.Background(), p, v); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ProviderModuleDir, "disable"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneConfig, []byte("ENCRYPTED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Finalize(context.Background(), p)
	if err == nil || s.Phase != PhaseRolledBack {
		t.Fatalf("state=%+v err=%v", s, err)
	}
	b, _ := os.ReadFile(p.ManagedRcloneConfig)
	if string(b) != string(old) {
		t.Fatalf("rollback config=%q", b)
	}
}

func TestMountSelectionIsExplicitAndPublicIDIsPrintable(t *testing.T) {
	p := testPaths(t)
	proc := filepath.Join(t.TempDir(), "123")
	if err := os.MkdirAll(proc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "cmdline"), []byte(p.ProviderModuleDir+"/bin/rclone\x00mount\x00demo:\x00/tmp/explicit\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_MIGRATION_PROC_ROOT", filepath.Dir(proc))
	d, err := Detect(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Mounts) != 1 || strings.ContainsRune(d.Mounts[0].ID, '\x00') || !strings.HasPrefix(d.Mounts[0].ID, "mount:") {
		t.Fatalf("mount=%+v", d.Mounts)
	}
	v, err := PreviewMigration(p, PreviewRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.SelectedMounts) != 0 {
		t.Fatalf("mounts must be explicit, got %+v", v.SelectedMounts)
	}
	v, err = PreviewMigration(p, PreviewRequest{SelectedMounts: []string{d.Mounts[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.SelectedMounts) != 1 {
		t.Fatalf("explicit mount not selected: %+v", v)
	}
}

func TestFinalizeStartsOnlyExplicitlySelectedMounts(t *testing.T) {
	p := testPaths(t)
	t.Setenv("RNEXUS_START_GRACE_SECONDS", "0")
	t.Setenv("RNEXUS_STOP_TIMEOUT_SECONDS", "1")

	runner := filepath.Join(p.ProviderModuleDir, "provider-mount.sh")
	if err := os.WriteFile(runner, []byte("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mountA := filepath.Join(root, "selected")
	mountB := filepath.Join(root, "not-selected")
	cmds := []*exec.Cmd{
		exec.Command("/bin/sh", runner, "mount", "demo:", mountA),
		exec.Command("/bin/sh", runner, "mount", "demo:", mountB),
	}
	for _, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, cmd := range cmds {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for len(scanProcesses(p.ProviderModuleDir)) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	d, err := Detect(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Mounts) != 2 {
		t.Fatalf("provider mounts=%+v", d.Mounts)
	}
	var selected, unselected MountCandidate
	for _, m := range d.Mounts {
		switch filepath.Clean(m.Mountpoint) {
		case filepath.Clean(mountA):
			selected = m
		case filepath.Clean(mountB):
			unselected = m
		}
	}
	if selected.ID == "" || unselected.ID == "" {
		t.Fatalf("could not identify selected/unselected candidates: %+v", d.Mounts)
	}

	preview, err := PreviewMigration(p, PreviewRequest{SelectedMounts: []string{selected.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.SelectedMounts) != 1 || preview.SelectedMounts[0].Name != selected.Name {
		t.Fatalf("review selection drifted: %+v", preview.SelectedMounts)
	}
	state, err := Apply(context.Background(), p, preview)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseAwaitingProviderDisable || len(state.ImportedMounts) != 1 || state.ImportedMounts[0] != selected.Name {
		t.Fatalf("migration state does not bind reviewed mount: %+v", state)
	}
	for _, cmd := range cmds {
		_ = cmd.Wait()
	}

	registry, err := mounts.LoadRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Mounts) != 1 || registry.Mounts[0].Name != selected.Name || registry.Mounts[0].Enabled {
		t.Fatalf("apply must import only reviewed mount and keep it disabled: %+v", registry.Mounts)
	}
	if status := mounts.StatusOne(p, unselected.Name); status.State != "not-configured" {
		t.Fatalf("unselected provider mount entered Nexus authority before finalize: %+v", status)
	}

	if err := os.WriteFile(filepath.Join(p.ProviderModuleDir, "disable"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = Finalize(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseCompleted {
		t.Fatalf("phase=%s", state.Phase)
	}
	defer func() { _, _ = mounts.Stop(context.Background(), p, selected.Name) }()

	selectedStatus := mounts.StatusOne(p, selected.Name)
	if selectedStatus.State != "running" || selectedStatus.PID <= 0 {
		t.Fatalf("explicitly selected Nexus mount was not started: %+v", selectedStatus)
	}
	unselectedStatus := mounts.StatusOne(p, unselected.Name)
	if unselectedStatus.State != "not-configured" {
		t.Fatalf("unselected provider mount became Nexus-defined/running: %+v", unselectedStatus)
	}
	registry, err = mounts.LoadRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Mounts) != 1 || registry.Mounts[0].Name != selected.Name || !registry.Mounts[0].Enabled {
		t.Fatalf("final authority registry does not contain exactly the reviewed mount enabled: %+v", registry.Mounts)
	}
}
