package runtimeupdate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimesource"
	"rclone-nexus/internal/runtimestate"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	return paths.Paths{StateDir: filepath.Join(root, "state"), RuntimeDir: filepath.Join(root, "state", "runtime"), RuntimeStoreDir: filepath.Join(root, "state", "runtimes")}.Normalize()
}

func TestDefaultPolicyMatchesSafePersonalUpdateDefaults(t *testing.T) {
	p := DefaultPolicy()
	if !p.CheckAutomatically || !p.AcquireAutomatically || !p.QualifyAutomatically || !p.StageAutomatically {
		t.Fatalf("automatic safe pipeline defaults changed: %+v", p)
	}
	if p.ActivationMode != ActivationNextReboot || p.RestartActiveMountsAutomatically {
		t.Fatalf("activation defaults permit hot swap: %+v", p)
	}
	if p.SourceID != "bclone" || p.RetainHistory < 1 {
		t.Fatalf("source/history defaults changed: %+v", p)
	}
}

func TestPolicyRejectsImmediateActivationWithoutMountRestartAuthority(t *testing.T) {
	p := DefaultPolicy()
	p.ActivationMode = ActivationImmediate
	p.RestartActiveMountsAutomatically = false
	if _, err := validatePolicy(p); err == nil {
		t.Fatal("unsafe immediate activation policy accepted")
	}
}

func TestSnapshotReportsMissingStagedCandidateWithoutInventingRuntime(t *testing.T) {
	p := testPaths(t)
	state := runtimestate.State{SchemaVersion: runtimestate.SchemaVersion, Phase: runtimestate.PhaseActive, StagedRuntimeID: "missing-runtime", StagedBinarySHA256: strings.Repeat("a", 64)}
	if err := runtimestate.Save(p, state); err != nil {
		t.Fatal(err)
	}
	snap, err := SnapshotOf(p)
	if err != nil {
		t.Fatal(err)
	}
	if snap.StagedRuntimeID != "missing-runtime" || !snap.StagedMissing {
		t.Fatalf("missing staged candidate not surfaced: %+v", snap)
	}
}

func TestBootActivationClearsDisappearedStagedCandidateAndFailsClosed(t *testing.T) {
	p := testPaths(t)
	state := runtimestate.State{SchemaVersion: runtimestate.SchemaVersion, Phase: runtimestate.PhaseActive, StagedRuntimeID: "missing-runtime", StagedBinarySHA256: strings.Repeat("b", 64)}
	if err := runtimestate.Save(p, state); err != nil {
		t.Fatal(err)
	}
	_, changed, err := BootActivate(context.Background(), p, nil)
	if err == nil || changed {
		t.Fatalf("missing staged candidate should fail closed changed=%v err=%v", changed, err)
	}
	got, ok, loadErr := runtimestate.Load(p)
	if loadErr != nil || !ok {
		t.Fatalf("load=%v ok=%v", loadErr, ok)
	}
	if got.StagedRuntimeID != "" || got.StagedBinarySHA256 != "" {
		t.Fatalf("stale staged identity survived: %+v", got)
	}
	update, readErr := loadState(p)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if update.LastResult != "staged-missing" || update.Retryable {
		t.Fatalf("unexpected recovery state: %+v", update)
	}
}

func TestOfflineUpdateCheckIsRetryableAndDoesNotPublishActivationState(t *testing.T) {
	p := testPaths(t)
	spec := runtimesource.Spec{ID: "offline-fixture", Engine: "rclone", Kind: runtimesource.KindURL, DefaultChannel: runtimesource.ChannelManualOnly, URL: "http://127.0.0.1:1/rclone", ExpectedSHA256: strings.Repeat("c", 64)}
	if _, err := runtimesource.Register(p, spec); err != nil {
		t.Fatal(err)
	}
	_, err := Check(context.Background(), p, "offline-fixture")
	if err == nil {
		t.Fatal("offline update check unexpectedly succeeded")
	}
	snap, snapErr := SnapshotOf(p)
	if snapErr != nil {
		t.Fatal(snapErr)
	}
	if !snap.State.Retryable || snap.State.LastResult != "acquire-retryable" {
		t.Fatalf("offline state is not retryable: %+v", snap.State)
	}
	if snap.CurrentRuntimeID != "" || snap.StagedRuntimeID != "" {
		t.Fatalf("offline check corrupted activation state: %+v", snap)
	}
}

func TestSanitizeErrorRemovesURLCredentialsAndQuery(t *testing.T) {
	err := errors.New("fetch https://user:secret@example.invalid/path?token=abc failed")
	got := sanitizeError(err)
	if strings.Contains(got, "secret") || strings.Contains(got, "token=") || strings.Contains(got, "user:") {
		t.Fatalf("credential leak: %q", got)
	}
	if !strings.Contains(got, "https://example.invalid/path") {
		t.Fatalf("sanitized URL lost useful origin/path: %q", got)
	}
}

func TestUpdateStateAndPolicyFilesArePrivate(t *testing.T) {
	p := testPaths(t)
	if _, err := SavePolicy(p, DefaultPolicy()); err != nil {
		t.Fatal(err)
	}
	state := State{SchemaVersion: StateSchemaVersion, LastResult: "fixture"}
	if err := saveState(p, state); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{p.RuntimeUpdatePolicy, p.RuntimeUpdateState} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%#o", name, info.Mode().Perm())
		}
	}
}

func TestStateRoundTripRejectsUnsupportedSchema(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(p.RuntimeUpdateState), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(State{SchemaVersion: 99})
	if err := os.WriteFile(p.RuntimeUpdateState, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(p); err == nil {
		t.Fatal("unsupported state schema accepted")
	}
}

func TestDisappearedSourceFailsClosedWithoutChangingActivationState(t *testing.T) {
	p := testPaths(t)
	_, err := Check(context.Background(), p, "source-that-does-not-exist")
	if err == nil {
		t.Fatal("missing source unexpectedly resolved")
	}
	if _, ok, loadErr := runtimestate.Load(p); loadErr != nil || ok {
		t.Fatalf("missing source changed activation state ok=%v err=%v", ok, loadErr)
	}
}
