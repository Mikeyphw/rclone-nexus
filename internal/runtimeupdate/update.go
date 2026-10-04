package runtimeupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimeacquire"
	"rclone-nexus/internal/runtimeactivation"
	"rclone-nexus/internal/runtimesource"
	"rclone-nexus/internal/runtimestate"
	"rclone-nexus/internal/runtimestore"
)

const (
	PolicySchemaVersion = 1
	StateSchemaVersion  = 1
)

type ActivationMode string

const (
	ActivationExplicit   ActivationMode = "explicit"
	ActivationNextReboot ActivationMode = "next-reboot"
	ActivationImmediate  ActivationMode = "immediate"
)

type Policy struct {
	SchemaVersion                    int            `json:"schema_version"`
	SourceID                         string         `json:"source_id"`
	CheckAutomatically               bool           `json:"check_automatically"`
	AcquireAutomatically             bool           `json:"acquire_automatically"`
	QualifyAutomatically             bool           `json:"qualify_automatically"`
	StageAutomatically               bool           `json:"stage_automatically"`
	ActivationMode                   ActivationMode `json:"activation_mode"`
	RestartActiveMountsAutomatically bool           `json:"restart_active_mounts_automatically"`
	CheckIntervalMinutes             int            `json:"check_interval_minutes"`
	RetainHistory                    int            `json:"retain_history"`
}

type State struct {
	SchemaVersion         int    `json:"schema_version"`
	Generation            uint64 `json:"generation"`
	LastCheckUnixMS       int64  `json:"last_check_unix_ms,omitempty"`
	LastSuccessUnixMS     int64  `json:"last_success_unix_ms,omitempty"`
	LastResult            string `json:"last_result,omitempty"`
	LastError             string `json:"last_error,omitempty"`
	Retryable             bool   `json:"retryable"`
	LastResolutionID      string `json:"last_resolution_id,omitempty"`
	CandidateRuntimeID    string `json:"candidate_runtime_id,omitempty"`
	CandidateBinarySHA256 string `json:"candidate_binary_sha256,omitempty"`
	LastGCUnixMS          int64  `json:"last_gc_unix_ms,omitempty"`
	UpdatedUnixMS         int64  `json:"updated_unix_ms"`
}

type Snapshot struct {
	Policy               Policy                   `json:"policy"`
	State                State                    `json:"state"`
	CurrentRuntimeID     string                   `json:"current_runtime_id,omitempty"`
	CurrentBinarySHA256  string                   `json:"current_binary_sha256,omitempty"`
	StagedRuntimeID      string                   `json:"staged_runtime_id,omitempty"`
	StagedBinarySHA256   string                   `json:"staged_binary_sha256,omitempty"`
	PreviousRuntimeID    string                   `json:"previous_runtime_id,omitempty"`
	PreviousBinarySHA256 string                   `json:"previous_binary_sha256,omitempty"`
	StagedMissing        bool                     `json:"staged_missing,omitempty"`
	TransitionInProgress bool                     `json:"transition_in_progress"`
	Activation           runtimeactivation.Status `json:"activation"`
}

func DefaultPolicy() Policy {
	return Policy{
		SchemaVersion:                    PolicySchemaVersion,
		SourceID:                         "bclone",
		CheckAutomatically:               true,
		AcquireAutomatically:             true,
		QualifyAutomatically:             true,
		StageAutomatically:               true,
		ActivationMode:                   ActivationNextReboot,
		RestartActiveMountsAutomatically: false,
		CheckIntervalMinutes:             360,
		RetainHistory:                    2,
	}
}

func validatePolicy(p Policy) (Policy, error) {
	p.SchemaVersion = PolicySchemaVersion
	p.SourceID = strings.TrimSpace(p.SourceID)
	if p.SourceID == "" || len(p.SourceID) > 64 || strings.ContainsAny(p.SourceID, "/\\\x00\r\n") {
		return p, errors.New("runtime update source_id is invalid")
	}
	switch p.ActivationMode {
	case ActivationExplicit, ActivationNextReboot, ActivationImmediate:
	default:
		return p, errors.New("runtime update activation_mode is invalid")
	}
	if p.ActivationMode == ActivationImmediate && !p.RestartActiveMountsAutomatically {
		return p, errors.New("immediate activation requires restart_active_mounts_automatically")
	}
	if p.CheckIntervalMinutes < 15 || p.CheckIntervalMinutes > 7*24*60 {
		return p, errors.New("runtime update check interval must be 15..10080 minutes")
	}
	if p.RetainHistory < 0 || p.RetainHistory > 32 {
		return p, errors.New("runtime update retained history must be 0..32")
	}
	if p.StageAutomatically && (!p.AcquireAutomatically || !p.QualifyAutomatically) {
		return p, errors.New("automatic staging requires automatic acquisition and qualification")
	}
	return p, nil
}

func ensureDir(p paths.Paths) error {
	return os.MkdirAll(p.Normalize().RuntimeUpdateDir, 0o700)
}

func atomicJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func LoadPolicy(p paths.Paths) (Policy, error) {
	p = p.Normalize()
	data, err := os.ReadFile(p.RuntimeUpdatePolicy)
	if os.IsNotExist(err) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return Policy{}, err
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, err
	}
	if policy.SchemaVersion != PolicySchemaVersion {
		return Policy{}, errors.New("unsupported runtime update policy schema")
	}
	return validatePolicy(policy)
}

func SavePolicy(p paths.Paths, raw Policy) (Policy, error) {
	p = p.Normalize()
	policy, err := validatePolicy(raw)
	if err != nil {
		return Policy{}, err
	}
	if err := ensureDir(p); err != nil {
		return Policy{}, err
	}
	if _, _, err := runtimesource.Get(p, policy.SourceID); err != nil {
		return Policy{}, fmt.Errorf("runtime update source does not resolve: %w", err)
	}
	return policy, atomicJSON(p.RuntimeUpdatePolicy, policy)
}

func loadState(p paths.Paths) (State, error) {
	p = p.Normalize()
	data, err := os.ReadFile(p.RuntimeUpdateState)
	if os.IsNotExist(err) {
		return State{SchemaVersion: StateSchemaVersion}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.SchemaVersion != StateSchemaVersion {
		return State{}, errors.New("unsupported runtime update state schema")
	}
	return state, nil
}

func saveState(p paths.Paths, state State) error {
	p = p.Normalize()
	state.SchemaVersion = StateSchemaVersion
	state.Generation++
	state.UpdatedUnixMS = time.Now().UnixMilli()
	return atomicJSON(p.RuntimeUpdateState, state)
}

func withLock(p paths.Paths, fn func() error) error {
	p = p.Normalize()
	if err := ensureDir(p); err != nil {
		return err
	}
	f, err := os.OpenFile(p.RuntimeUpdateLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

var urlToken = regexp.MustCompile(`https?://[^\s"']+`)

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	value := urlToken.ReplaceAllStringFunc(err.Error(), func(raw string) string {
		u, parseErr := url.Parse(strings.TrimRight(raw, ".,;:)"))
		if parseErr != nil {
			return "[redacted-url]"
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	})
	value = strings.TrimSpace(value)
	if len(value) > 2048 {
		value = value[:2048]
	}
	return value
}

func retryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var nerr net.Error
	if errors.As(err, &nerr) {
		return true
	}
	v := strings.ToLower(err.Error())
	return strings.Contains(v, "timeout") || strings.Contains(v, "temporar") || strings.Contains(v, "connection") || strings.Contains(v, "http 429") || strings.Contains(v, "http 5") || strings.Contains(v, "offline")
}

func SnapshotOf(p paths.Paths) (Snapshot, error) {
	p = p.Normalize()
	policy, err := LoadPolicy(p)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := loadState(p)
	if err != nil {
		return Snapshot{}, err
	}
	activation, err := runtimeactivation.StatusOf(p)
	if err != nil && !os.IsNotExist(err) {
		return Snapshot{}, err
	}
	out := Snapshot{Policy: policy, State: state, Activation: activation, TransitionInProgress: activation.TransitionInProgress}
	if activation.Present {
		s := activation.State
		out.CurrentRuntimeID = s.ActiveRuntimeID
		out.CurrentBinarySHA256 = s.ActiveBinarySHA256
		out.StagedRuntimeID = s.StagedRuntimeID
		out.StagedBinarySHA256 = s.StagedBinarySHA256
		out.PreviousRuntimeID = s.PreviousRuntimeID
		out.PreviousBinarySHA256 = s.PreviousBinarySHA256
		if s.StagedRuntimeID != "" {
			if _, inspectErr := runtimestore.Inspect(p, s.StagedRuntimeID); inspectErr != nil {
				out.StagedMissing = true
			}
		}
	}
	return out, nil
}

func resolutionMatchesRuntime(p paths.Paths, runtimeID string, resolution runtimesource.Resolution) bool {
	if runtimeID == "" || resolution.ResolutionID == "" {
		return false
	}
	m, err := runtimestore.Inspect(p, runtimeID)
	if err != nil {
		return false
	}
	if m.Source.ResolutionID == resolution.ResolutionID {
		return true
	}
	return resolution.Kind == runtimesource.KindGitHub && m.Source.Type == runtimestore.SourceBuild &&
		strings.EqualFold(m.Source.Repository, resolution.Repository) && strings.EqualFold(m.Source.ResolvedRef, resolution.CommitSHA)
}

func protectedRuntimeIDs(p paths.Paths) []string {
	state, ok, err := runtimestate.Load(p.Normalize())
	if err != nil || !ok {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, id := range []string{state.ActiveRuntimeID, state.PreviousRuntimeID, state.StagedRuntimeID, state.CandidateRuntimeID} {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func GarbageCollect(p paths.Paths) (runtimestore.GCResult, error) {
	policy, err := LoadPolicy(p)
	if err != nil {
		return runtimestore.GCResult{}, err
	}
	result, err := runtimestore.GarbageCollect(p, protectedRuntimeIDs(p), policy.RetainHistory)
	if err == nil {
		state, _ := loadState(p)
		state.LastGCUnixMS = time.Now().UnixMilli()
		_ = saveState(p, state)
	}
	return result, err
}

func recordFailure(p paths.Paths, state *State, label string, err error) error {
	state.LastResult = label
	state.LastError = sanitizeError(err)
	state.Retryable = retryableError(err)
	return saveState(p, *state)
}

func Check(ctx context.Context, p paths.Paths, sourceOverride string) (Snapshot, error) {
	p = p.Normalize()
	var snapshot Snapshot
	var opErr error
	err := withLock(p, func() error {
		policy, err := LoadPolicy(p)
		if err != nil {
			return err
		}
		if strings.TrimSpace(sourceOverride) != "" {
			policy.SourceID = strings.TrimSpace(sourceOverride)
			if _, _, err := runtimesource.Get(p, policy.SourceID); err != nil {
				return err
			}
		}
		state, err := loadState(p)
		if err != nil {
			return err
		}
		state.LastCheckUnixMS = time.Now().UnixMilli()
		state.LastError = ""
		state.Retryable = false
		resolution, err := runtimesource.Resolve(ctx, p, nil, policy.SourceID, runtimesource.ResolveRequest{})
		if err != nil {
			opErr = err
			return recordFailure(p, &state, "check-retryable", err)
		}
		state.LastResolutionID = resolution.ResolutionID
		activation, _ := runtimeactivation.StatusOf(p)
		if activation.Present && resolutionMatchesRuntime(p, activation.State.ActiveRuntimeID, resolution) {
			state.LastResult = "current"
			state.LastSuccessUnixMS = time.Now().UnixMilli()
			state.CandidateRuntimeID = activation.State.ActiveRuntimeID
			state.CandidateBinarySHA256 = activation.State.ActiveBinarySHA256
			return saveState(p, state)
		}
		if activation.Present && resolutionMatchesRuntime(p, activation.State.StagedRuntimeID, resolution) {
			state.LastResult = "staged"
			state.LastSuccessUnixMS = time.Now().UnixMilli()
			state.CandidateRuntimeID = activation.State.StagedRuntimeID
			state.CandidateBinarySHA256 = activation.State.StagedBinarySHA256
			return saveState(p, state)
		}
		if !policy.AcquireAutomatically {
			state.LastResult = "available"
			state.LastSuccessUnixMS = time.Now().UnixMilli()
			return saveState(p, state)
		}
		manifest, effectiveResolution, acquireErr := runtimeacquire.AcquireResolution(ctx, p, resolution.ResolutionID)
		if effectiveResolution.ResolutionID != "" {
			state.LastResolutionID = effectiveResolution.ResolutionID
		}
		state.CandidateRuntimeID = manifest.RuntimeID
		state.CandidateBinarySHA256 = manifest.BinarySHA256
		if acquireErr != nil {
			opErr = acquireErr
			label := "candidate-failed"
			if retryableError(acquireErr) {
				label = "acquire-retryable"
			}
			return recordFailure(p, &state, label, acquireErr)
		}
		if !policy.QualifyAutomatically {
			state.LastResult = "acquired"
			state.LastSuccessUnixMS = time.Now().UnixMilli()
			state.LastError = ""
			state.Retryable = false
			if err := saveState(p, state); err != nil {
				return err
			}
			_, _ = GarbageCollect(p)
			return nil
		}
		manifest, qualifyErr := runtimestore.Test(ctx, p, manifest.RuntimeID)
		state.CandidateRuntimeID = manifest.RuntimeID
		state.CandidateBinarySHA256 = manifest.BinarySHA256
		if qualifyErr != nil || !manifest.Qualification.Qualified {
			if qualifyErr == nil {
				qualifyErr = errors.New("candidate is not qualified")
			}
			opErr = qualifyErr
			return recordFailure(p, &state, "candidate-failed", qualifyErr)
		}
		if policy.StageAutomatically {
			if _, err := runtimeactivation.Stage(ctx, p, manifest.RuntimeID); err != nil {
				opErr = err
				return recordFailure(p, &state, "stage-failed", err)
			}
			state.LastResult = "staged"
		} else {
			state.LastResult = "qualified"
		}
		state.LastSuccessUnixMS = time.Now().UnixMilli()
		state.LastError = ""
		state.Retryable = false
		if err := saveState(p, state); err != nil {
			return err
		}
		if policy.ActivationMode == ActivationImmediate && policy.RestartActiveMountsAutomatically && policy.StageAutomatically {
			if _, err := runtimeactivation.ActivateStaged(ctx, p, nil); err != nil {
				opErr = err
				state, _ = loadState(p)
				return recordFailure(p, &state, "activation-failed", err)
			}
			state, _ = loadState(p)
			state.LastResult = "active"
			state.LastSuccessUnixMS = time.Now().UnixMilli()
			if err := saveState(p, state); err != nil {
				return err
			}
		}
		_, _ = GarbageCollect(p)
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err = SnapshotOf(p)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, opErr
}

func Activate(ctx context.Context, p paths.Paths, progress runtimeactivation.Progress) (runtimeactivation.Result, error) {
	result, err := runtimeactivation.ActivateStaged(ctx, p, progress)
	state, _ := loadState(p)
	if err != nil {
		_ = recordFailure(p, &state, "activation-failed", err)
		return result, err
	}
	state.LastResult = "active"
	state.LastError = ""
	state.Retryable = false
	state.LastSuccessUnixMS = time.Now().UnixMilli()
	_ = saveState(p, state)
	_, _ = GarbageCollect(p)
	return result, nil
}

func Rollback(ctx context.Context, p paths.Paths, progress runtimeactivation.Progress) (runtimeactivation.Result, error) {
	result, err := runtimeactivation.Rollback(ctx, p, progress)
	state, _ := loadState(p)
	if err != nil {
		_ = recordFailure(p, &state, "rollback-failed", err)
		return result, err
	}
	state.LastResult = "rolled-back"
	state.LastError = ""
	state.Retryable = false
	state.LastSuccessUnixMS = time.Now().UnixMilli()
	_ = saveState(p, state)
	_, _ = GarbageCollect(p)
	return result, nil
}

func BootActivate(ctx context.Context, p paths.Paths, progress runtimeactivation.Progress) (runtimeactivation.Result, bool, error) {
	policy, err := LoadPolicy(p)
	if err != nil {
		return runtimeactivation.Result{}, false, err
	}
	if policy.ActivationMode != ActivationNextReboot {
		return runtimeactivation.Result{}, false, nil
	}
	snap, err := SnapshotOf(p)
	if err != nil {
		return runtimeactivation.Result{}, false, err
	}
	if snap.StagedRuntimeID == "" {
		return runtimeactivation.Result{}, false, nil
	}
	if snap.StagedMissing {
		_ = runtimeactivation.ClearStaged(p, snap.StagedRuntimeID)
		state, _ := loadState(p)
		err := errors.New("staged runtime disappeared before boot activation")
		_ = recordFailure(p, &state, "staged-missing", err)
		return runtimeactivation.Result{}, false, err
	}
	result, err := Activate(ctx, p, progress)
	return result, true, err
}
