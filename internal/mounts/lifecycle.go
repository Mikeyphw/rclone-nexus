package mounts

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/rc"
)

type Status struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Enabled    bool   `json:"enabled,omitempty"`
	Desired    string `json:"desired,omitempty"`
	State      string `json:"state"`
	PID        int    `json:"pid,omitempty"`
	Managed    bool   `json:"managed,omitempty"`
}

type ActionResult struct {
	Name  string `json:"name"`
	State string `json:"state"`
	PID   int    `json:"pid,omitempty"`
	Noop  bool   `json:"noop,omitempty"`
}

type LifecycleFailure struct {
	Name  string `json:"name"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

type ReconcileReport struct {
	Changed  []ActionResult     `json:"changed"`
	Failures []LifecycleFailure `json:"failures,omitempty"`
}

type Preview struct {
	Name            string `json:"name"`
	Remote          string `json:"remote"`
	Mountpoint      string `json:"mountpoint"`
	VFSProfile      string `json:"vfs_profile"`
	VFSCacheMode    string `json:"vfs_cache_mode"`
	VFSCacheMaxSize string `json:"vfs_cache_max_size,omitempty"`
	VFSCacheMaxAge  string `json:"vfs_cache_max_age,omitempty"`
	DirCacheTime    string `json:"dir_cache_time,omitempty"`
	PollInterval    string `json:"poll_interval,omitempty"`
	AllowOther      bool   `json:"allow_other"`
	HasExtraArgs    bool   `json:"has_extra_args"`
	ProviderReady   bool   `json:"provider_ready"`
}

type lifecyclePlanItem struct {
	Name            string
	RestartRequired bool
	Old             *Config
	New             *Config
}

func StatusOne(p paths.Paths, name string) Status {
	p = p.Normalize()
	cfg, err := Parse(p, name)
	if err != nil {
		return Status{Name: name, Configured: false, State: "not-configured"}
	}
	status := Status{
		Name: name, Configured: true, Enabled: cfg.Enabled,
		Desired: DesiredState(p, cfg), State: "stopped",
	}
	record, err := readProcessRecord(p, name)
	if err == nil {
		status.PID = record.PID
		if validateProcessRecord(record) == nil {
			status.State = "running"
			status.Managed = true
		} else {
			status.State = "stale"
		}
	} else if _, statErr := os.Stat(pidPath(p, name)); statErr == nil {
		status.State = "stale"
	}
	return status
}

func StatusAll(p paths.Paths) ([]Status, error) {
	names, err := List(p)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(names))
	for _, name := range names {
		out = append(out, StatusOne(p, name))
	}
	return out, nil
}

func PreviewStart(p paths.Paths, name string) (Preview, error) {
	cfg, err := Parse(p, name)
	if err != nil {
		return Preview{}, err
	}
	if err := validateStartConfig(p, cfg); err != nil {
		return Preview{}, err
	}
	effective, err := EffectiveVFS(cfg)
	if err != nil {
		return Preview{}, err
	}
	return Preview{
		Name: name, Remote: cfg.Remote, Mountpoint: cfg.Mountpoint, VFSProfile: cfg.VFSProfile,
		VFSCacheMode: effective.CacheMode, VFSCacheMaxSize: effective.CacheMaxSize, VFSCacheMaxAge: effective.CacheMaxAge,
		DirCacheTime: effective.DirCacheTime, PollInterval: effective.PollInterval, AllowOther: cfg.AllowOther,
		HasExtraArgs: cfg.ArgsFile != "", ProviderReady: provider.Discover(p).Ready,
	}, nil
}

func validateStartConfig(p paths.Paths, cfg Config) error {
	return validateConfig(p, normalizeConfig(cfg))
}

func readExtraArgs(p paths.Paths, cfg Config) ([]string, error) {
	if cfg.ArgsFile == "" {
		return nil, nil
	}
	path := cfg.ArgsFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.Normalize().StateDir, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s: args_file does not exist", cfg.Name)
	}
	defer file.Close()
	var args []string
	scanner := bufio.NewScanner(io.LimitReader(file, 1<<20))
	scanner.Buffer(make([]byte, 4096), 256<<10)
	for scanner.Scan() {
		arg := scanner.Text()
		if arg == "" || strings.HasPrefix(arg, "#") {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(arg))
		if strings.HasPrefix(lower, "--rc") {
			return nil, fmt.Errorf("%s: args_file cannot override Nexus RC options", cfg.Name)
		}
		args = append(args, arg)
		if len(args) > 1024 {
			return nil, fmt.Errorf("%s: args_file contains too many arguments", cfg.Name)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return args, nil
}

func graceDuration() time.Duration {
	value := os.Getenv("RNEXUS_START_GRACE_SECONDS")
	if value == "" {
		return time.Second
	}
	if duration, err := time.ParseDuration(value + "s"); err == nil && duration >= 0 && duration <= 30*time.Second {
		return duration
	}
	return time.Second
}

func stopTimeout() time.Duration {
	value := os.Getenv("RNEXUS_STOP_TIMEOUT_SECONDS")
	if value == "" {
		return 20 * time.Second
	}
	if duration, err := time.ParseDuration(value + "s"); err == nil && duration >= 0 && duration <= 120*time.Second {
		return duration
	}
	return 20 * time.Second
}

func Start(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return ActionResult{}, err
	}
	var result ActionResult
	err := withMountLock(p, name, func() error {
		cfg, err := Parse(p, name)
		if err != nil {
			return err
		}
		if err := setDesiredState(p, name, DesiredRunning); err != nil {
			return err
		}
		result, err = startUnlocked(ctx, p, cfg)
		return err
	})
	return result, err
}

func startUnlocked(ctx context.Context, p paths.Paths, cfg Config) (ActionResult, error) {
	if err := validateStartConfig(p, cfg); err != nil {
		return ActionResult{}, err
	}
	if record, err := readProcessRecord(p, cfg.Name); err == nil {
		if validateProcessRecord(record) == nil {
			return ActionResult{Name: cfg.Name, State: "running", PID: record.PID, Noop: true}, nil
		}
		// A stale/reused PID record is never trusted or signalled. Clear only
		// Nexus-owned metadata, then launch a new process.
		removeProcessRecord(p, cfg.Name)
	} else {
		removeProcessRecord(p, cfg.Name)
	}

	rclone, err := provider.FindRclone(p)
	if err != nil {
		return ActionResult{}, fmt.Errorf("rclone binary not found; install/enable NewFuture module id 'rclone'")
	}
	if info, err := os.Stat(p.RcloneConfig); err != nil || !info.Mode().IsRegular() {
		return ActionResult{}, fmt.Errorf("rclone config not found")
	}
	if err := os.MkdirAll(cfg.Mountpoint, 0o755); err != nil {
		return ActionResult{}, err
	}
	cacheDir := filepath.Join(p.CacheDir, cfg.Name)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return ActionResult{}, err
	}
	_ = os.Chmod(cacheDir, 0o700)

	effectiveVFS, err := EffectiveVFS(cfg)
	if err != nil {
		return ActionResult{}, err
	}
	rcRecord, err := rc.Prepare(p, cfg.Name)
	if err != nil {
		return ActionResult{}, fmt.Errorf("prepare local RC endpoint: %w", err)
	}
	rcPrepared := true
	defer func() {
		if rcPrepared {
			rc.Remove(p, cfg.Name)
		}
	}()
	args := []string{
		"mount", cfg.Remote, cfg.Mountpoint,
		"--config", p.RcloneConfig,
		"--vfs-cache-mode", effectiveVFS.CacheMode,
		"--cache-dir", cacheDir,
		"--log-file", filepath.Join(p.LogDir, "mount-"+cfg.Name+".log"),
		"--log-level", cfg.LogLevel,
	}
	if effectiveVFS.CacheMaxSize != "" {
		args = append(args, "--vfs-cache-max-size", effectiveVFS.CacheMaxSize)
	}
	if effectiveVFS.CacheMaxAge != "" {
		args = append(args, "--vfs-cache-max-age", effectiveVFS.CacheMaxAge)
	}
	if effectiveVFS.DirCacheTime != "" {
		args = append(args, "--dir-cache-time", effectiveVFS.DirCacheTime)
	}
	if effectiveVFS.PollInterval != "" {
		args = append(args, "--poll-interval", effectiveVFS.PollInterval)
	}
	if cfg.AllowOther {
		args = append(args, "--allow-other")
	}
	if cfg.ReadOnly {
		args = append(args, "--read-only")
	}
	extra, err := readExtraArgs(p, cfg)
	if err != nil {
		return ActionResult{}, err
	}
	args = append(args, extra...)
	args = append(args, rc.Args(rcRecord)...)

	logPath := filepath.Join(p.LogDir, "mount-"+cfg.Name+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return ActionResult{}, err
	}
	cmd := exec.CommandContext(context.WithoutCancel(ctx), rclone, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return ActionResult{}, err
	}
	_ = logFile.Close()
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()

	record, err := newProcessRecord(pid, cfg)
	if err != nil {
		_ = cmd.Process.Kill()
		return ActionResult{}, fmt.Errorf("capture process identity: %w", err)
	}
	if err := writeProcessRecord(p, cfg.Name, record); err != nil {
		_ = cmd.Process.Kill()
		return ActionResult{}, err
	}
	if grace := graceDuration(); grace > 0 {
		timer := time.NewTimer(grace)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = cmd.Process.Kill()
			removeProcessRecord(p, cfg.Name)
			return ActionResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	if validateProcessRecord(record) != nil {
		removeProcessRecord(p, cfg.Name)
		return ActionResult{}, fmt.Errorf("%s: rclone exited during startup", cfg.Name)
	}
	rcPrepared = false
	return ActionResult{Name: cfg.Name, State: "started", PID: pid}, nil
}

func unmount(p paths.Paths, mountpoint string) {
	if mountpoint == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if helper, err := provider.FindFuseHelper(p); err == nil {
		if exec.CommandContext(ctx, helper, "-u", mountpoint).Run() == nil {
			return
		}
	}
	if helper, err := exec.LookPath("umount"); err == nil {
		_ = exec.CommandContext(ctx, helper, mountpoint).Run()
	}
}

func unmountIfOwned(p paths.Paths, cfg Config) bool {
	obs, err := ObserveRuntime(p, cfg.Name)
	if err != nil || !obs.MountAlive || !obs.OwnedMount {
		return false
	}
	unmount(p, cfg.Mountpoint)
	return true
}

func Stop(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return ActionResult{}, err
	}
	var result ActionResult
	err := withMountLock(p, name, func() error {
		cfg, err := Parse(p, name)
		if err != nil {
			return err
		}
		if err := setDesiredState(p, name, DesiredStopped); err != nil {
			return err
		}
		result, err = stopUnlocked(ctx, p, cfg)
		return err
	})
	return result, err
}

func stopUnlocked(ctx context.Context, p paths.Paths, cfg Config) (ActionResult, error) {
	record, err := readProcessRecord(p, cfg.Name)
	if err != nil {
		// No Nexus identity record means there is no authority to unmount a
		// path merely because it matches this configuration.
		removeProcessRecord(p, cfg.Name)
		rc.Remove(p, cfg.Name)
		return ActionResult{Name: cfg.Name, State: "stopped", Noop: true}, nil
	}
	if err := validateProcessRecord(record); err != nil {
		// Identity mismatch can be PID reuse. Never signal it. A dead Nexus
		// process may still leave an owned FUSE mount behind; ownership is
		// revalidated from the record/config/mount tuple before unmounting.
		_ = unmountIfOwned(p, cfg)
		removeProcessRecord(p, cfg.Name)
		rc.Remove(p, cfg.Name)
		return ActionResult{Name: cfg.Name, State: "stopped", Noop: true}, nil
	}

	_ = syscall.Kill(record.PID, syscall.SIGTERM)
	deadline := time.NewTimer(stopTimeout())
	ticker := time.NewTicker(100 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		if validateProcessRecord(record) != nil {
			break
		}
		select {
		case <-ctx.Done():
			return ActionResult{}, ctx.Err()
		case <-deadline.C:
			// Revalidate immediately before SIGKILL; if the original process
			// exited and the PID was reused, do not touch the replacement.
			if validateProcessRecord(record) == nil {
				_ = syscall.Kill(record.PID, syscall.SIGKILL)
			}
			goto stopped
		case <-ticker.C:
		}
	}

stopped:
	// Keep the process identity record until after the ownership check so a
	// just-exited Nexus process can still authorize cleanup of its own FUSE
	// mount. Never unmount solely from a configured path.
	_ = unmountIfOwned(p, cfg)
	removeProcessRecord(p, cfg.Name)
	rc.Remove(p, cfg.Name)
	return ActionResult{Name: cfg.Name, State: "stopped"}, nil
}

func Restart(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return ActionResult{}, err
	}
	var result ActionResult
	err := withMountLock(p, name, func() error {
		cfg, err := Parse(p, name)
		if err != nil {
			return err
		}
		if err := setDesiredState(p, name, DesiredRunning); err != nil {
			return err
		}
		if _, err := stopUnlocked(ctx, p, cfg); err != nil {
			return err
		}
		result, err = startUnlocked(ctx, p, cfg)
		return err
	})
	return result, err
}

func Reconcile(ctx context.Context, p paths.Paths, progress func(name, state string)) (ReconcileReport, error) {
	p = p.Normalize()
	names, err := List(p)
	if err != nil {
		return ReconcileReport{}, err
	}
	report := ReconcileReport{}
	for _, name := range names {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}
		var result ActionResult
		action := ""
		err = withMountLock(p, name, func() error {
			// Desired state and observed state are authoritative only after the
			// per-mount lock is held. This prevents a stale reconcile decision
			// from undoing a concurrent explicit start/stop.
			cfg, lockErr := Parse(p, name)
			if lockErr != nil {
				return lockErr
			}
			desired := DesiredState(p, cfg)
			status := StatusOne(p, name)
			switch {
			case desired == DesiredRunning && status.State != "running":
				action = "start"
				if progress != nil {
					progress(name, "starting")
				}
				result, lockErr = startUnlocked(ctx, p, cfg)
			case desired == DesiredStopped && status.State == "running":
				action = "stop"
				if progress != nil {
					progress(name, "stopping")
				}
				result, lockErr = stopUnlocked(ctx, p, cfg)
			}
			return lockErr
		})
		if err != nil {
			report.Failures = append(report.Failures, lifecycleFailure(name, err))
			if progress != nil {
				progress(name, "failed")
			}
			continue
		}
		if action == "" {
			continue
		}
		report.Changed = append(report.Changed, result)
		if progress != nil {
			progress(name, result.State)
		}
	}
	return report, nil
}

func planLifecycle(_ paths.Paths, oldConfigs, nextConfigs []Config, changes []ConfigChange) []lifecyclePlanItem {
	oldMap := map[string]Config{}
	nextMap := map[string]Config{}
	for _, cfg := range oldConfigs {
		oldMap[cfg.Name] = cfg
	}
	for _, cfg := range nextConfigs {
		nextMap[cfg.Name] = cfg
	}
	plan := make([]lifecyclePlanItem, 0, len(changes))
	for _, change := range changes {
		old, oldOK := oldMap[change.Name]
		next, newOK := nextMap[change.Name]
		item := lifecyclePlanItem{Name: change.Name, RestartRequired: change.Restart}
		if oldOK {
			oldCopy := old
			item.Old = &oldCopy
		}
		if newOK {
			newCopy := next
			item.New = &newCopy
		}
		plan = append(plan, item)
	}
	return plan
}

func executeLifecyclePlan(ctx context.Context, p paths.Paths, plan []lifecyclePlanItem, progress func(string, string)) ([]ActionResult, []LifecycleFailure) {
	actions := make([]ActionResult, 0)
	failures := make([]LifecycleFailure, 0)
	for _, item := range plan {
		select {
		case <-ctx.Done():
			failures = append(failures, lifecycleFailure(item.Name, ctx.Err()))
			return actions, failures
		default:
		}
		var result ActionResult
		var action string
		var actionErr error
		lockErr := withMountLock(p, item.Name, func() error {
			// Re-evaluate desired/observed state after acquiring the mount lock.
			// This prevents a concurrent explicit stop from being undone by a
			// configuration apply that planned a restart moments earlier.
			if item.New == nil {
				if item.Old != nil {
					status := StatusOne(p, item.Name)
					if status.State == "running" {
						action = "stop"
						result, actionErr = stopUnlocked(ctx, p, *item.Old)
					}
				}
				return actionErr
			}

			status := StatusOne(p, item.Name)
			desired := DesiredState(p, *item.New)
			switch {
			case desired == DesiredStopped && status.State == "running":
				action = "stop"
				if progress != nil {
					progress(item.Name, "stopping")
				}
				old := item.New
				if item.Old != nil {
					old = item.Old
				}
				result, actionErr = stopUnlocked(ctx, p, *old)
			case desired == DesiredRunning && status.State != "running":
				action = "start"
				if progress != nil {
					progress(item.Name, "starting")
				}
				result, actionErr = startUnlocked(ctx, p, *item.New)
			case desired == DesiredRunning && status.State == "running" && item.RestartRequired:
				action = "restart"
				if progress != nil {
					progress(item.Name, "restarting")
				}
				old := item.New
				if item.Old != nil {
					old = item.Old
				}
				if _, actionErr = stopUnlocked(ctx, p, *old); actionErr == nil {
					result, actionErr = startUnlocked(ctx, p, *item.New)
				}
			}
			return actionErr
		})
		if lockErr != nil {
			actionErr = lockErr
		}
		if item.New == nil {
			removeDesiredState(p, item.Name)
		}
		if action == "" && actionErr == nil {
			continue
		}
		if actionErr != nil {
			failures = append(failures, lifecycleFailure(item.Name, actionErr))
			if progress != nil {
				progress(item.Name, "failed")
			}
			continue
		}
		actions = append(actions, result)
		if progress != nil {
			progress(item.Name, result.State)
		}
	}
	return actions, failures
}

func lifecycleFailure(name string, err error) LifecycleFailure {
	code := "lifecycle_failed"
	message := err.Error()
	switch {
	case strings.Contains(message, "rclone binary not found"):
		code = "provider_unavailable"
	case strings.Contains(message, "rclone config not found"):
		code = "provider_config_missing"
	case strings.Contains(message, "args_file"):
		code = "args_file_invalid"
	}
	return LifecycleFailure{Name: name, Code: code, Error: message}
}
