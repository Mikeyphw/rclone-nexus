package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	cachegov "rclone-nexus/internal/cache"
	"rclone-nexus/internal/diagnostics"
	"rclone-nexus/internal/mounts"
	nsbridge "rclone-nexus/internal/namespace"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/policy"
	"rclone-nexus/internal/readiness"
)

const (
	Running       = "RUNNING"
	Degraded      = "DEGRADED"
	RemoteOffline = "REMOTE_OFFLINE"
	MountStale    = "MOUNT_STALE"
	AuthError     = "AUTH_ERROR"
	FuseError     = "FUSE_ERROR"
	Retrying      = "RETRYING"
	Stopped       = "STOPPED"
)

type Health struct {
	SchemaVersion   int                `json:"schema_version"`
	Name            string             `json:"name"`
	State           string             `json:"state"`
	Reason          string             `json:"reason,omitempty"`
	Desired         string             `json:"desired"`
	PID             int                `json:"pid,omitempty"`
	ProcessAlive    bool               `json:"process_alive"`
	MountAlive      bool               `json:"mount_alive"`
	OwnedMount      bool               `json:"owned_mount"`
	Attempts        int                `json:"restart_attempts"`
	RestartBudget   int                `json:"restart_budget"`
	NextRetryUnixMS int64              `json:"next_retry_unix_ms,omitempty"`
	LastError       string             `json:"last_error,omitempty"`
	UpdatedUnixMS   int64              `json:"updated_unix_ms"`
	Readiness       readiness.Snapshot `json:"readiness"`
	Policy          policy.Decision    `json:"policy"`
	Cache           cachegov.Status    `json:"cache"`
}

type Report struct {
	Changed  []mounts.ActionResult     `json:"changed"`
	Failures []mounts.LifecycleFailure `json:"failures,omitempty"`
	Health   []Health                  `json:"health"`
}

func healthPath(p paths.Paths, name string) string {
	return filepath.Join(p.Normalize().HealthDir, name+".json")
}

func readHealth(p paths.Paths, name string) Health {
	data, err := os.ReadFile(healthPath(p, name))
	if err != nil {
		return Health{Name: name, SchemaVersion: 1, RestartBudget: restartBudget()}
	}
	var h Health
	if json.Unmarshal(data, &h) != nil || h.SchemaVersion != 1 || h.Name != name {
		return Health{Name: name, SchemaVersion: 1, RestartBudget: restartBudget()}
	}
	h.RestartBudget = restartBudget()
	if h.Attempts > 0 && h.UpdatedUnixMS > 0 && time.Since(time.UnixMilli(h.UpdatedUnixMS)) > retryResetAfter() {
		h.Attempts, h.NextRetryUnixMS, h.LastError = 0, 0, ""
	}
	return h
}

func writeHealth(p paths.Paths, h Health) error {
	previous := Health{}
	if data, err := os.ReadFile(healthPath(p, h.Name)); err == nil {
		_ = json.Unmarshal(data, &previous)
	}
	h.SchemaVersion = 1
	h.RestartBudget = restartBudget()
	h.UpdatedUnixMS = time.Now().UnixMilli()
	payload, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	path := healthPath(p, h.Name)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".health-*")
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
	if previous.State != h.State || previous.Reason != h.Reason || previous.Desired != h.Desired || previous.Policy.State != h.Policy.State {
		_ = diagnostics.Append(p, "mount-health", h.Name, h.State, h.Reason, map[string]any{
			"desired": h.Desired, "process_alive": h.ProcessAlive, "mount_alive": h.MountAlive,
			"policy_state": h.Policy.State, "policy_reason": h.Policy.Reason,
		})
	}
	return nil
}

func readinessFor(ctx context.Context, p paths.Paths, cfg mounts.Config, boot bool) readiness.Snapshot {
	return readiness.Check(ctx, p, readiness.Spec{Name: cfg.Name, Remote: cfg.Remote, Mountpoint: cfg.Mountpoint, BootRequired: boot, RequireNetwork: cfg.RequireNetwork, ProbeRemote: cfg.ProbeRemote})
}

func resourcesFor(ctx context.Context, p paths.Paths, cfg mounts.Config, boot bool, record bool) (policy.Decision, cachegov.Status, error) {
	spec, err := mounts.PolicySpec(cfg)
	if err != nil {
		return policy.Decision{}, cachegov.Status{}, err
	}
	limits, err := mounts.CacheLimits(cfg)
	if err != nil {
		return policy.Decision{}, cachegov.Status{}, err
	}
	cacheStatus, err := cachegov.Inspect(p, cfg.Name, limits)
	if err != nil {
		return policy.Decision{}, cachegov.Status{}, err
	}
	var decision policy.Decision
	if record {
		decision, err = policy.EvaluateAndRecord(ctx, p, cfg.Name, spec, boot)
	} else {
		decision, err = policy.Evaluate(ctx, p, cfg.Name, spec, boot)
	}
	return decision, cacheStatus, err
}

func classify(cfg mounts.Config, desired string, obs mounts.RuntimeObservation, ready readiness.Snapshot, policyDecision policy.Decision, cacheStatus cachegov.Status, previous Health) Health {
	h := previous
	h.Name, h.Desired = cfg.Name, desired
	h.PID, h.ProcessAlive, h.MountAlive, h.OwnedMount = obs.PID, obs.ProcessAlive, obs.MountAlive, obs.OwnedMount
	h.Readiness = ready
	h.Policy = policyDecision
	h.Cache = cacheStatus
	h.Reason = ""
	if desired == mounts.DesiredStopped {
		h.State = Stopped
		return h
	}
	if obs.ProcessAlive && obs.MountAlive {
		switch {
		case ready.RemoteState == "auth_error":
			h.State, h.Reason = AuthError, "remote_auth_error"
		case ready.RemoteState == "offline" || ready.WaitingReason == "network_unavailable":
			h.State, h.Reason = RemoteOffline, "remote_offline"
		case ready.RemoteState == "error":
			h.State, h.Reason = Degraded, "remote_probe_failed"
		case ready.WaitingReason == "fuse_device_unavailable":
			h.State, h.Reason = FuseError, "fuse_device_unavailable"
		default:
			h.State = Running
		}
		return h
	}
	if obs.ProcessAlive != obs.MountAlive || (obs.MountAlive && obs.ProcessRecordPresent) {
		h.State, h.Reason = MountStale, "process_mount_disagree"
		if obs.MountAlive && !obs.OwnedMount {
			h.Reason = "unowned_mount_present"
		}
		return h
	}
	if !ready.Ready {
		h.Reason = ready.WaitingReason
		switch ready.WaitingReason {
		case "remote_auth_error":
			h.State = AuthError
		case "remote_offline", "network_unavailable":
			h.State = RemoteOffline
		case "fuse_device_unavailable":
			h.State = FuseError
		default:
			h.State = Retrying
		}
		return h
	}
	if h.Attempts >= restartBudget() {
		h.State, h.Reason = Degraded, "restart_budget_exhausted"
		return h
	}
	h.State, h.Reason = Retrying, "not_running"
	return h
}

func InspectOne(ctx context.Context, p paths.Paths, name string, boot bool) (Health, error) {
	cfg, err := mounts.Parse(p, name)
	if err != nil {
		return Health{}, err
	}
	desired := mounts.DesiredState(p, cfg)
	obs, err := mounts.ObserveRuntime(p, name)
	if err != nil {
		return Health{}, err
	}
	ready := readinessFor(ctx, p, cfg, boot)
	policyDecision, cacheStatus, err := resourcesFor(ctx, p, cfg, boot, false)
	if err != nil {
		return Health{}, err
	}
	return classify(cfg, desired, obs, ready, policyDecision, cacheStatus, readHealth(p, name)), nil
}

func InspectAll(ctx context.Context, p paths.Paths, boot bool) ([]Health, error) {
	names, err := mounts.List(p)
	if err != nil {
		return nil, err
	}
	out := make([]Health, 0, len(names))
	for _, name := range names {
		h, err := InspectOne(ctx, p, name, boot)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func waitForMountPublication(ctx context.Context, p paths.Paths, name string, pid int) (mounts.RuntimeObservation, error) {
	// A freshly-started rclone process can become observable before FUSE has
	// published its mount. Treat that as bounded startup convergence rather than
	// stale runtime. This wait never consumes restart budget and never accepts a
	// different/reused process identity.
	value := os.Getenv("RNEXUS_MOUNT_VISIBILITY_GRACE_MS")
	grace := 500 * time.Millisecond
	if value != "" {
		if ms, err := strconv.Atoi(value); err == nil && ms >= 0 && ms <= 30000 {
			grace = time.Duration(ms) * time.Millisecond
		}
	}
	deadline := time.Now().Add(grace)
	for {
		obs, err := mounts.ObserveRuntime(p, name)
		if err != nil {
			return obs, err
		}
		if !obs.ProcessAlive || obs.PID != pid || obs.MountAlive {
			return obs, nil
		}
		if time.Now().After(deadline) {
			return obs, nil
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return obs, ctx.Err()
		case <-timer.C:
		}
	}
}

func ReconcileOnce(ctx context.Context, p paths.Paths, boot bool, progress func(string, string)) (Report, error) {
	names, err := mounts.List(p)
	if err != nil {
		return Report{}, err
	}
	report := Report{}
	for _, name := range names {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		default:
		}
		cfg, err := mounts.Parse(p, name)
		if err != nil {
			report.Failures = append(report.Failures, failure(name, "config_invalid", err))
			continue
		}
		desired := mounts.DesiredState(p, cfg)
		obs, err := mounts.ObserveRuntime(p, name)
		if err != nil {
			report.Failures = append(report.Failures, failure(name, "observe_failed", err))
			continue
		}
		ready := readinessFor(ctx, p, cfg, boot)
		policyDecision, cacheStatus, resourceErr := resourcesFor(ctx, p, cfg, boot, true)
		if resourceErr != nil {
			report.Failures = append(report.Failures, failure(name, "policy_observe_failed", resourceErr))
			continue
		}
		h := classify(cfg, desired, obs, ready, policyDecision, cacheStatus, readHealth(p, name))

		if desired == mounts.DesiredStopped {
			if nsbridge.Desired(p, name) {
				if _, visibilityErr := nsbridge.SuspendOwned(ctx, p, name); visibilityErr != nil {
					h = noteFailure(h, visibilityErr)
					report.Failures = append(report.Failures, failure(name, "namespace_suspend_failed", visibilityErr))
					_ = writeHealth(p, h)
					report.Health = append(report.Health, h)
					continue
				}
			}
			if obs.ProcessAlive {
				if progress != nil {
					progress(name, "stopping")
				}
				action, stopErr := mounts.ReconcileStop(ctx, p, name)
				if stopErr != nil {
					h = noteFailure(h, stopErr)
					report.Failures = append(report.Failures, failure(name, "stop_failed", stopErr))
				} else {
					if !action.Noop {
						report.Changed = append(report.Changed, action)
					}
					h.Attempts, h.NextRetryUnixMS, h.LastError = 0, 0, ""
				}
			}
			// Re-read desired and observed state after the lock-protected action.
			// An explicit concurrent start may have won the lock and must be
			// reflected instead of being overwritten by stale STOPPED health.
			if fresh, freshErr := InspectOne(ctx, p, name, boot); freshErr == nil {
				h = fresh
			} else if len(report.Failures) == 0 || report.Failures[len(report.Failures)-1].Name != name {
				report.Failures = append(report.Failures, failure(name, "refresh_failed", freshErr))
			}
			_ = writeHealth(p, h)
			report.Health = append(report.Health, h)
			continue
		}

		if obs.ProcessAlive && obs.MountAlive {
			// A valid VFS mount is intentionally retained through remote/network
			// outages; health truth changes without destructive lifecycle action.
			if h.State == Running {
				h.Attempts, h.NextRetryUnixMS, h.LastError = 0, 0, ""
			}
			if nsbridge.Desired(p, name) {
				if _, visibilityErr := nsbridge.ReconcileDesired(ctx, p, name); visibilityErr != nil {
					report.Failures = append(report.Failures, failure(name, "namespace_reconcile_failed", visibilityErr))
				}
			}
			_ = writeHealth(p, h)
			report.Health = append(report.Health, h)
			continue
		}

		now := time.Now().UnixMilli()
		if h.NextRetryUnixMS > now {
			_ = writeHealth(p, h)
			report.Health = append(report.Health, h)
			continue
		}

		if h.State == MountStale {
			if nsbridge.Desired(p, name) {
				if _, visibilityErr := nsbridge.SuspendOwned(ctx, p, name); visibilityErr != nil {
					h = noteFailure(h, visibilityErr)
					report.Failures = append(report.Failures, failure(name, "namespace_suspend_failed", visibilityErr))
					_ = writeHealth(p, h)
					report.Health = append(report.Health, h)
					continue
				}
			}
			if obs.MountAlive && !obs.OwnedMount {
				h = noteFailure(h, fmt.Errorf("stale mount is not provably Nexus-owned"))
				_ = writeHealth(p, h)
				report.Health = append(report.Health, h)
				continue
			}
			if progress != nil {
				progress(name, "cleaning-stale")
			}
			action, repairErr := mounts.RepairStale(ctx, p, name)
			if repairErr != nil {
				h = noteFailure(h, repairErr)
				report.Failures = append(report.Failures, failure(name, "stale_cleanup_failed", repairErr))
				_ = writeHealth(p, h)
				report.Health = append(report.Health, h)
				continue
			}
			if !action.Noop {
				report.Changed = append(report.Changed, action)
			}
			if fresh, freshErr := InspectOne(ctx, p, name, boot); freshErr == nil {
				h = fresh
				desired = h.Desired
				obs, _ = mounts.ObserveRuntime(p, name)
				if currentCfg, cfgErr := mounts.Parse(p, name); cfgErr == nil {
					cfg = currentCfg
					ready = h.Readiness
				}
			}
		}

		if desired == mounts.DesiredStopped {
			_ = writeHealth(p, h)
			report.Health = append(report.Health, h)
			continue
		}
		// Cache pruning is ownership-bounded and only runs while the mount is
		// not alive. This closes storage pressure before policy evaluation
		// without racing an active rclone VFS cache.
		if !obs.ProcessAlive && h.Cache.PrunePending {
			limits, limitsErr := mounts.CacheLimits(cfg)
			if limitsErr == nil {
				if progress != nil {
					progress(name, "pruning-cache")
				}
				if _, pruneErr := cachegov.Prune(ctx, p, name, limits); pruneErr != nil {
					report.Failures = append(report.Failures, failure(name, "cache_prune_failed", pruneErr))
				} else if refreshed, refreshedErr := InspectOne(ctx, p, name, boot); refreshedErr == nil {
					h = refreshed
					policyDecision = h.Policy
					cacheStatus = h.Cache
				}
			}
		}
		if !h.Policy.Allowed {
			h.State, h.Reason = Retrying, "policy_blocked"
			h.NextRetryUnixMS = 0
			_ = writeHealth(p, h)
			report.Health = append(report.Health, h)
			continue
		}
		if !ready.Ready {
			// Waiting dependencies do not consume restart budget.
			_ = writeHealth(p, h)
			report.Health = append(report.Health, h)
			continue
		}
		if h.Attempts >= restartBudget() {
			h.State, h.Reason = Degraded, "restart_budget_exhausted"
			_ = writeHealth(p, h)
			report.Health = append(report.Health, h)
			continue
		}

		if progress != nil {
			progress(name, "starting")
		}
		action, startErr := mounts.ReconcileStart(ctx, p, name)
		if startErr != nil {
			h = noteFailure(h, startErr)
			report.Failures = append(report.Failures, failure(name, "start_failed", startErr))
		} else {
			if !action.Noop {
				report.Changed = append(report.Changed, action)
			}
			h.Attempts, h.NextRetryUnixMS, h.LastError = 0, 0, ""
			if action.PID > 0 {
				if converged, waitErr := waitForMountPublication(ctx, p, name, action.PID); waitErr == nil {
					obs = converged
				} else {
					report.Failures = append(report.Failures, failure(name, "mount_visibility_wait_failed", waitErr))
				}
			}
			if fresh, freshErr := InspectOne(ctx, p, name, boot); freshErr == nil {
				h = fresh
				obs, _ = mounts.ObserveRuntime(p, name)
			} else {
				report.Failures = append(report.Failures, failure(name, "refresh_failed", freshErr))
			}
			if obs.ProcessAlive && !obs.MountAlive && h.Desired == mounts.DesiredRunning {
				h.State, h.Reason = MountStale, "mount_not_visible_after_start"
				h = noteFailure(h, fmt.Errorf("mount not visible after start grace"))
			}
			if obs.ProcessAlive && obs.MountAlive && nsbridge.Desired(p, name) {
				if _, visibilityErr := nsbridge.ReconcileDesired(ctx, p, name); visibilityErr != nil {
					report.Failures = append(report.Failures, failure(name, "namespace_reconcile_failed", visibilityErr))
				}
			}
		}
		_ = writeHealth(p, h)
		report.Health = append(report.Health, h)
	}
	sort.Slice(report.Health, func(i, j int) bool { return report.Health[i].Name < report.Health[j].Name })
	return report, nil
}

func Reconcile(ctx context.Context, p paths.Paths, boot bool, progress func(string, string)) (Report, error) {
	if !boot {
		return ReconcileOnce(ctx, p, false, progress)
	}
	deadline := time.Now().Add(bootWait())
	delay := retryBase()
	var last Report
	for {
		report, err := ReconcileOnce(ctx, p, true, progress)
		if err != nil {
			return report, err
		}
		last = report
		if !needsBoundedWait(report.Health) || time.Now().After(deadline) {
			return report, nil
		}
		if progress != nil {
			progress("*", "waiting-dependencies")
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > retryMax() {
			delay = retryMax()
		}
	}
}

func Run(ctx context.Context, p paths.Paths, progress func(string, string)) {
	_ = p.Normalize().EnsureState()
	_, _ = ReconcileOnce(ctx, p, true, progress)
	diagnostics.RotateRuntimeLogs(p)
	ticker := time.NewTicker(supervisorInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = ReconcileOnce(ctx, p, true, progress)
			diagnostics.RotateRuntimeLogs(p)
		}
	}
}

func noteFailure(h Health, err error) Health {
	h.Attempts++
	h.LastError = safeFailureReason(err)
	h.NextRetryUnixMS = time.Now().Add(backoff(h.Attempts)).UnixMilli()
	if h.Attempts >= restartBudget() {
		h.State, h.Reason = Degraded, "restart_budget_exhausted"
	} else {
		h.State, h.Reason = Retrying, "recovery_backoff"
	}
	return h
}

func safeFailureReason(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "rclone binary not found"):
		return "provider_unavailable"
	case strings.Contains(message, "rclone config not found"):
		return "provider_config_missing"
	case strings.Contains(message, "args_file"):
		return "args_file_invalid"
	case strings.Contains(message, "not visible after start"):
		return "mount_not_visible"
	case strings.Contains(message, "not provably nexus-owned"):
		return "ownership_unproven"
	case strings.Contains(message, "exited during startup"):
		return "process_exited_during_startup"
	default:
		return "lifecycle_failed"
	}
}

func failure(name, code string, err error) mounts.LifecycleFailure {
	return mounts.LifecycleFailure{Name: name, Code: code, Error: err.Error()}
}

func needsBoundedWait(health []Health) bool {
	for _, h := range health {
		switch h.State {
		case Retrying, FuseError, RemoteOffline, MountStale:
			return true
		}
	}
	return false
}

func envInt(name string, fallback, min, max int) int {
	if value := os.Getenv(name); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n >= min && n <= max {
			return n
		}
	}
	return fallback
}
func restartBudget() int { return envInt("RNEXUS_RESTART_BUDGET", 5, 1, 20) }
func retryBase() time.Duration {
	return time.Duration(envInt("RNEXUS_RETRY_BASE_MS", 2000, 10, 60000)) * time.Millisecond
}
func retryMax() time.Duration {
	return time.Duration(envInt("RNEXUS_RETRY_MAX_MS", 60000, 10, 300000)) * time.Millisecond
}
func bootWait() time.Duration {
	return time.Duration(envInt("RNEXUS_BOOT_WAIT_SECONDS", 180, 0, 900)) * time.Second
}
func supervisorInterval() time.Duration {
	return time.Duration(envInt("RNEXUS_SUPERVISOR_INTERVAL_SECONDS", 15, 1, 300)) * time.Second
}
func retryResetAfter() time.Duration {
	return time.Duration(envInt("RNEXUS_RETRY_RESET_SECONDS", 900, 10, 86400)) * time.Second
}
func backoff(attempt int) time.Duration {
	delay := retryBase()
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= retryMax() {
			return retryMax()
		}
	}
	if delay > retryMax() {
		return retryMax()
	}
	return delay
}

func StateValues() []string {
	return []string{Running, Degraded, RemoteOffline, MountStale, AuthError, FuseError, Retrying, Stopped}
}
func IsHealthState(value string) bool {
	for _, state := range StateValues() {
		if strings.EqualFold(value, state) {
			return true
		}
	}
	return false
}
