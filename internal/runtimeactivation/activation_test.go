package runtimeactivation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimeauth"
	"rclone-nexus/internal/runtimestate"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{StateDir: root}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	return p
}

func testCandidate(t *testing.T, p paths.Paths, id, body string) candidate {
	t.Helper()
	dir := filepath.Join(p.RuntimeStoreDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "rclone")
	if err := os.WriteFile(binary, []byte(body), 0o500); err != nil {
		t.Fatal(err)
	}
	digest, err := runtimestate.HashFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return candidate{ID: id, Digest: digest, Binary: binary}
}

func fakeDeps(t *testing.T, p paths.Paths, old, next candidate) dependencies {
	t.Helper()
	candidates := map[string]candidate{old.ID: old, next.ID: next}
	return dependencies{
		verifyCandidate: func(_ context.Context, id string) (candidate, error) {
			c, ok := candidates[id]
			if !ok {
				return candidate{}, errors.New("candidate missing")
			}
			if _, err := runtimestate.VerifyRuntimeBytes(p, c.ID, c.Digest); err != nil {
				return candidate{}, err
			}
			return c, nil
		},
		bootstrap: func(context.Context) (candidate, bool, error) { return old, true, nil },
		desired:   func() ([]string, error) { return []string{"docs"}, nil },
		quiesce: func(context.Context, string) (mounts.ActionResult, error) {
			return mounts.ActionResult{Name: "docs", State: "stopped"}, nil
		},
		reconcile: func(context.Context, func(string, string)) (mounts.ReconcileReport, error) {
			return mounts.ReconcileReport{Changed: []mounts.ActionResult{{Name: "docs", State: "started"}}}, nil
		},
		verifyDesired: func([]string) error { return nil },
		project:       func(c candidate) error { return projectActive(p, c) },
		verifyExisting: func(id, digest string) (candidate, error) {
			c, ok := candidates[id]
			if !ok || c.Digest != digest {
				return candidate{}, errors.New("runtime missing")
			}
			if _, err := runtimestate.VerifyRuntimeBytes(p, id, digest); err != nil {
				return candidate{}, err
			}
			return c, nil
		},
	}
}

func TestActivationPersistsActivePreviousAndReverifiesCandidate(t *testing.T) {
	p := testPaths(t)
	old := testCandidate(t, p, "rclone-old", "old-runtime")
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	deps := fakeDeps(t, p, old, next)
	verifyCalls := 0
	baseVerify := deps.verifyCandidate
	deps.verifyCandidate = func(ctx context.Context, id string) (candidate, error) {
		verifyCalls++
		return baseVerify(ctx, id)
	}
	c := controller{p: p, deps: deps}
	result, err := c.activateLocked(context.Background(), next.ID, "activate")
	if err != nil {
		t.Fatal(err)
	}
	if verifyCalls != 2 {
		t.Fatalf("candidate verification calls=%d want=2", verifyCalls)
	}
	if result.State.Phase != runtimestate.PhaseActive || result.State.ActiveRuntimeID != next.ID || result.State.PreviousRuntimeID != old.ID {
		t.Fatalf("unexpected final state: %+v", result.State)
	}
	state, ok, err := runtimestate.Load(p)
	if err != nil || !ok {
		t.Fatalf("load state: ok=%v err=%v", ok, err)
	}
	if state.ActiveRuntimeID != next.ID || state.PreviousRuntimeID != old.ID {
		t.Fatalf("durable identities not persisted: %+v", state)
	}
	resolved, err := filepath.EvalSymlinks(p.ManagedRcloneBin)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(next.Binary) {
		t.Fatalf("active projection=%q err=%v want=%q", resolved, err, next.Binary)
	}
	if _, err := os.Stat(result.ReceiptPath); err != nil {
		t.Fatalf("transaction receipt not persisted: %v", err)
	}
}

func TestActivationRollbackWhenQuiesceRefuses(t *testing.T) {
	p := testPaths(t)
	old := testCandidate(t, p, "rclone-old", "old-runtime")
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	deps := fakeDeps(t, p, old, next)
	deps.quiesce = func(context.Context, string) (mounts.ActionResult, error) {
		return mounts.ActionResult{}, errors.New("mount is not provably Nexus-owned")
	}
	c := controller{p: p, deps: deps}
	result, err := c.activateLocked(context.Background(), next.ID, "activate")
	if err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	if result.State.ActiveRuntimeID != old.ID || result.State.Phase != runtimestate.PhaseRecovered {
		t.Fatalf("failed quiesce did not recover old runtime: %+v", result.State)
	}
}

func TestActivationRollbackWhenCandidateDisappearsAfterStaging(t *testing.T) {
	p := testPaths(t)
	old := testCandidate(t, p, "rclone-old", "old-runtime")
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	deps := fakeDeps(t, p, old, next)
	calls := 0
	base := deps.verifyCandidate
	deps.verifyCandidate = func(ctx context.Context, id string) (candidate, error) {
		calls++
		if calls == 2 {
			_ = os.Remove(next.Binary)
			return candidate{}, errors.New("candidate disappeared")
		}
		return base(ctx, id)
	}
	c := controller{p: p, deps: deps}
	result, err := c.activateLocked(context.Background(), next.ID, "activate")
	if err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	if result.State.ActiveRuntimeID != old.ID || result.State.Phase != runtimestate.PhaseRecovered {
		t.Fatalf("candidate disappearance did not rollback: %+v", result.State)
	}
}

func TestActivationRollbackOnStartupContractFailure(t *testing.T) {
	p := testPaths(t)
	old := testCandidate(t, p, "rclone-old", "old-runtime")
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	deps := fakeDeps(t, p, old, next)
	reconciles := 0
	deps.reconcile = func(context.Context, func(string, string)) (mounts.ReconcileReport, error) {
		reconciles++
		if reconciles == 1 {
			return mounts.ReconcileReport{Failures: []mounts.LifecycleFailure{{Name: "docs", Code: "rc_runtime_failed", Error: "new binary started but RC contract failed"}}}, nil
		}
		return mounts.ReconcileReport{Changed: []mounts.ActionResult{{Name: "docs", State: "started"}}}, nil
	}
	c := controller{p: p, deps: deps}
	result, err := c.activateLocked(context.Background(), next.ID, "activate")
	if err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	if result.State.ActiveRuntimeID != old.ID || result.State.Phase != runtimestate.PhaseRecovered {
		t.Fatalf("startup failure did not rollback: %+v", result.State)
	}
}

func TestRestorePreviousPersistsRollbackAuthorityBeforeRestart(t *testing.T) {
	p := testPaths(t)
	old := testCandidate(t, p, "rclone-old", "old-runtime")
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	deps := fakeDeps(t, p, old, next)
	state := runtimestate.State{
		SchemaVersion: runtimestate.SchemaVersion, Phase: runtimestate.PhaseActivePendingVerify, TransactionID: "rtx-crash",
		ActiveRuntimeID: next.ID, ActiveBinarySHA256: next.Digest,
		PreviousRuntimeID: old.ID, PreviousBinarySHA256: old.Digest,
		CandidateRuntimeID: next.ID, CandidateBinarySHA256: next.Digest,
		StagedRuntimeID: next.ID, StagedBinarySHA256: next.Digest,
		DesiredMounts: []string{"docs"},
	}
	if err := projectActive(p, next); err != nil {
		t.Fatal(err)
	}
	if err := runtimestate.Save(p, state); err != nil {
		t.Fatal(err)
	}

	reconcileObservedRollbackAuthority := false
	deps.reconcile = func(context.Context, func(string, string)) (mounts.ReconcileReport, error) {
		durable, ok, err := runtimestate.Load(p)
		if err != nil || !ok {
			return mounts.ReconcileReport{}, errors.New("rollback authority was not durably published before reconcile")
		}
		if durable.Phase != runtimestate.PhaseRollback || durable.ActiveRuntimeID != old.ID || durable.ActiveBinarySHA256 != old.Digest {
			return mounts.ReconcileReport{}, errors.New("reconcile observed stale candidate runtime authority")
		}
		selected, err := runtimeauth.ExecutableForTransition(p)
		if err != nil || filepath.Clean(selected) != filepath.Clean(old.Binary) {
			return mounts.ReconcileReport{}, errors.New("transition resolver did not select durable previous-runtime authority")
		}
		reconcileObservedRollbackAuthority = true
		return mounts.ReconcileReport{Changed: []mounts.ActionResult{{Name: "docs", State: "started"}}}, nil
	}

	c := controller{p: p, deps: deps}
	result, err := c.restorePrevious(context.Background(), &state, "recovered crash during candidate verification")
	if err != nil {
		t.Fatal(err)
	}
	if !reconcileObservedRollbackAuthority {
		t.Fatal("rollback reconcile ran without observing durable previous-runtime authority")
	}
	if result.State.Phase != runtimestate.PhaseRecovered || result.State.ActiveRuntimeID != old.ID {
		t.Fatalf("unexpected recovered state: %+v", result.State)
	}
}

func TestRollbackRestartPartialFailureIsDegradedRecovered(t *testing.T) {
	p := testPaths(t)
	old := testCandidate(t, p, "rclone-old", "old-runtime")
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	deps := fakeDeps(t, p, old, next)
	reconciles := 0
	deps.reconcile = func(context.Context, func(string, string)) (mounts.ReconcileReport, error) {
		reconciles++
		if reconciles == 1 {
			return mounts.ReconcileReport{}, errors.New("candidate startup failed")
		}
		return mounts.ReconcileReport{Failures: []mounts.LifecycleFailure{{Name: "docs", Error: "rollback restart failed"}}}, nil
	}
	c := controller{p: p, deps: deps}
	result, err := c.activateLocked(context.Background(), next.ID, "activate")
	if err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	if result.State.ActiveRuntimeID != old.ID || result.State.Phase != runtimestate.PhaseDegradedRecovered {
		t.Fatalf("partial rollback restart did not report degraded recovery: %+v", result.State)
	}
}

func TestRecoverEveryInFlightTransitionFailsSafeToPrevious(t *testing.T) {
	for _, phase := range []runtimestate.Phase{
		runtimestate.PhaseQuiescing,
		runtimestate.PhaseActivePendingVerify,
		runtimestate.PhaseRollback,
	} {
		t.Run(string(phase), func(t *testing.T) {
			p := testPaths(t)
			old := testCandidate(t, p, "rclone-old", "old-runtime")
			next := testCandidate(t, p, "rclone-next", "next-runtime")
			deps := fakeDeps(t, p, old, next)
			state := runtimestate.State{
				SchemaVersion: runtimestate.SchemaVersion, Phase: phase, TransactionID: "rtx-crash",
				ActiveRuntimeID: old.ID, ActiveBinarySHA256: old.Digest,
				PreviousRuntimeID: old.ID, PreviousBinarySHA256: old.Digest,
				CandidateRuntimeID: next.ID, CandidateBinarySHA256: next.Digest,
				StagedRuntimeID: next.ID, StagedBinarySHA256: next.Digest,
				DesiredMounts: []string{"docs"},
			}
			if phase == runtimestate.PhaseActivePendingVerify || phase == runtimestate.PhaseRollback {
				state.ActiveRuntimeID, state.ActiveBinarySHA256 = next.ID, next.Digest
				if err := projectActive(p, next); err != nil {
					t.Fatal(err)
				}
			} else if err := projectActive(p, old); err != nil {
				t.Fatal(err)
			}
			if err := runtimestate.Save(p, state); err != nil {
				t.Fatal(err)
			}
			c := controller{p: p, deps: deps}
			result, err := c.recoverLocked(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.State.ActiveRuntimeID != old.ID || result.State.Phase != runtimestate.PhaseRecovered {
				t.Fatalf("phase %s recovery=%+v", phase, result.State)
			}
		})
	}
}

func TestRecoverStagedKeepsCandidateForRetry(t *testing.T) {
	p := testPaths(t)
	old := testCandidate(t, p, "rclone-old", "old-runtime")
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	deps := fakeDeps(t, p, old, next)
	state := runtimestate.State{
		SchemaVersion: runtimestate.SchemaVersion, Phase: runtimestate.PhaseStaged, TransactionID: "rtx-staged",
		ActiveRuntimeID: old.ID, ActiveBinarySHA256: old.Digest,
		PreviousRuntimeID: old.ID, PreviousBinarySHA256: old.Digest,
		CandidateRuntimeID: next.ID, CandidateBinarySHA256: next.Digest,
	}
	if err := projectActive(p, old); err != nil {
		t.Fatal(err)
	}
	if err := runtimestate.Save(p, state); err != nil {
		t.Fatal(err)
	}
	c := controller{p: p, deps: deps}
	result, err := c.recoverLocked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Phase != runtimestate.PhaseRecovered || result.State.ActiveRuntimeID != old.ID || result.State.StagedRuntimeID != next.ID {
		t.Fatalf("staged recovery=%+v", result.State)
	}
}

func TestRecoverWithoutPreviousFailsClosed(t *testing.T) {
	p := testPaths(t)
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	deps := fakeDeps(t, p, candidate{ID: "unused", Digest: next.Digest, Binary: next.Binary}, next)
	state := runtimestate.State{
		SchemaVersion: runtimestate.SchemaVersion, Phase: runtimestate.PhaseActivePendingVerify, TransactionID: "rtx-no-prev",
		ActiveRuntimeID: next.ID, ActiveBinarySHA256: next.Digest,
		CandidateRuntimeID: next.ID, CandidateBinarySHA256: next.Digest,
	}
	if err := projectActive(p, next); err != nil {
		t.Fatal(err)
	}
	if err := runtimestate.Save(p, state); err != nil {
		t.Fatal(err)
	}
	c := controller{p: p, deps: deps}
	result, err := c.recoverLocked(context.Background())
	if err == nil {
		t.Fatal("recovery unexpectedly succeeded without previous runtime")
	}
	if result.State.Phase != runtimestate.PhaseDegradedRecovered {
		t.Fatalf("missing previous runtime phase=%s", result.State.Phase)
	}
}

func TestProjectionRenameDoesNotOverwriteBytesUnderOpenProcess(t *testing.T) {
	p := testPaths(t)
	next := testCandidate(t, p, "rclone-next", "next-runtime")
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneBin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneBin, []byte("currently-running-bytes"), 0o500); err != nil {
		t.Fatal(err)
	}
	open, err := os.Open(p.ManagedRcloneBin)
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	if err := projectActive(p, next); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := open.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "currently-running-bytes" {
		t.Fatalf("open inode changed underneath process: %q", string(buf[:n]))
	}
}
