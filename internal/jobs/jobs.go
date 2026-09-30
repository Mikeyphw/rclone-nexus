package jobs

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/policy"
	"rclone-nexus/internal/provider"
)

const (
	TypeSync  = "sync"
	TypeCopy  = "copy"
	TypeCheck = "check"
)

type Config struct {
	Name               string `json:"name"`
	Enabled            bool   `json:"enabled"`
	Type               string `json:"type"`
	Source             string `json:"source"`
	Destination        string `json:"destination"`
	Every              string `json:"every"`
	NetworkMode        string `json:"network_mode,omitempty"`
	ChargingOnly       bool   `json:"charging_only,omitempty"`
	MinBattery         int    `json:"min_battery,omitempty"`
	ConfirmDestructive bool   `json:"confirm_destructive,omitempty"`
}

type Registry struct {
	SchemaVersion int      `json:"schema_version"`
	Revision      uint64   `json:"revision"`
	Digest        string   `json:"digest"`
	Jobs          []Config `json:"jobs"`
}

type Preview struct {
	CurrentRevision uint64   `json:"current_revision"`
	CandidateDigest string   `json:"candidate_digest"`
	Jobs            []Config `json:"jobs"`
	Destructive     []string `json:"destructive_sync_jobs,omitempty"`
}

type State struct {
	SchemaVersion      int    `json:"schema_version"`
	Name               string `json:"name"`
	NextRunUnixMS      int64  `json:"next_run_unix_ms"`
	LastStartedUnixMS  int64  `json:"last_started_unix_ms,omitempty"`
	LastFinishedUnixMS int64  `json:"last_finished_unix_ms,omitempty"`
	LastState          string `json:"last_state,omitempty"`
	LastError          string `json:"last_error,omitempty"`
	LastRequestID      string `json:"last_request_id,omitempty"`
	RunCount           uint64 `json:"run_count"`
}

type RunResult struct {
	Name       string  `json:"name"`
	Trigger    string  `json:"trigger"`
	State      string  `json:"state"`
	ExitCode   int     `json:"exit_code"`
	DurationMS int64   `json:"duration_ms"`
	Bytes      int64   `json:"bytes,omitempty"`
	Speed      float64 `json:"speed_bytes_per_sec,omitempty"`
	ETASeconds float64 `json:"eta_seconds,omitempty"`
}

type Progress func(event, message string, data any)

var stateMu sync.Mutex

func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}
func validateEndpoint(v string) error {
	if v == "" || strings.ContainsAny(v, "\x00\r\n") || strings.HasPrefix(v, "-") {
		return fmt.Errorf("invalid job endpoint")
	}
	if strings.Contains(v, ":") {
		name := strings.SplitN(v, ":", 2)[0]
		if !validName(name) {
			return fmt.Errorf("invalid remote name")
		}
		return nil
	}
	if !filepath.IsAbs(v) {
		return fmt.Errorf("local job endpoint must be absolute")
	}
	return nil
}
func interval(c Config) (time.Duration, error) {
	d, err := time.ParseDuration(c.Every)
	if err != nil || d < time.Minute || d > 365*24*time.Hour {
		return 0, fmt.Errorf("invalid job interval")
	}
	return d, nil
}
func normalize(c Config) Config {
	c.Name = strings.TrimSpace(c.Name)
	c.Type = strings.ToLower(strings.TrimSpace(c.Type))
	c.Source = strings.TrimSpace(c.Source)
	c.Destination = strings.TrimSpace(c.Destination)
	c.Every = strings.TrimSpace(c.Every)
	c.NetworkMode = strings.ToLower(strings.TrimSpace(c.NetworkMode))
	if c.NetworkMode == "" {
		c.NetworkMode = policy.NetworkAny
	}
	return c
}
func validate(c Config, requireApproval bool) error {
	c = normalize(c)
	if !validName(c.Name) {
		return fmt.Errorf("invalid job name")
	}
	switch c.Type {
	case TypeSync, TypeCopy, TypeCheck:
	default:
		return fmt.Errorf("unsupported job type")
	}
	if err := validateEndpoint(c.Source); err != nil {
		return err
	}
	if err := validateEndpoint(c.Destination); err != nil {
		return err
	}
	if _, err := interval(c); err != nil {
		return err
	}
	if !policy.ValidNetworkMode(c.NetworkMode) {
		return fmt.Errorf("unsupported job network_mode")
	}
	if c.MinBattery < 0 || c.MinBattery > 100 {
		return fmt.Errorf("invalid job min_battery")
	}
	if requireApproval && c.Type == TypeSync && !c.ConfirmDestructive {
		return fmt.Errorf("sync job requires confirm_destructive=true")
	}
	return nil
}
func canonicalDigest(jobs []Config) string {
	copyJobs := append([]Config(nil), jobs...)
	sort.Slice(copyJobs, func(i, j int) bool { return copyJobs[i].Name < copyJobs[j].Name })
	b, _ := json.Marshal(copyJobs)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func emptyRegistry() Registry {
	return Registry{SchemaVersion: 1, Revision: 0, Digest: canonicalDigest(nil), Jobs: []Config{}}
}

func Load(p paths.Paths) (Registry, error) {
	p = p.Normalize()
	b, err := os.ReadFile(p.JobRegistry)
	if os.IsNotExist(err) {
		return emptyRegistry(), nil
	}
	if err != nil {
		return Registry{}, err
	}
	var r Registry
	if err = json.Unmarshal(b, &r); err != nil {
		return Registry{}, err
	}
	if r.SchemaVersion != 1 {
		return Registry{}, fmt.Errorf("unsupported job registry schema")
	}
	for i := range r.Jobs {
		r.Jobs[i] = normalize(r.Jobs[i])
		if err := validate(r.Jobs[i], true); err != nil {
			return Registry{}, fmt.Errorf("job %s: %w", r.Jobs[i].Name, err)
		}
	}
	if canonicalDigest(r.Jobs) != r.Digest {
		return Registry{}, fmt.Errorf("job registry digest mismatch")
	}
	return r, nil
}
func PreviewCandidate(p paths.Paths, candidate []Config) (Preview, error) {
	current, err := Load(p)
	if err != nil {
		return Preview{}, err
	}
	seen := map[string]bool{}
	out := make([]Config, 0, len(candidate))
	var destructive []string
	for _, c := range candidate {
		c = normalize(c)
		if seen[c.Name] {
			return Preview{}, fmt.Errorf("duplicate job name")
		}
		seen[c.Name] = true
		if err := validate(c, false); err != nil {
			return Preview{}, err
		}
		if c.Type == TypeSync {
			destructive = append(destructive, c.Name)
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return Preview{CurrentRevision: current.Revision, CandidateDigest: canonicalDigest(out), Jobs: out, Destructive: destructive}, nil
}
func writeRegistry(p paths.Paths, r Registry) error {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(p.JobRegistry), ".jobs-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	_ = tmp.Chmod(0o600)
	if _, err = tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, p.JobRegistry)
}
func withRegistryLock(p paths.Paths, fn func() error) error {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(p.JobLockDir, "registry.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func ApplyCandidate(p paths.Paths, expected uint64, digest string, candidate []Config) (Registry, error) {
	var result Registry
	err := withRegistryLock(p, func() error {
		preview, err := PreviewCandidate(p, candidate)
		if err != nil {
			return err
		}
		if preview.CurrentRevision != expected {
			return fmt.Errorf("stale job revision")
		}
		if preview.CandidateDigest != digest {
			return fmt.Errorf("job candidate digest mismatch")
		}
		for _, c := range preview.Jobs {
			if err := validate(c, true); err != nil {
				return err
			}
		}
		r := Registry{SchemaVersion: 1, Revision: expected + 1, Digest: digest, Jobs: preview.Jobs}
		if err := writeRegistry(p, r); err != nil {
			return err
		}
		now := time.Now()
		for _, j := range r.Jobs {
			if _, err := ReadState(p, j.Name); os.IsNotExist(err) {
				d, _ := interval(j)
				_ = WriteState(p, State{SchemaVersion: 1, Name: j.Name, NextRunUnixMS: now.Add(d).UnixMilli()})
			}
		}
		result = r
		return nil
	})
	return result, err
}
func Get(p paths.Paths, name string) (Config, error) {
	r, err := Load(p)
	if err != nil {
		return Config{}, err
	}
	for _, j := range r.Jobs {
		if j.Name == name {
			return j, nil
		}
	}
	return Config{}, fmt.Errorf("job not found")
}
func Snapshot(p paths.Paths) (Registry, error) { return Load(p) }

func statePath(p paths.Paths, name string) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("invalid job name")
	}
	return filepath.Join(p.Normalize().JobStateDir, name+".json"), nil
}
func ReadState(p paths.Paths, name string) (State, error) {
	path, err := statePath(p, name)
	if err != nil {
		return State{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var s State
	if err = json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}
	if s.SchemaVersion != 1 || s.Name != name {
		return State{}, fmt.Errorf("invalid job state")
	}
	return s, nil
}
func WriteState(p paths.Paths, s State) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return err
	}
	path, err := statePath(p, s.Name)
	if err != nil {
		return err
	}
	s.SchemaVersion = 1
	payload, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	n := tmp.Name()
	defer os.Remove(n)
	_ = tmp.Chmod(0o600)
	if _, err = tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(n, path)
}
func States(p paths.Paths) ([]State, error) {
	r, err := Load(p)
	if err != nil {
		return nil, err
	}
	out := make([]State, 0, len(r.Jobs))
	now := time.Now()
	for _, j := range r.Jobs {
		s, err := ReadState(p, j.Name)
		if os.IsNotExist(err) {
			d, _ := interval(j)
			s = State{SchemaVersion: 1, Name: j.Name, NextRunUnixMS: now.Add(d).UnixMilli()}
			_ = WriteState(p, s)
		} else if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func Due(p paths.Paths, now time.Time) ([]string, error) {
	r, err := Load(p)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, j := range r.Jobs {
		if !j.Enabled {
			continue
		}
		s, err := ReadState(p, j.Name)
		if os.IsNotExist(err) {
			d, _ := interval(j)
			_ = WriteState(p, State{SchemaVersion: 1, Name: j.Name, NextRunUnixMS: now.Add(d).UnixMilli()})
			continue
		}
		if err != nil {
			return nil, err
		}
		if s.NextRunUnixMS <= now.UnixMilli() {
			out = append(out, j.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func lockJob(p paths.Paths, name string) (*os.File, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(p.JobLockDir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("job already running")
	}
	return f, nil
}
func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }
func policyFor(ctx context.Context, p paths.Paths, c Config, record bool) (policy.Decision, error) {
	spec := policy.Spec{NetworkMode: c.NetworkMode, ChargingOnly: c.ChargingOnly, MinBattery: c.MinBattery}
	if record {
		return policy.EvaluateAndRecord(ctx, p, "job-"+c.Name, spec, false)
	}
	return policy.Evaluate(ctx, p, "job-"+c.Name, spec, false)
}
func PreviewRun(p paths.Paths, name string) (map[string]any, error) {
	c, err := Get(p, name)
	if err != nil {
		return nil, err
	}
	dec, err := policyFor(context.Background(), p, c, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": c.Name, "type": c.Type, "enabled": c.Enabled, "destructive": c.Type == TypeSync, "destructive_confirmed": c.ConfirmDestructive, "policy": dec}, nil
}

func parseProgress(line string) (map[string]any, bool) {
	var raw map[string]any
	if json.Unmarshal([]byte(line), &raw) != nil {
		return nil, false
	}
	out := map[string]any{}
	for _, k := range []string{"bytes", "speed", "eta", "errors", "checks", "transfers", "percentage"} {
		if v, ok := raw[k]; ok {
			out[k] = v
		}
	}
	if stats, ok := raw["stats"].(map[string]any); ok {
		for _, k := range []string{"bytes", "speed", "eta", "errors", "checks", "transfers"} {
			if v, ok := stats[k]; ok {
				out[k] = v
			}
		}
	}
	return out, len(out) > 0
}
func runStream(scanner *bufio.Scanner, log *os.File, mu *sync.Mutex, progress Progress, last *RunResult) {
	for scanner.Scan() {
		line := scanner.Text()
		mu.Lock()
		_, _ = fmt.Fprintln(log, line)
		mu.Unlock()
		if data, ok := parseProgress(line); ok {
			if v, ok := data["bytes"].(float64); ok {
				last.Bytes = int64(v)
			}
			if v, ok := data["speed"].(float64); ok {
				last.Speed = v
			}
			if v, ok := data["eta"].(float64); ok {
				last.ETASeconds = v
			}
			if progress != nil {
				progress("progress", "rclone job progress", data)
			}
		}
	}
}
func Run(ctx context.Context, p paths.Paths, name, trigger, requestID string, progress Progress) (RunResult, error) {
	if trigger != "manual" && trigger != "schedule" {
		return RunResult{}, fmt.Errorf("invalid job trigger")
	}
	c, err := Get(p, name)
	if err != nil {
		return RunResult{}, err
	}
	lock, err := lockJob(p, name)
	if err != nil {
		return RunResult{}, err
	}
	defer unlock(lock)
	decision, err := policyFor(ctx, p, c, true)
	if err != nil {
		return RunResult{}, err
	}
	if !decision.Allowed {
		return RunResult{}, fmt.Errorf("job policy blocked: %s", decision.Reason)
	}
	rclone, err := provider.FindRclone(p)
	if err != nil {
		return RunResult{}, err
	}
	p = p.Normalize()
	if info, err := os.Stat(p.RcloneConfig); err != nil || !info.Mode().IsRegular() {
		return RunResult{}, fmt.Errorf("rclone config not found")
	}
	d, _ := interval(c)
	s, err := ReadState(p, name)
	if os.IsNotExist(err) {
		s = State{SchemaVersion: 1, Name: name, NextRunUnixMS: time.Now().Add(d).UnixMilli()}
	} else if err != nil {
		return RunResult{}, err
	}
	now := time.Now()
	if trigger == "schedule" {
		s.NextRunUnixMS = now.Add(d).UnixMilli()
	}
	s.LastStartedUnixMS = now.UnixMilli()
	s.LastState = "RUNNING"
	s.LastError = ""
	s.LastRequestID = requestID
	if err := WriteState(p, s); err != nil {
		return RunResult{}, err
	}
	args := []string{c.Type, c.Source, c.Destination, "--config", p.RcloneConfig, "--use-json-log", "--stats", "1s", "--stats-one-line-json"}
	logPath := filepath.Join(p.LogDir, "job-"+name+".log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return RunResult{}, err
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, rclone, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{}, err
	}
	started := time.Now()
	if err = cmd.Start(); err != nil {
		return RunResult{}, err
	}
	result := RunResult{Name: name, Trigger: trigger, State: "RUNNING"}
	var wg sync.WaitGroup
	var logMu sync.Mutex
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 4096), 256<<10)
		runStream(sc, log, &logMu, progress, &result)
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 4096), 256<<10)
		runStream(sc, log, &logMu, progress, &result)
	}()
	waitErr := cmd.Wait()
	wg.Wait()
	result.DurationMS = time.Since(started).Milliseconds()
	if waitErr != nil {
		result.State = "FAILED"
		if ctx.Err() != nil {
			result.State = "CANCELLED"
			waitErr = ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			result.ExitCode = ee.ExitCode()
		} else {
			result.ExitCode = 1
		}
	} else {
		result.State = "SUCCEEDED"
	}
	s.LastFinishedUnixMS = time.Now().UnixMilli()
	s.LastState = result.State
	s.RunCount++
	if waitErr != nil {
		s.LastError = waitErr.Error()
	} else {
		s.LastError = ""
	}
	_ = WriteState(p, s)
	if waitErr != nil {
		return result, waitErr
	}
	return result, nil
}

func SchedulerInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("RNEXUS_JOB_SCHEDULER_INTERVAL")); v != "" {
		if d, e := time.ParseDuration(v); e == nil && d >= 100*time.Millisecond && d <= 10*time.Minute {
			return d
		}
	}
	return 30 * time.Second
}
