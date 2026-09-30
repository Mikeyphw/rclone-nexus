package namespace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"rclone-nexus/internal/paths"
)

const DesiredAppVisible = "app-visible"

type OwnedTarget struct {
	NamespaceID   string         `json:"namespace_id"`
	UserIDs       []int          `json:"user_ids,omitempty"`
	Classes       []string       `json:"classes,omitempty"`
	Signature     MountSignature `json:"signature"`
	AppliedUnixMS int64          `json:"applied_unix_ms"`
}

type State struct {
	SchemaVersion   int            `json:"schema_version"`
	Name            string         `json:"name"`
	Desired         string         `json:"desired"`
	Mountpoint      string         `json:"mountpoint"`
	SourceSignature MountSignature `json:"source_signature"`
	Targets         []OwnedTarget  `json:"targets,omitempty"`
	LastClaim       string         `json:"last_claim,omitempty"`
	UpdatedUnixMS   int64          `json:"updated_unix_ms"`
}

func statePath(p paths.Paths, name string) string {
	return filepath.Join(p.Normalize().NamespaceDir, name+".json")
}

func LoadState(p paths.Paths, name string) (State, error) {
	data, err := os.ReadFile(statePath(p, name))
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("invalid namespace state: %w", err)
	}
	if state.SchemaVersion != SchemaVersion || state.Name != name || state.Mountpoint == "" {
		return State{}, fmt.Errorf("invalid namespace state")
	}
	return state, nil
}

func writeState(p paths.Paths, state State) error {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return err
	}
	state.SchemaVersion = SchemaVersion
	state.UpdatedUnixMS = time.Now().UnixMilli()
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	dir := p.NamespaceDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".namespace-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, statePath(p, state.Name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err == nil {
		defer d.Close()
		_ = d.Sync()
	}
	return nil
}

func Desired(p paths.Paths, name string) bool {
	state, err := LoadState(p, name)
	return err == nil && state.Desired == DesiredAppVisible
}
func Forget(p paths.Paths, name string) error {
	err := os.Remove(statePath(p, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
