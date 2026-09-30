package platformstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"rclone-nexus/internal/paths"
)

const CurrentSchema = 1

type State struct {
	SchemaVersion int   `json:"schema_version"`
	UpdatedUnixMS int64 `json:"updated_unix_ms"`
}

type Result struct {
	From         int  `json:"from"`
	To           int  `json:"to"`
	Migrated     bool `json:"migrated"`
	Bootstrapped bool `json:"bootstrapped"`
}

func statePath(p paths.Paths) string { return filepath.Join(p.Normalize().PlatformDir, "state.json") }
func previousPath(p paths.Paths) string {
	return filepath.Join(p.Normalize().PlatformDir, "state.previous.json")
}

func Read(p paths.Paths) (State, error) {
	data, err := os.ReadFile(statePath(p))
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("invalid platform state: %w", err)
	}
	if s.SchemaVersion < 0 {
		return State{}, errors.New("invalid platform schema")
	}
	return s, nil
}

func ValidateUpgrade(p paths.Paths) error {
	s, err := Read(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.SchemaVersion > CurrentSchema {
		return fmt.Errorf("state schema %d is newer than supported schema %d", s.SchemaVersion, CurrentSchema)
	}
	return nil
}

func Migrate(p paths.Paths) (Result, error) { return migrateWithHook(p, nil) }

func migrateWithHook(p paths.Paths, afterBackup func() error) (Result, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return Result{}, err
	}
	s, err := Read(p)
	if os.IsNotExist(err) {
		if err := writeAtomic(statePath(p), State{SchemaVersion: CurrentSchema, UpdatedUnixMS: time.Now().UnixMilli()}); err != nil {
			return Result{}, err
		}
		return Result{From: 0, To: CurrentSchema, Bootstrapped: true}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if s.SchemaVersion > CurrentSchema {
		return Result{}, fmt.Errorf("state schema %d is newer than supported schema %d", s.SchemaVersion, CurrentSchema)
	}
	if s.SchemaVersion == CurrentSchema {
		return Result{From: CurrentSchema, To: CurrentSchema}, nil
	}

	original, err := os.ReadFile(statePath(p))
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(previousPath(p), original, 0o600); err != nil {
		return Result{}, err
	}
	if afterBackup != nil {
		if err := afterBackup(); err != nil {
			_ = os.WriteFile(statePath(p), original, 0o600)
			return Result{}, err
		}
	}
	next := State{SchemaVersion: CurrentSchema, UpdatedUnixMS: time.Now().UnixMilli()}
	if err := writeAtomic(statePath(p), next); err != nil {
		_ = os.WriteFile(statePath(p), original, 0o600)
		return Result{}, err
	}
	return Result{From: s.SchemaVersion, To: CurrentSchema, Migrated: true}, nil
}

func writeAtomic(path string, value State) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".platform-state-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
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
	if err := os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
