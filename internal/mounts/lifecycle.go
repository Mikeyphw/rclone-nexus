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
	"strconv"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/provider"
)

type Status struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Enabled    bool   `json:"enabled,omitempty"`
	State      string `json:"state"`
	PID        int    `json:"pid,omitempty"`
}

type ActionResult struct {
	Name  string `json:"name"`
	State string `json:"state"`
	PID   int    `json:"pid,omitempty"`
	Noop  bool   `json:"noop,omitempty"`
}

type Preview struct {
	Name          string `json:"name"`
	Remote        string `json:"remote"`
	Mountpoint    string `json:"mountpoint"`
	VFSCacheMode  string `json:"vfs_cache_mode"`
	AllowOther    bool   `json:"allow_other"`
	HasExtraArgs  bool   `json:"has_extra_args"`
	ProviderReady bool   `json:"provider_ready"`
}

func pidPath(p paths.Paths, name string) string { return filepath.Join(p.RunDir, name+".pid") }

func readPID(p paths.Paths, name string) (int, error) {
	data, err := os.ReadFile(pidPath(p, name))
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, errors.New("invalid pid file")
	}
	return pid, nil
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func StatusOne(p paths.Paths, name string) Status {
	cfg, err := Parse(p, name)
	if err != nil {
		return Status{Name: name, Configured: false, State: "not-configured"}
	}
	status := Status{Name: name, Configured: true, Enabled: cfg.Enabled, State: "stopped"}
	if pid, err := readPID(p, name); err == nil {
		status.PID = pid
		if alive(pid) {
			status.State = "running"
		} else {
			status.State = "stale"
		}
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
	if err := validateStartConfig(cfg); err != nil {
		return Preview{}, err
	}
	return Preview{
		Name: name, Remote: cfg.Remote, Mountpoint: cfg.Mountpoint,
		VFSCacheMode: cfg.VFSCacheMode, AllowOther: cfg.AllowOther,
		HasExtraArgs: cfg.ArgsFile != "", ProviderReady: provider.Discover(p).Ready,
	}, nil
}

func validateStartConfig(cfg Config) error {
	if cfg.Remote == "" {
		return fmt.Errorf("%s: missing remote=", cfg.Name)
	}
	if cfg.Mountpoint == "" {
		return fmt.Errorf("%s: missing mountpoint=", cfg.Name)
	}
	if !filepath.IsAbs(cfg.Mountpoint) {
		return fmt.Errorf("%s: mountpoint must be absolute", cfg.Name)
	}
	return nil
}

func readExtraArgs(p paths.Paths, cfg Config) ([]string, error) {
	if cfg.ArgsFile == "" {
		return nil, nil
	}
	path := cfg.ArgsFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.StateDir, path)
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
	if err := p.EnsureState(); err != nil {
		return ActionResult{}, err
	}
	cfg, err := Parse(p, name)
	if err != nil {
		return ActionResult{}, err
	}
	if err := validateStartConfig(cfg); err != nil {
		return ActionResult{}, err
	}
	if pid, err := readPID(p, name); err == nil && alive(pid) {
		return ActionResult{Name: name, State: "running", PID: pid, Noop: true}, nil
	}
	_ = os.Remove(pidPath(p, name))

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
	cacheDir := filepath.Join(p.CacheDir, name)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return ActionResult{}, err
	}
	_ = os.Chmod(cacheDir, 0o700)

	args := []string{
		"mount", cfg.Remote, cfg.Mountpoint,
		"--config", p.RcloneConfig,
		"--vfs-cache-mode", cfg.VFSCacheMode,
		"--cache-dir", cacheDir,
		"--log-file", filepath.Join(p.LogDir, "mount-"+name+".log"),
		"--log-level", cfg.LogLevel,
	}
	if cfg.AllowOther {
		args = append(args, "--allow-other")
	}
	extra, err := readExtraArgs(p, cfg)
	if err != nil {
		return ActionResult{}, err
	}
	args = append(args, extra...)

	logPath := filepath.Join(p.LogDir, "mount-"+name+".log")
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
	// racd is long-lived. Always reap the child after it exits so failed or
	// deliberately stopped mounts cannot accumulate zombie processes under
	// the daemon. The mount remains independently controllable by PID.
	go func() {
		_ = cmd.Wait()
	}()
	if err := os.WriteFile(pidPath(p, name), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return ActionResult{}, err
	}
	if grace := graceDuration(); grace > 0 {
		timer := time.NewTimer(grace)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = cmd.Process.Kill()
			_ = os.Remove(pidPath(p, name))
			return ActionResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	if !alive(pid) {
		_ = os.Remove(pidPath(p, name))
		return ActionResult{}, fmt.Errorf("%s: rclone exited during startup", name)
	}
	return ActionResult{Name: name, State: "started", PID: pid}, nil
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

func Stop(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	cfg, err := Parse(p, name)
	if err != nil {
		return ActionResult{}, err
	}
	pid, pidErr := readPID(p, name)
	if pidErr == nil && alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		deadline := time.NewTimer(stopTimeout())
		ticker := time.NewTicker(100 * time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()
		for alive(pid) {
			select {
			case <-ctx.Done():
				return ActionResult{}, ctx.Err()
			case <-deadline.C:
				_ = syscall.Kill(pid, syscall.SIGKILL)
				goto stopped
			case <-ticker.C:
			}
		}
	}

stopped:
	_ = os.Remove(pidPath(p, name))
	unmount(p, cfg.Mountpoint)
	return ActionResult{Name: name, State: "stopped", Noop: pidErr != nil}, nil
}

func Restart(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	if _, err := Stop(ctx, p, name); err != nil {
		return ActionResult{}, err
	}
	return Start(ctx, p, name)
}

func Reconcile(ctx context.Context, p paths.Paths, progress func(name, state string)) ([]ActionResult, error) {
	names, err := List(p)
	if err != nil {
		return nil, err
	}
	results := make([]ActionResult, 0, len(names))
	for _, name := range names {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}
		cfg, err := Parse(p, name)
		if err != nil || !cfg.Enabled {
			continue
		}
		status := StatusOne(p, name)
		if status.State == "running" {
			continue
		}
		if progress != nil {
			progress(name, "starting")
		}
		result, err := Start(ctx, p, name)
		if err != nil {
			return results, fmt.Errorf("%s: %w", name, err)
		}
		results = append(results, result)
		if progress != nil {
			progress(name, "started")
		}
	}
	return results, nil
}
