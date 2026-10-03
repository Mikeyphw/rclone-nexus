package runtimeactivation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimestate"
	"rclone-nexus/internal/runtimestore"
)

const receiptSchemaVersion = 1

type Progress func(phase, message string, data any)

type Transition struct {
	Phase     runtimestate.Phase `json:"phase"`
	UnixMS    int64              `json:"unix_ms"`
	Active    string             `json:"active_runtime_id,omitempty"`
	Previous  string             `json:"previous_runtime_id,omitempty"`
	Candidate string             `json:"candidate_runtime_id,omitempty"`
	Quiesced  []string           `json:"quiesced_mounts,omitempty"`
	Message   string             `json:"message,omitempty"`
}

type Receipt struct {
	SchemaVersion      int          `json:"schema_version"`
	TransactionID      string       `json:"transaction_id"`
	Action             string       `json:"action"`
	RequestedRuntimeID string       `json:"requested_runtime_id,omitempty"`
	StartedUnixMS      int64        `json:"started_unix_ms"`
	FinishedUnixMS     int64        `json:"finished_unix_ms,omitempty"`
	Outcome            string       `json:"outcome,omitempty"`
	Transitions        []Transition `json:"transitions"`
	Evidence           []string     `json:"evidence,omitempty"`
	Error              string       `json:"error,omitempty"`
}

type Status struct {
	Present              bool               `json:"present"`
	State                runtimestate.State `json:"state"`
	TransitionInProgress bool               `json:"transition_in_progress"`
	ActiveBinary         string             `json:"active_binary,omitempty"`
	Projection           string             `json:"projection,omitempty"`
	ProjectionOK         bool               `json:"projection_ok"`
	StatePath            string             `json:"state_path"`
	ReceiptPath          string             `json:"receipt_path,omitempty"`
}

type Result struct {
	Action        string                 `json:"action"`
	TransactionID string                 `json:"transaction_id,omitempty"`
	State         runtimestate.State     `json:"state"`
	Changed       bool                   `json:"changed"`
	Recovered     bool                   `json:"recovered,omitempty"`
	ReceiptPath   string                 `json:"receipt_path,omitempty"`
	Reconcile     mounts.ReconcileReport `json:"reconcile"`
}

type candidate struct {
	ID     string
	Digest string
	Binary string
}

type dependencies struct {
	verifyCandidate func(context.Context, string) (candidate, error)
	bootstrap       func(context.Context) (candidate, bool, error)
	desired         func() ([]string, error)
	quiesce         func(context.Context, string) (mounts.ActionResult, error)
	reconcile       func(context.Context, func(string, string)) (mounts.ReconcileReport, error)
	verifyDesired   func([]string) error
	project         func(candidate) error
	verifyExisting  func(string, string) (candidate, error)
}

type controller struct {
	p        paths.Paths
	deps     dependencies
	progress Progress
	receipt  *Receipt
}

func transactionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return fmt.Sprintf("rtx-%d-%s", time.Now().UnixMilli(), hex.EncodeToString(buf))
	}
	return fmt.Sprintf("rtx-%d-%d", time.Now().UnixMilli(), os.Getpid())
}

func withLock(p paths.Paths, fn func() error) error {
	p = p.Normalize()
	if err := os.MkdirAll(filepath.Dir(p.RuntimeActivationLock), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p.RuntimeActivationLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock runtime activation: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	if len(value) > 2048 {
		value = value[:2048]
	}
	return value
}

func receiptPath(p paths.Paths, tx string) string {
	return filepath.Join(p.Normalize().RuntimeTransactionsDir, tx+".json")
}

func writeReceipt(p paths.Paths, receipt Receipt) error {
	p = p.Normalize()
	if receipt.TransactionID == "" || strings.ContainsAny(receipt.TransactionID, "/\\\x00\r\n") {
		return errors.New("invalid runtime transaction id")
	}
	if err := os.MkdirAll(p.RuntimeTransactionsDir, 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	path := receiptPath(p, receipt.TransactionID)
	tmp, err := os.CreateTemp(p.RuntimeTransactionsDir, ".transaction-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	h, err := os.Open(p.RuntimeTransactionsDir)
	if err != nil {
		return err
	}
	syncErr := h.Sync()
	closeErr := h.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func (c *controller) emit(phase runtimestate.Phase, message string, data any) {
	if c.progress != nil {
		c.progress(string(phase), message, data)
	}
}

func (c *controller) persist(state *runtimestate.State, message string) error {
	state.Generation++
	// Keep the in-memory state timestamp identical to the durable transition
	// timestamp so transaction receipts never record zero/stale transition time.
	state.UpdatedUnixMS = time.Now().UnixMilli()
	if err := runtimestate.Save(c.p, *state); err != nil {
		return err
	}
	if c.receipt != nil {
		c.receipt.Transitions = append(c.receipt.Transitions, Transition{
			Phase: state.Phase, UnixMS: state.UpdatedUnixMS, Active: state.ActiveRuntimeID,
			Previous: state.PreviousRuntimeID, Candidate: state.CandidateRuntimeID,
			Quiesced: append([]string(nil), state.QuiescedMounts...), Message: message,
		})
		if err := writeReceipt(c.p, *c.receipt); err != nil {
			return err
		}
	}
	c.emit(state.Phase, message, *state)
	return nil
}

func realDependencies(p paths.Paths) dependencies {
	p = p.Normalize()
	return dependencies{
		verifyCandidate: func(ctx context.Context, id string) (candidate, error) {
			manifest, err := runtimestore.Inspect(p, id)
			if err != nil {
				return candidate{}, err
			}
			if !manifest.Qualification.Qualified || manifest.Qualification.State != "qualified" {
				return candidate{}, errors.New("runtime candidate is not qualified")
			}
			// Activation never trusts a stale qualification bit. Re-run the full X02
			// verifier against the immutable bytes immediately before switching.
			manifest, err = runtimestore.Test(ctx, p, id)
			if err != nil {
				return candidate{}, err
			}
			if !manifest.Qualification.Qualified || manifest.Qualification.State != "qualified" {
				return candidate{}, errors.New("runtime candidate failed activation-time qualification")
			}
			inspected, err := runtimestore.Inspect(p, id)
			if err != nil {
				return candidate{}, err
			}
			return candidate{ID: id, Digest: inspected.BinarySHA256, Binary: runtimestore.BinaryPath(p, id)}, nil
		},
		bootstrap: func(ctx context.Context) (candidate, bool, error) {
			info, err := os.Stat(p.ManagedRcloneBin)
			if os.IsNotExist(err) {
				return candidate{}, false, nil
			}
			if err != nil {
				return candidate{}, false, err
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return candidate{}, false, errors.New("managed bootstrap runtime is not executable")
			}
			manifest, err := runtimestore.Import(ctx, p, runtimestore.ImportRequest{
				Engine: "rclone", SourceType: runtimestore.SourceExecutablePath, Path: p.ManagedRcloneBin,
			})
			if err != nil {
				return candidate{}, false, fmt.Errorf("qualify current runtime for rollback: %w", err)
			}
			return candidate{ID: manifest.RuntimeID, Digest: manifest.BinarySHA256, Binary: runtimestore.BinaryPath(p, manifest.RuntimeID)}, true, nil
		},
		desired: func() ([]string, error) {
			names, err := mounts.List(p)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(names))
			for _, name := range names {
				cfg, err := mounts.Parse(p, name)
				if err != nil {
					return nil, err
				}
				if mounts.DesiredState(p, cfg) == mounts.DesiredRunning {
					out = append(out, name)
				}
			}
			sort.Strings(out)
			return out, nil
		},
		quiesce: func(ctx context.Context, name string) (mounts.ActionResult, error) {
			return mounts.QuiesceOwned(ctx, p, name)
		},
		reconcile: func(ctx context.Context, progress func(string, string)) (mounts.ReconcileReport, error) {
			return mounts.ReconcileRuntimeTransition(ctx, p, progress)
		},
		verifyDesired: func(names []string) error {
			for _, name := range names {
				status := mounts.StatusOne(p, name)
				if status.State != "running" || !status.Managed {
					return fmt.Errorf("desired mount %s did not restart under Nexus ownership", name)
				}
			}
			return nil
		},
		project: func(c candidate) error { return projectActive(p, c) },
		verifyExisting: func(id, digest string) (candidate, error) {
			manifest, err := runtimestore.Inspect(p, id)
			if err != nil {
				return candidate{}, err
			}
			if !manifest.Qualification.Qualified || manifest.BinarySHA256 != digest {
				return candidate{}, errors.New("previous runtime is not a qualified immutable candidate")
			}
			binary, err := runtimestate.VerifyRuntimeBytes(p, id, digest)
			if err != nil {
				return candidate{}, err
			}
			return candidate{ID: id, Digest: digest, Binary: binary}, nil
		},
	}
}

func projectActive(p paths.Paths, c candidate) error {
	p = p.Normalize()
	if c.ID == "" || c.Digest == "" {
		return errors.New("cannot project empty runtime candidate")
	}
	verified, err := runtimestate.VerifyRuntimeBytes(p, c.ID, c.Digest)
	if err != nil {
		return err
	}
	if filepath.Clean(verified) != filepath.Clean(c.Binary) {
		return errors.New("candidate binary path changed before projection")
	}
	dir := filepath.Dir(p.ManagedRcloneBin)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".rclone-active-%d-%d", os.Getpid(), time.Now().UnixNano()))
	_ = os.Remove(tmp)
	if err := os.Symlink(c.Binary, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	// Rename replaces only the directory entry. Any already-running rclone
	// process keeps its original inode; activation never overwrites executable
	// bytes underneath a live process.
	if err := os.Rename(tmp, p.ManagedRcloneBin); err != nil {
		return err
	}
	h, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := h.Sync()
	closeErr := h.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func StatusOf(p paths.Paths) (Status, error) {
	p = p.Normalize()
	state, ok, err := runtimestate.Load(p)
	out := Status{Present: ok, State: state, StatePath: p.RuntimeActivationState}
	if err != nil || !ok {
		return out, err
	}
	out.TransitionInProgress = runtimestate.TransitionInProgressState(state)
	if state.TransactionID != "" {
		out.ReceiptPath = receiptPath(p, state.TransactionID)
	}
	if state.ActiveRuntimeID == "" {
		return out, nil
	}
	binary, err := runtimestate.VerifyRuntimeBytes(p, state.ActiveRuntimeID, state.ActiveBinarySHA256)
	if err != nil {
		return out, err
	}
	out.ActiveBinary = binary
	out.Projection = p.ManagedRcloneBin
	resolved, err := filepath.EvalSymlinks(p.ManagedRcloneBin)
	if err == nil && filepath.Clean(resolved) == filepath.Clean(binary) {
		out.ProjectionOK = true
	}
	return out, nil
}

func Activate(ctx context.Context, p paths.Paths, id string, progress Progress) (Result, error) {
	p = p.Normalize()
	var result Result
	err := withLock(p, func() error {
		c := controller{p: p, deps: realDependencies(p), progress: progress}
		var err error
		result, err = c.activateLocked(ctx, id, "activate")
		return err
	})
	return result, err
}

func Rollback(ctx context.Context, p paths.Paths, progress Progress) (Result, error) {
	p = p.Normalize()
	var result Result
	err := withLock(p, func() error {
		c := controller{p: p, deps: realDependencies(p), progress: progress}
		state, ok, err := runtimestate.Load(p)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("runtime activation state does not exist")
		}
		if runtimestate.TransitionInProgressState(state) {
			if _, err := c.recoverLocked(ctx); err != nil {
				return err
			}
			state, _, err = runtimestate.Load(p)
			if err != nil {
				return err
			}
		}
		if state.PreviousRuntimeID == "" {
			return errors.New("previous runtime is unavailable")
		}
		result, err = c.activateLocked(ctx, state.PreviousRuntimeID, "rollback")
		return err
	})
	return result, err
}

func Recover(ctx context.Context, p paths.Paths, progress Progress) (Result, error) {
	p = p.Normalize()
	var result Result
	err := withLock(p, func() error {
		c := controller{p: p, deps: realDependencies(p), progress: progress}
		var err error
		result, err = c.recoverLocked(ctx)
		return err
	})
	return result, err
}

func (c *controller) beginReceipt(action, requested string) *Receipt {
	now := time.Now().UnixMilli()
	return &Receipt{SchemaVersion: receiptSchemaVersion, TransactionID: transactionID(), Action: action, RequestedRuntimeID: requested, StartedUnixMS: now, Transitions: []Transition{}}
}

func (c *controller) finishReceipt(outcome string, err error) error {
	if c.receipt == nil {
		return nil
	}
	c.receipt.FinishedUnixMS = time.Now().UnixMilli()
	c.receipt.Outcome = outcome
	c.receipt.Error = sanitizeError(err)
	return writeReceipt(c.p, *c.receipt)
}

func (c *controller) ensureStableBase(ctx context.Context) (runtimestate.State, error) {
	state, ok, err := runtimestate.Load(c.p)
	if err != nil {
		return runtimestate.State{}, err
	}
	if ok {
		if runtimestate.TransitionInProgressState(state) {
			return runtimestate.State{}, errors.New("runtime activation recovery is required")
		}
		if state.ActiveRuntimeID != "" {
			if _, err := c.deps.verifyExisting(state.ActiveRuntimeID, state.ActiveBinarySHA256); err != nil {
				return runtimestate.State{}, fmt.Errorf("active runtime integrity failed: %w", err)
			}
		}
		return state, nil
	}
	bootstrap, present, err := c.deps.bootstrap(ctx)
	if err != nil {
		return runtimestate.State{}, err
	}
	state = runtimestate.State{SchemaVersion: runtimestate.SchemaVersion, Phase: runtimestate.PhaseActive}
	if !present {
		return state, nil
	}
	// Convert the pre-X03 managed path to a projection of a qualified immutable
	// store entry before publishing activation state. The bytes are identical to
	// the runtime that was already selected, so this does not hot-swap a process.
	if err := c.deps.project(bootstrap); err != nil {
		return runtimestate.State{}, err
	}
	state.ActiveRuntimeID = bootstrap.ID
	state.ActiveBinarySHA256 = bootstrap.Digest
	if err := runtimestate.Save(c.p, state); err != nil {
		return runtimestate.State{}, err
	}
	return state, nil
}

func (c *controller) activateLocked(ctx context.Context, id, action string) (Result, error) {
	if state, ok, err := runtimestate.Load(c.p); err != nil {
		return Result{}, err
	} else if ok && runtimestate.TransitionInProgressState(state) {
		if _, err := c.recoverLocked(ctx); err != nil {
			return Result{}, err
		}
	}

	candidateRuntime, err := c.deps.verifyCandidate(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("activation candidate verification failed: %w", err)
	}
	base, err := c.ensureStableBase(ctx)
	if err != nil {
		return Result{}, err
	}
	if base.ActiveRuntimeID == candidateRuntime.ID && base.ActiveBinarySHA256 == candidateRuntime.Digest {
		return Result{Action: action, State: base, Changed: false}, nil
	}
	desired, err := c.deps.desired()
	if err != nil {
		return Result{}, err
	}
	if base.ActiveRuntimeID == "" && len(desired) != 0 {
		return Result{}, errors.New("cannot activate desired mounts without a qualified rollback runtime")
	}

	c.receipt = c.beginReceipt(action, id)
	state := runtimestate.State{
		SchemaVersion:   runtimestate.SchemaVersion,
		Generation:      base.Generation,
		Phase:           runtimestate.PhaseStaged,
		TransactionID:   c.receipt.TransactionID,
		ActiveRuntimeID: base.ActiveRuntimeID, ActiveBinarySHA256: base.ActiveBinarySHA256,
		PreviousRuntimeID: base.ActiveRuntimeID, PreviousBinarySHA256: base.ActiveBinarySHA256,
		CandidateRuntimeID: candidateRuntime.ID, CandidateBinarySHA256: candidateRuntime.Digest,
		StagedRuntimeID: candidateRuntime.ID, StagedBinarySHA256: candidateRuntime.Digest,
		DesiredMounts: append([]string(nil), desired...), StartedUnixMS: c.receipt.StartedUnixMS,
	}
	c.receipt.Evidence = []string{c.p.RuntimeActivationState, candidateRuntime.Binary}
	if state.PreviousRuntimeID != "" {
		if previousBinary, pathErr := runtimestate.RuntimeBinaryPath(c.p, state.PreviousRuntimeID); pathErr == nil {
			c.receipt.Evidence = append(c.receipt.Evidence, previousBinary)
		}
	}
	if err := c.persist(&state, "candidate staged after activation-time qualification"); err != nil {
		return Result{}, err
	}

	state.Phase = runtimestate.PhaseQuiescing
	if err := c.persist(&state, "quiescing Nexus-owned mounts while preserving desired state"); err != nil {
		return Result{}, err
	}
	for _, name := range desired {
		result, quiesceErr := c.deps.quiesce(ctx, name)
		if quiesceErr != nil {
			failure := fmt.Errorf("quiesce %s: %w", name, quiesceErr)
			state.LastError = sanitizeError(failure)
			rollbackResult, rollbackErr := c.rollbackFailedActivation(ctx, &state, candidateRuntime, failure)
			if rollbackErr != nil {
				return rollbackResult, errors.Join(failure, rollbackErr)
			}
			return rollbackResult, failure
		}
		if !result.Noop {
			state.QuiescedMounts = append(state.QuiescedMounts, name)
			if err := c.persist(&state, "quiesced "+name); err != nil {
				return Result{}, err
			}
		}
	}

	// Hash/qualification is checked again after quiesce. A staged file that was
	// removed or changed cannot become active merely because it qualified earlier.
	candidateRuntime, err = c.deps.verifyCandidate(ctx, id)
	if err != nil {
		failure := fmt.Errorf("candidate changed or failed requalification after quiesce: %w", err)
		state.LastError = sanitizeError(failure)
		rollbackResult, rollbackErr := c.rollbackFailedActivation(ctx, &state, candidateRuntime, failure)
		if rollbackErr != nil {
			return rollbackResult, errors.Join(failure, rollbackErr)
		}
		return rollbackResult, failure
	}
	state.CandidateBinarySHA256 = candidateRuntime.Digest
	state.StagedBinarySHA256 = candidateRuntime.Digest
	state.ActiveRuntimeID = candidateRuntime.ID
	state.ActiveBinarySHA256 = candidateRuntime.Digest
	state.Phase = runtimestate.PhaseActivePendingVerify
	if err := c.persist(&state, "candidate selected; restart verification pending"); err != nil {
		return Result{}, err
	}
	if err := c.deps.project(candidateRuntime); err != nil {
		failure := fmt.Errorf("publish active runtime projection: %w", err)
		rollbackResult, rollbackErr := c.rollbackFailedActivation(ctx, &state, candidateRuntime, failure)
		if rollbackErr != nil {
			return rollbackResult, errors.Join(failure, rollbackErr)
		}
		return rollbackResult, failure
	}

	report, reconcileErr := c.deps.reconcile(ctx, func(name, mountState string) {
		c.emit(runtimestate.PhaseActivePendingVerify, "mount "+name+" "+mountState, map[string]string{"name": name, "state": mountState})
	})
	if reconcileErr == nil && len(report.Failures) != 0 {
		reconcileErr = fmt.Errorf("candidate restart produced %d mount failure(s)", len(report.Failures))
	}
	if reconcileErr == nil {
		reconcileErr = c.deps.verifyDesired(desired)
	}
	if reconcileErr != nil {
		failure := fmt.Errorf("candidate startup verification failed: %w", reconcileErr)
		state.LastError = sanitizeError(failure)
		rollbackResult, rollbackErr := c.rollbackFailedActivation(ctx, &state, candidateRuntime, failure)
		rollbackResult.Reconcile = report
		if rollbackErr != nil {
			return rollbackResult, errors.Join(failure, rollbackErr)
		}
		return rollbackResult, failure
	}

	state.Phase = runtimestate.PhaseActive
	state.CandidateRuntimeID = ""
	state.CandidateBinarySHA256 = ""
	state.StagedRuntimeID = ""
	state.StagedBinarySHA256 = ""
	state.QuiescedMounts = nil
	state.LastError = ""
	state.Recovery = ""
	if err := c.persist(&state, "candidate proved stable and is now active"); err != nil {
		return Result{}, err
	}
	if err := c.finishReceipt("ACTIVE", nil); err != nil {
		return Result{}, err
	}
	return Result{Action: action, TransactionID: state.TransactionID, State: state, Changed: true, ReceiptPath: receiptPath(c.p, state.TransactionID), Reconcile: report}, nil
}

func (c *controller) rollbackFailedActivation(ctx context.Context, state *runtimestate.State, candidateRuntime candidate, cause error) (Result, error) {
	state.Phase = runtimestate.PhaseRollback
	state.LastError = sanitizeError(cause)
	if err := c.persist(state, "activation failed; rolling back"); err != nil {
		return Result{State: *state}, err
	}
	result, err := c.restorePrevious(ctx, state, "automatic rollback")
	if err != nil {
		_ = c.finishReceipt("DEGRADED_RECOVERED", errors.Join(cause, err))
		return result, err
	}
	_ = candidateRuntime
	if finishErr := c.finishReceipt(string(result.State.Phase), cause); finishErr != nil {
		return result, finishErr
	}
	return result, nil
}

func (c *controller) restorePrevious(ctx context.Context, state *runtimestate.State, reason string) (Result, error) {
	if state.PreviousRuntimeID == "" || state.PreviousBinarySHA256 == "" {
		state.Phase = runtimestate.PhaseDegradedRecovered
		state.Recovery = "rollback target unavailable"
		state.LastError = strings.TrimSpace(state.LastError + "; previous runtime missing")
		if err := c.persist(state, "rollback target is unavailable"); err != nil {
			return Result{State: *state}, err
		}
		return Result{Action: "recover", TransactionID: state.TransactionID, State: *state, Changed: true, Recovered: true, ReceiptPath: receiptPath(c.p, state.TransactionID)}, errors.New("previous runtime is unavailable for rollback")
	}
	previous, err := c.deps.verifyExisting(state.PreviousRuntimeID, state.PreviousBinarySHA256)
	if err != nil {
		state.Phase = runtimestate.PhaseDegradedRecovered
		state.Recovery = "previous runtime integrity failed"
		state.LastError = sanitizeError(err)
		if saveErr := c.persist(state, "previous runtime cannot be restored"); saveErr != nil {
			return Result{State: *state}, errors.Join(err, saveErr)
		}
		return Result{Action: "recover", TransactionID: state.TransactionID, State: *state, Changed: true, Recovered: true, ReceiptPath: receiptPath(c.p, state.TransactionID)}, err
	}
	state.ActiveRuntimeID = previous.ID
	state.ActiveBinarySHA256 = previous.Digest
	if err := c.deps.project(previous); err != nil {
		state.Phase = runtimestate.PhaseDegradedRecovered
		state.Recovery = "previous runtime projection failed"
		state.LastError = sanitizeError(err)
		_ = c.persist(state, "previous runtime projection failed")
		return Result{State: *state}, err
	}
	report, reconcileErr := c.deps.reconcile(ctx, func(name, mountState string) {
		c.emit(runtimestate.PhaseRollback, "rollback mount "+name+" "+mountState, map[string]string{"name": name, "state": mountState})
	})
	if reconcileErr == nil && len(report.Failures) != 0 {
		reconcileErr = fmt.Errorf("rollback restart produced %d mount failure(s)", len(report.Failures))
	}
	if reconcileErr == nil {
		reconcileErr = c.deps.verifyDesired(state.DesiredMounts)
	}
	if reconcileErr != nil {
		state.Phase = runtimestate.PhaseDegradedRecovered
		state.Recovery = reason + ": previous runtime restored but one or more desired mounts failed"
		state.LastError = sanitizeError(reconcileErr)
	} else {
		state.Phase = runtimestate.PhaseRecovered
		state.Recovery = reason
	}
	// Preserve the failed candidate as staged so a user can inspect/retest it,
	// while active execution is once again bound to the previous immutable ID.
	state.StagedRuntimeID = state.CandidateRuntimeID
	state.StagedBinarySHA256 = state.CandidateBinarySHA256
	state.CandidateRuntimeID = ""
	state.CandidateBinarySHA256 = ""
	state.QuiescedMounts = nil
	if err := c.persist(state, reason); err != nil {
		return Result{State: *state, Reconcile: report}, err
	}
	result := Result{Action: "recover", TransactionID: state.TransactionID, State: *state, Changed: true, Recovered: true, ReceiptPath: receiptPath(c.p, state.TransactionID), Reconcile: report}
	return result, reconcileErr
}

func (c *controller) recoverLocked(ctx context.Context) (Result, error) {
	state, ok, err := runtimestate.Load(c.p)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{Action: "recover", Changed: false}, nil
	}
	if !runtimestate.TransitionInProgressState(state) {
		if state.ActiveRuntimeID != "" {
			active, verifyErr := c.deps.verifyExisting(state.ActiveRuntimeID, state.ActiveBinarySHA256)
			if verifyErr != nil {
				return Result{Action: "recover", State: state}, verifyErr
			}
			if err := c.deps.project(active); err != nil {
				return Result{Action: "recover", State: state}, err
			}
		}
		return Result{Action: "recover", State: state, Changed: false}, nil
	}

	c.receipt = &Receipt{SchemaVersion: receiptSchemaVersion, TransactionID: state.TransactionID, Action: "recover", RequestedRuntimeID: state.CandidateRuntimeID, StartedUnixMS: time.Now().UnixMilli()}
	if data, readErr := os.ReadFile(receiptPath(c.p, state.TransactionID)); readErr == nil {
		var existing Receipt
		if json.Unmarshal(data, &existing) == nil && existing.TransactionID == state.TransactionID {
			c.receipt = &existing
		}
	}

	switch state.Phase {
	case runtimestate.PhaseStaged:
		// No mount or pointer mutation is guaranteed to have happened yet. Keep
		// the candidate staged for retry and return to the stable active runtime.
		state.Phase = runtimestate.PhaseRecovered
		state.Recovery = "recovered staged activation before quiesce"
		state.StagedRuntimeID = state.CandidateRuntimeID
		state.StagedBinarySHA256 = state.CandidateBinarySHA256
		state.CandidateRuntimeID = ""
		state.CandidateBinarySHA256 = ""
		if state.ActiveRuntimeID != "" {
			active, verifyErr := c.deps.verifyExisting(state.ActiveRuntimeID, state.ActiveBinarySHA256)
			if verifyErr != nil {
				state.Phase = runtimestate.PhaseDegradedRecovered
				state.LastError = sanitizeError(verifyErr)
			} else if projectErr := c.deps.project(active); projectErr != nil {
				state.Phase = runtimestate.PhaseDegradedRecovered
				state.LastError = sanitizeError(projectErr)
			}
		}
		if err := c.persist(&state, state.Recovery); err != nil {
			return Result{State: state}, err
		}
		_ = c.finishReceipt(string(state.Phase), nil)
		return Result{Action: "recover", TransactionID: state.TransactionID, State: state, Changed: true, Recovered: true, ReceiptPath: receiptPath(c.p, state.TransactionID)}, nil
	case runtimestate.PhaseQuiescing:
		// Pointer still names the prior runtime. Reconcile desired mounts under
		// that prior ID and retain the candidate as staged.
		return c.restorePrevious(ctx, &state, "recovered crash during quiesce")
	case runtimestate.PhaseActivePendingVerify, runtimestate.PhaseRollback:
		// Candidate may already have been selected. Always fail safe to the
		// previous proven runtime rather than assuming startup succeeded.
		return c.restorePrevious(ctx, &state, "recovered incomplete activation by rollback")
	default:
		return Result{Action: "recover", State: state}, nil
	}
}
