package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
	"rclone-nexus/internal/redact"
)

const (
	StateRunning     = "RUNNING"
	StateSucceeded   = "SUCCEEDED"
	StateFailed      = "FAILED"
	StateCancelled   = "CANCELLED"
	StateInterrupted = "INTERRUPTED"
	maxEvents        = 128
)

type Event struct {
	Sequence int    `json:"sequence"`
	UnixMS   int64  `json:"unix_ms"`
	Event    string `json:"event"`
	Message  string `json:"message,omitempty"`
	Data     any    `json:"data,omitempty"`
}

type Record struct {
	SchemaVersion int                    `json:"schema_version"`
	RequestID     string                 `json:"request_id"`
	Operation     string                 `json:"operation"`
	Class         string                 `json:"class"`
	Cancellable   bool                   `json:"cancellable"`
	State         string                 `json:"state"`
	OwnerPID      int                    `json:"owner_pid"`
	OwnerStart    uint64                 `json:"owner_start_ticks"`
	StartedUnixMS int64                  `json:"started_unix_ms"`
	UpdatedUnixMS int64                  `json:"updated_unix_ms"`
	CompletedMS   int64                  `json:"completed_unix_ms,omitempty"`
	Events        []Event                `json:"events,omitempty"`
	Result        any                    `json:"result,omitempty"`
	Error         *protocol.MachineError `json:"error,omitempty"`
}

var mu sync.Mutex

func recordPath(p paths.Paths, requestID string) (string, error) {
	if requestID == "" || len(requestID) > protocol.MaxRequestIDLength {
		return "", fmt.Errorf("invalid request id")
	}
	for _, r := range requestID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-", r) {
			continue
		}
		return "", fmt.Errorf("invalid request id")
	}
	p = p.Normalize()
	return filepath.Join(p.OperationsDir, requestID+".json"), nil
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
	if len(fields) <= 19 {
		return 0, fmt.Errorf("short /proc stat")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

func ownerAlive(r Record) bool {
	if r.OwnerPID <= 0 || r.OwnerStart == 0 {
		return false
	}
	if err := syscall.Kill(r.OwnerPID, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	ticks, err := processStartTicks(r.OwnerPID)
	return err == nil && ticks == r.OwnerStart
}

func atomicWrite(path string, value Record) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".operation-*")
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
	dirFile, err := os.Open(dir)
	if err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func read(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, err
	}
	if r.SchemaVersion != 1 || r.RequestID == "" {
		return Record{}, fmt.Errorf("invalid operation record")
	}
	return r, nil
}

func Begin(p paths.Paths, requestID, operation, class string, cancellable bool) (Record, error) {
	mu.Lock()
	defer mu.Unlock()
	if err := p.Normalize().EnsureState(); err != nil {
		return Record{}, err
	}
	path, err := recordPath(p, requestID)
	if err != nil {
		return Record{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return Record{}, fmt.Errorf("operation journal already contains request_id %s", requestID)
	}
	ticks, err := processStartTicks(os.Getpid())
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UnixMilli()
	r := Record{SchemaVersion: 1, RequestID: requestID, Operation: operation, Class: class, Cancellable: cancellable, State: StateRunning, OwnerPID: os.Getpid(), OwnerStart: ticks, StartedUnixMS: now, UpdatedUnixMS: now}
	if err := atomicWrite(path, r); err != nil {
		return Record{}, err
	}
	return r, nil
}

func boundedValue(value any, maxBytes int) any {
	sanitized := redact.Value(value, protocol.MaxStringBytes)
	payload, err := json.Marshal(sanitized)
	if err != nil || len(payload) > maxBytes {
		return map[string]any{"truncated": true}
	}
	return sanitized
}
func Append(p paths.Paths, requestID, event, message string, data any) error {
	mu.Lock()
	defer mu.Unlock()
	path, err := recordPath(p, requestID)
	if err != nil {
		return err
	}
	r, err := read(path)
	if err != nil {
		return err
	}
	if r.State != StateRunning {
		return nil
	}
	seq := 1
	if len(r.Events) > 0 {
		seq = r.Events[len(r.Events)-1].Sequence + 1
	}
	r.Events = append(r.Events, Event{Sequence: seq, UnixMS: time.Now().UnixMilli(), Event: redact.BoundedString(event, 128), Message: redact.BoundedString(message, protocol.MaxStringBytes), Data: redact.Value(data, protocol.MaxStringBytes)})
	if len(r.Events) > maxEvents {
		r.Events = append([]Event(nil), r.Events[len(r.Events)-maxEvents:]...)
	}
	r.UpdatedUnixMS = time.Now().UnixMilli()
	return atomicWrite(path, r)
}

func Complete(p paths.Paths, requestID, state string, result any, machineError *protocol.MachineError) error {
	mu.Lock()
	defer mu.Unlock()
	path, err := recordPath(p, requestID)
	if err != nil {
		return err
	}
	r, err := read(path)
	if err != nil {
		return err
	}
	switch state {
	case StateSucceeded, StateFailed, StateCancelled, StateInterrupted:
	default:
		return fmt.Errorf("invalid terminal state %q", state)
	}
	now := time.Now().UnixMilli()
	r.State, r.UpdatedUnixMS, r.CompletedMS = state, now, now
	r.Result = boundedValue(result, protocol.MaxResponseBytes/2)
	if machineError != nil {
		copy := *machineError
		copy.Message = redact.BoundedString(scrubPrivatePaths(p, copy.Message), protocol.MaxStringBytes)
		copy.Detail = redact.BoundedString(scrubPrivatePaths(p, copy.Detail), protocol.MaxStringBytes)
		r.Error = &copy
	}
	return atomicWrite(path, r)
}

func scrubPrivatePaths(p paths.Paths, value string) string {
	p = p.Normalize()
	for _, private := range []string{p.RcloneConfig, p.ProviderModuleDir, p.ModuleDir, p.StateDir} {
		if private != "" {
			value = strings.ReplaceAll(value, private, "<private-path>")
		}
	}
	return value
}

func recoverOne(path string, r Record) (Record, error) {
	if r.State != StateRunning || ownerAlive(r) {
		return r, nil
	}
	now := time.Now().UnixMilli()
	r.State, r.UpdatedUnixMS, r.CompletedMS = StateInterrupted, now, now
	r.Error = protocol.Error("operation_interrupted", "operation owner exited before terminal state was recorded", "")
	if err := atomicWrite(path, r); err != nil {
		return Record{}, err
	}
	return r, nil
}

func Get(p paths.Paths, requestID string) (Record, error) {
	mu.Lock()
	defer mu.Unlock()
	path, err := recordPath(p, requestID)
	if err != nil {
		return Record{}, err
	}
	r, err := read(path)
	if err != nil {
		return Record{}, err
	}
	return recoverOne(path, r)
}

func List(p paths.Paths, limit int) ([]Record, error) {
	mu.Lock()
	defer mu.Unlock()
	p = p.Normalize()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	entries, err := os.ReadDir(p.OperationsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil
		}
		return nil, err
	}
	out := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(p.OperationsDir, entry.Name())
		r, err := read(path)
		if err != nil {
			continue
		}
		r, err = recoverOne(path, r)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedUnixMS > out[j].UpdatedUnixMS })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func RecoverOrphans(p paths.Paths) error {
	_, err := List(p, 200)
	return err
}
