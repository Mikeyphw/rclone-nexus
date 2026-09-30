package mounts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"rclone-nexus/internal/paths"
)

const (
	DesiredRunning = "running"
	DesiredStopped = "stopped"
)

type desiredRecord struct {
	SchemaVersion int    `json:"schema_version"`
	State         string `json:"state"`
}

func desiredPath(p paths.Paths, name string) string {
	p = p.Normalize()
	return filepath.Join(p.DesiredDir, name+".json")
}

func desiredOverride(p paths.Paths, name string) (string, bool) {
	data, err := os.ReadFile(desiredPath(p, name))
	if err != nil {
		return "", false
	}
	var record desiredRecord
	if json.Unmarshal(data, &record) != nil || record.SchemaVersion != 1 {
		return "", false
	}
	if record.State != DesiredRunning && record.State != DesiredStopped {
		return "", false
	}
	return record.State, true
}

func DesiredState(p paths.Paths, cfg Config) string {
	if state, ok := desiredOverride(p, cfg.Name); ok {
		return state
	}
	if cfg.Enabled {
		return DesiredRunning
	}
	return DesiredStopped
}

func setDesiredState(p paths.Paths, name, state string) error {
	p = p.Normalize()
	if !ValidName(name) {
		return fmt.Errorf("invalid mount name: %s", name)
	}
	if state != DesiredRunning && state != DesiredStopped {
		return fmt.Errorf("invalid desired state %q", state)
	}
	if err := os.MkdirAll(p.DesiredDir, 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(desiredRecord{SchemaVersion: 1, State: state})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return atomicWritePrivate(desiredPath(p, name), payload)
}

func removeDesiredState(p paths.Paths, name string) {
	_ = os.Remove(desiredPath(p, name))
}
