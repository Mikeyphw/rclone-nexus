package mounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
)

type ProcessRecord struct {
	SchemaVersion int    `json:"schema_version"`
	PID           int    `json:"pid"`
	StartTicks    uint64 `json:"start_ticks"`
	ConfigDigest  string `json:"config_digest"`
	StartedUnix   int64  `json:"started_unix"`
}

func pidPath(p paths.Paths, name string) string {
	p = p.Normalize()
	return filepath.Join(p.MountRunDir, name+".process.json")
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func processStartTicks(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	text := string(data)
	closeParen := strings.LastIndex(text, ")")
	if closeParen < 0 || closeParen+2 >= len(text) {
		return 0, fmt.Errorf("malformed /proc stat")
	}
	fields := strings.Fields(text[closeParen+2:])
	// fields[0] is stat field 3 (state); starttime is stat field 22.
	if len(fields) <= 19 {
		return 0, fmt.Errorf("short /proc stat")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, err
	}
	return ticks, nil
}

func newProcessRecord(pid int, cfg Config) (ProcessRecord, error) {
	ticks, err := processStartTicks(pid)
	if err != nil {
		return ProcessRecord{}, err
	}
	digest, err := digestConfigs([]Config{cfg})
	if err != nil {
		return ProcessRecord{}, err
	}
	return ProcessRecord{
		SchemaVersion: 1,
		PID:           pid,
		StartTicks:    ticks,
		ConfigDigest:  digest,
		StartedUnix:   time.Now().Unix(),
	}, nil
}

func writeProcessRecord(p paths.Paths, name string, record ProcessRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return atomicWritePrivate(pidPath(p, name), payload)
}

func readProcessRecord(p paths.Paths, name string) (ProcessRecord, error) {
	data, err := os.ReadFile(pidPath(p, name))
	if err != nil {
		return ProcessRecord{}, err
	}
	var record ProcessRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return ProcessRecord{}, fmt.Errorf("invalid process identity record: %w", err)
	}
	if record.SchemaVersion != 1 || record.PID <= 0 || record.StartTicks == 0 {
		return ProcessRecord{}, fmt.Errorf("invalid process identity record")
	}
	return record, nil
}

func validateProcessRecord(record ProcessRecord) error {
	if !alive(record.PID) {
		return fmt.Errorf("process is not alive")
	}
	ticks, err := processStartTicks(record.PID)
	if err != nil {
		return err
	}
	if ticks != record.StartTicks {
		return fmt.Errorf("process identity mismatch")
	}
	return nil
}

func removeProcessRecord(p paths.Paths, name string) {
	_ = os.Remove(pidPath(p, name))
}

func atomicWritePrivate(path string, payload []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".tmp-*")
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
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDir(dir)
}
