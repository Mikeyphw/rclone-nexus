package runtimestate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
)

const SchemaVersion = 1

type Phase string

const (
	PhaseActive              Phase = "ACTIVE"
	PhaseStaged              Phase = "STAGED"
	PhaseQuiescing           Phase = "QUIESCING"
	PhaseActivePendingVerify Phase = "ACTIVE_PENDING_VERIFY"
	PhaseRollback            Phase = "ROLLBACK"
	PhaseRecovered           Phase = "RECOVERED"
	PhaseDegradedRecovered   Phase = "DEGRADED_RECOVERED"
)

type State struct {
	SchemaVersion         int      `json:"schema_version"`
	Generation            uint64   `json:"generation"`
	Phase                 Phase    `json:"phase"`
	TransactionID         string   `json:"transaction_id,omitempty"`
	ActiveRuntimeID       string   `json:"active_runtime_id,omitempty"`
	ActiveBinarySHA256    string   `json:"active_binary_sha256,omitempty"`
	PreviousRuntimeID     string   `json:"previous_runtime_id,omitempty"`
	PreviousBinarySHA256  string   `json:"previous_binary_sha256,omitempty"`
	CandidateRuntimeID    string   `json:"candidate_runtime_id,omitempty"`
	CandidateBinarySHA256 string   `json:"candidate_binary_sha256,omitempty"`
	StagedRuntimeID       string   `json:"staged_runtime_id,omitempty"`
	StagedBinarySHA256    string   `json:"staged_binary_sha256,omitempty"`
	DesiredMounts         []string `json:"desired_mounts,omitempty"`
	QuiescedMounts        []string `json:"quiesced_mounts,omitempty"`
	LastError             string   `json:"last_error,omitempty"`
	Recovery              string   `json:"recovery,omitempty"`
	StartedUnixMS         int64    `json:"started_unix_ms,omitempty"`
	UpdatedUnixMS         int64    `json:"updated_unix_ms"`
}

func validRuntimeID(id string) bool {
	if id == "" {
		return true
	}
	if len(id) > 96 || strings.Contains(id, "..") || strings.ContainsAny(id, "/\\\x00\r\n") {
		return false
	}
	return filepath.Base(id) == id
}

func validDigest(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func Stable(phase Phase) bool {
	return phase == PhaseActive || phase == PhaseRecovered || phase == PhaseDegradedRecovered
}

func TransitionInProgressState(state State) bool {
	switch state.Phase {
	case PhaseStaged, PhaseQuiescing, PhaseActivePendingVerify, PhaseRollback:
		return true
	default:
		return false
	}
}

func validate(state State) error {
	if state.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported runtime activation state schema %d", state.SchemaVersion)
	}
	switch state.Phase {
	case PhaseActive, PhaseStaged, PhaseQuiescing, PhaseActivePendingVerify, PhaseRollback, PhaseRecovered, PhaseDegradedRecovered:
	default:
		return fmt.Errorf("invalid runtime activation phase %q", state.Phase)
	}
	for name, id := range map[string]string{
		"active":    state.ActiveRuntimeID,
		"previous":  state.PreviousRuntimeID,
		"candidate": state.CandidateRuntimeID,
		"staged":    state.StagedRuntimeID,
	} {
		if !validRuntimeID(id) {
			return fmt.Errorf("invalid %s runtime id", name)
		}
	}
	for name, digest := range map[string]string{
		"active":    state.ActiveBinarySHA256,
		"previous":  state.PreviousBinarySHA256,
		"candidate": state.CandidateBinarySHA256,
		"staged":    state.StagedBinarySHA256,
	} {
		if !validDigest(digest) {
			return fmt.Errorf("invalid %s runtime digest", name)
		}
	}
	if state.ActiveRuntimeID == "" && state.ActiveBinarySHA256 != "" {
		return errors.New("active runtime digest without active runtime id")
	}
	if state.ActiveRuntimeID != "" && state.ActiveBinarySHA256 == "" {
		return errors.New("active runtime id without binary digest")
	}
	return nil
}

func Load(p paths.Paths) (State, bool, error) {
	p = p.Normalize()
	info, err := os.Lstat(p.RuntimeActivationState)
	if os.IsNotExist(err) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return State{}, false, errors.New("runtime activation state is not a regular file")
	}
	data, err := os.ReadFile(p.RuntimeActivationState)
	if err != nil {
		return State{}, false, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, false, err
	}
	if err := validate(state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

func Save(p paths.Paths, state State) error {
	p = p.Normalize()
	if err := os.MkdirAll(filepath.Dir(p.RuntimeActivationState), 0o700); err != nil {
		return err
	}
	state.SchemaVersion = SchemaVersion
	state.UpdatedUnixMS = time.Now().UnixMilli()
	if err := validate(state); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	dir := filepath.Dir(p.RuntimeActivationState)
	tmp, err := os.CreateTemp(dir, ".activation-*")
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
	if err := os.Rename(name, p.RuntimeActivationState); err != nil {
		return err
	}
	if err := os.Chmod(p.RuntimeActivationState, 0o600); err != nil {
		return err
	}
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func TransitionInProgress(p paths.Paths) (bool, error) {
	state, ok, err := Load(p)
	if err != nil || !ok {
		return false, err
	}
	return TransitionInProgressState(state), nil
}

func RuntimeBinaryPath(p paths.Paths, id string) (string, error) {
	if !validRuntimeID(id) || id == "" {
		return "", errors.New("invalid runtime id")
	}
	return filepath.Join(p.Normalize().RuntimeStoreDir, id, "rclone"), nil
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func VerifyRuntimeBytes(p paths.Paths, id, expected string) (string, error) {
	binary, err := RuntimeBinaryPath(p, id)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(binary)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("runtime binary is not an executable regular file")
	}
	digest, err := HashFile(binary)
	if err != nil {
		return "", err
	}
	if expected != "" && digest != expected {
		return "", errors.New("runtime binary hash does not match activation state")
	}
	return binary, nil
}
