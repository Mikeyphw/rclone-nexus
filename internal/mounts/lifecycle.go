package mounts

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/diagnostics"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/rc"
	"rclone-nexus/internal/runtimestate"
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

type LifecycleError struct {
	Code      string
	Category  string
	Stage     string
	Message   string
	Detail    string
	Retryable bool
	ExitCode  int
}

func (e *LifecycleError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Message + ": " + e.Detail
	}
	return e.Message
}

type LifecycleFailure struct {
	Name      string `json:"name"`
	Code      string `json:"code"`
	Category  string `json:"category,omitempty"`
	Stage     string `json:"stage,omitempty"`
	Error     string `json:"error"`
	Detail    string `json:"detail,omitempty"`
	Retryable bool   `json:"retryable"`
	ExitCode  int    `json:"exit_code,omitempty"`
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

func startupLogTail(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	start := info.Size() - max
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(f, max))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		lower := strings.ToLower(line)
		if strings.Contains(lower, "fatal error:") || strings.Contains(lower, "unknown flag:") || strings.Contains(lower, "flag provided but not defined") || strings.Contains(lower, "failed to create file system") || strings.Contains(lower, "failed to mount") {
			if len(line) > 2048 {
				line = line[:2048]
			}
			return line
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "-") {
			continue
		}
		if len(line) > 2048 {
			line = line[:2048]
		}
		return line
	}
	return ""
}

func lifecycleDetail(p paths.Paths, detail string) string {
	p = p.Normalize()
	return diagnostics.SanitizeText(detail, p.StateDir, p.RuntimeDir, p.ModuleDir, p.ProviderModuleDir, p.ManagedRcloneConfig, p.RcloneConfig)
}

func terminalLifecycleError(p paths.Paths, name, code, category, stage, message, detail string) *LifecycleError {
	return &LifecycleError{
		Code: code, Category: category, Stage: stage, Message: name + ": " + message,
		Detail: lifecycleDetail(p, detail), Retryable: false,
	}
}

func startupLifecycleError(p paths.Paths, name, logPath string, waitErr error) *LifecycleError {
	detail := lifecycleDetail(p, startupLogTail(logPath, 16<<10))
	lower := strings.ToLower(detail)
	out := &LifecycleError{Code: "process_start_failed", Category: "runtime", Stage: "process_start", Message: name + ": rclone exited during startup", Detail: detail, Retryable: false}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		out.ExitCode = exitErr.ExitCode()
	}
	switch {
	case strings.Contains(lower, "unknown flag:") || strings.Contains(lower, "unknown shorthand flag") || strings.Contains(lower, "flag provided but not defined"):
		out.Code = "rclone_cli_incompatible"
		out.Message = name + ": rclone rejected a command-line option"
	case strings.Contains(lower, "unauthorized") || strings.Contains(lower, "invalid_grant") || strings.Contains(lower, "authentication") || strings.Contains(lower, "token expired") || strings.Contains(lower, "access denied"):
		out.Code = "remote_auth_error"
		out.Category = "remote"
		out.Message = name + ": remote authentication failed during startup"
	case strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no route to host") || strings.Contains(lower, "connection refused") || strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout") || strings.Contains(lower, "name resolution"):
		out.Code = "remote_offline"
		out.Category = "remote"
		out.Message = name + ": remote was unavailable during startup"
		out.Retryable = true
	case strings.Contains(lower, "fuse") && (strings.Contains(lower, "failed") || strings.Contains(lower, "error")):
		out.Code = "fuse_start_failed"
		out.Message = name + ": FUSE mount startup failed"
	}
	return out
}

func requireNoRuntimeTransition(p paths.Paths) error {
	inProgress, err := runtimestate.TransitionInProgress(p)
	if err != nil {
		return fmt.Errorf("read runtime activation state: %w", err)
	}
	if inProgress {
		return errors.New("runtime activation is in progress")
	}
	return nil
}

func Start(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	p = p.Normalize()
	if err := requireNoRuntimeTransition(p); err != nil {
		return ActionResult{}, err
	}
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
		result, err = startUnlocked(ctx, p, cfg, false)
		return err
	})
	return result, err
}

func startUnlocked(ctx context.Context, p paths.Paths, cfg Config, allowRuntimeTransition bool) (ActionResult, error) {
	if err := validateStartConfig(p, cfg); err != nil {
		return ActionResult{}, terminalLifecycleError(p, cfg.Name, "mount_config_invalid", "configuration", "config_validation", "mount configuration is invalid", err.Error())
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

	var rclone string
	var err error
	if allowRuntimeTransition {
		rclone, err = provider.FindRcloneForTransition(p)
	} else {
		rclone, err = provider.FindRclone(p)
	}
	if err != nil {
		return ActionResult{}, terminalLifecycleError(p, cfg.Name, "runtime_authority_unavailable", "runtime", "runtime_resolution", "canonical rclone runtime is unavailable", err.Error())
	}
	configPath, err := provider.ConfigPath(p)
	if err != nil {
		return ActionResult{}, terminalLifecycleError(p, cfg.Name, "runtime_config_unavailable", "runtime", "runtime_resolution", "canonical rclone configuration is unavailable", err.Error())
	}
	if info, err := os.Stat(configPath); err != nil || !info.Mode().IsRegular() {
		detail := "rclone config not found"
		if err != nil {
			detail = err.Error()
		}
		return ActionResult{}, terminalLifecycleError(p, cfg.Name, "runtime_config_unavailable", "runtime", "runtime_resolution", "canonical rclone configuration is unavailable", detail)
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
		return ActionResult{}, terminalLifecycleError(p, cfg.Name, "mount_config_invalid", "configuration", "vfs_resolution", "VFS configuration is invalid", err.Error())
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
		"--config", configPath,
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
		return ActionResult{}, terminalLifecycleError(p, cfg.Name, "mount_args_invalid", "configuration", "argv_prepare", "mount arguments are invalid", err.Error())
	}
	args = append(args, extra...)
	args = append(args, rc.Args(rcRecord)...)
	missingFlags, cliErr := provider.UnsupportedMountFlagsForBinary(ctx, rclone, args)
	if cliErr != nil {
		if ctx.Err() != nil {
			return ActionResult{}, ctx.Err()
		}
		return ActionResult{}, &LifecycleError{Code: "provider_cli_probe_failed", Category: "provider", Stage: "argv_preflight", Message: cfg.Name + ": could not qualify provider mount CLI", Detail: lifecycleDetail(p, cliErr.Error()), Retryable: false}
	}
	if len(missingFlags) != 0 {
		return ActionResult{}, &LifecycleError{Code: "rclone_cli_incompatible", Category: "runtime", Stage: "argv_preflight", Message: cfg.Name + ": provider rclone does not support the generated mount command", Detail: provider.FormatMissingFlags(missingFlags), Retryable: false}
	}

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
		return ActionResult{}, &LifecycleError{Code: "process_exec_failed", Category: "runtime", Stage: "process_exec", Message: cfg.Name + ": could not execute rclone", Detail: lifecycleDetail(p, err.Error()), Retryable: false}
	}
	_ = logFile.Close()
	pid := cmd.Process.Pid
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait(); close(waitCh) }()

	record, err := newProcessRecord(pid, cfg)
	if err != nil {
		select {
		case waitErr := <-waitCh:
			return ActionResult{}, startupLifecycleError(p, cfg.Name, logPath, waitErr)
		default:
		}
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
		case waitErr := <-waitCh:
			timer.Stop()
			removeProcessRecord(p, cfg.Name)
			return ActionResult{}, startupLifecycleError(p, cfg.Name, logPath, waitErr)
		case <-timer.C:
		}
	}
	if validateProcessRecord(record) != nil {
		removeProcessRecord(p, cfg.Name)
		var waitErr error
		select {
		case waitErr = <-waitCh:
		default:
		}
		return ActionResult{}, startupLifecycleError(p, cfg.Name, logPath, waitErr)
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

// QuiesceOwned stops a runtime process/mount only when Nexus ownership is
// provable, without changing the desired-state record. It is intentionally
// separate from Stop so runtime activation can restart the same desired mounts.
func QuiesceOwned(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
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
		obs, err := ObserveRuntime(p, name)
		if err != nil {
			return err
		}
		if !obs.ProcessAlive && !obs.MountAlive {
			result = ActionResult{Name: name, State: "stopped", Noop: true}
			return nil
		}
		if (obs.ProcessAlive && !obs.ProcessManaged) || (obs.MountAlive && !obs.OwnedMount) {
			return fmt.Errorf("%s: runtime is not provably Nexus-owned", name)
		}
		result, err = stopUnlocked(ctx, p, cfg)
		return err
	})
	return result, err
}

func Stop(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	p = p.Normalize()
	if err := requireNoRuntimeTransition(p); err != nil {
		return ActionResult{}, err
	}
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
	if err := requireNoRuntimeTransition(p); err != nil {
		return ActionResult{}, err
	}
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
		result, err = startUnlocked(ctx, p, cfg, false)
		return err
	})
	return result, err
}

func Reconcile(ctx context.Context, p paths.Paths, progress func(name, state string)) (ReconcileReport, error) {
	p = p.Normalize()
	if err := requireNoRuntimeTransition(p); err != nil {
		return ReconcileReport{}, err
	}
	return reconcile(ctx, p, progress, false)
}

// ReconcileRuntimeTransition is used only by the activation controller while
// it owns the runtime transaction. Desired state is preserved; candidate
// execution is allowed only through this explicit path.
func ReconcileRuntimeTransition(ctx context.Context, p paths.Paths, progress func(name, state string)) (ReconcileReport, error) {
	return reconcile(ctx, p.Normalize(), progress, true)
}

func reconcile(ctx context.Context, p paths.Paths, progress func(name, state string), allowRuntimeTransition bool) (ReconcileReport, error) {
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
				result, lockErr = startUnlocked(ctx, p, cfg, allowRuntimeTransition)
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
				result, actionErr = startUnlocked(ctx, p, *item.New, false)
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
					result, actionErr = startUnlocked(ctx, p, *item.New, false)
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
	var lifecycleErr *LifecycleError
	if errors.As(err, &lifecycleErr) {
		return LifecycleFailure{Name: name, Code: lifecycleErr.Code, Category: lifecycleErr.Category, Stage: lifecycleErr.Stage, Error: lifecycleErr.Message, Detail: lifecycleErr.Detail, Retryable: lifecycleErr.Retryable, ExitCode: lifecycleErr.ExitCode}
	}
	code := "lifecycle_failed"
	message := err.Error()
	switch {
	case strings.Contains(message, "runtime migration required"):
		code = "runtime_migration_required"
	case strings.Contains(message, "runtime authority ambiguous"):
		code = "runtime_authority_ambiguous"
	case strings.Contains(message, "managed rclone runtime not found"), strings.Contains(message, "external runtime mode requires"), strings.Contains(message, "rclone binary not found"):
		code = "runtime_authority_unavailable"
	case strings.Contains(message, "rclone config not found"), strings.Contains(message, "config path is not configured"):
		code = "runtime_config_unavailable"
	case strings.Contains(message, "args_file"):
		code = "args_file_invalid"
	}
	return LifecycleFailure{Name: name, Code: code, Category: "runtime", Error: message, Retryable: false}
}
