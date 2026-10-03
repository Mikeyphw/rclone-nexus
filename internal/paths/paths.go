package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	ModuleDir           string
	ProviderModuleDir   string
	StateDir            string
	MountsDir           string
	RunDir              string
	LogDir              string
	CacheDir            string
	ConfigDir           string
	ConfigRegistry      string
	ConfigPrevious      string
	DesiredDir          string
	MountRunDir         string
	LockDir             string
	HealthDir           string
	OperationsDir       string
	NamespaceDir        string
	PolicyDir           string
	JobsDir             string
	JobStateDir         string
	JobLockDir          string
	JobRegistry         string
	RCDir               string
	DiagnosticsDir      string
	SupportDir          string
	PlatformDir         string
	RuntimeDir          string
	RuntimeStoreDir     string
	ManagedRuntimeDir   string
	ManagedRcloneBin    string
	ManagedConfigDir    string
	ManagedRcloneConfig string
	RcloneConfig        string
	FuseDevice          string
	Socket              string
	DaemonLock          string
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (p Paths) Normalize() Paths {
	if p.StateDir == "" {
		p.StateDir = "/data/adb/rclone-nexus"
	}
	if p.MountsDir == "" {
		p.MountsDir = filepath.Join(p.StateDir, "mounts.d")
	}
	if p.RunDir == "" {
		p.RunDir = filepath.Join(p.StateDir, "run")
	}
	if p.LogDir == "" {
		p.LogDir = filepath.Join(p.StateDir, "logs")
	}
	if p.CacheDir == "" {
		p.CacheDir = filepath.Join(p.StateDir, "cache")
	}
	if p.ConfigDir == "" {
		p.ConfigDir = filepath.Join(p.StateDir, "config")
	}
	if p.ConfigRegistry == "" {
		p.ConfigRegistry = filepath.Join(p.ConfigDir, "registry-v2.json")
	}
	if p.ConfigPrevious == "" {
		p.ConfigPrevious = filepath.Join(p.ConfigDir, "previous-v2.json")
	}
	if p.DesiredDir == "" {
		p.DesiredDir = filepath.Join(p.StateDir, "desired")
	}
	if p.MountRunDir == "" {
		p.MountRunDir = filepath.Join(p.RunDir, "mounts")
	}
	if p.LockDir == "" {
		p.LockDir = filepath.Join(p.RunDir, "locks")
	}
	if p.HealthDir == "" {
		p.HealthDir = filepath.Join(p.StateDir, "health")
	}
	if p.OperationsDir == "" {
		p.OperationsDir = filepath.Join(p.StateDir, "operations")
	}
	if p.NamespaceDir == "" {
		p.NamespaceDir = filepath.Join(p.StateDir, "namespace")
	}
	if p.PolicyDir == "" {
		p.PolicyDir = filepath.Join(p.StateDir, "policy")
	}
	if p.JobsDir == "" {
		p.JobsDir = filepath.Join(p.StateDir, "jobs")
	}
	if p.JobStateDir == "" {
		p.JobStateDir = filepath.Join(p.JobsDir, "state")
	}
	if p.JobLockDir == "" {
		p.JobLockDir = filepath.Join(p.RunDir, "jobs")
	}
	if p.JobRegistry == "" {
		p.JobRegistry = filepath.Join(p.JobsDir, "registry-v1.json")
	}
	if p.RCDir == "" {
		p.RCDir = filepath.Join(p.RunDir, "rc")
	}
	if p.DiagnosticsDir == "" {
		p.DiagnosticsDir = filepath.Join(p.StateDir, "diagnostics")
	}
	if p.SupportDir == "" {
		p.SupportDir = filepath.Join(p.DiagnosticsDir, "support")
	}
	if p.PlatformDir == "" {
		p.PlatformDir = filepath.Join(p.StateDir, "platform")
	}
	if p.RuntimeDir == "" {
		p.RuntimeDir = filepath.Join(p.StateDir, "runtime")
	}
	if p.RuntimeStoreDir == "" {
		p.RuntimeStoreDir = filepath.Join(p.StateDir, "runtimes")
	}
	if p.ManagedRuntimeDir == "" {
		p.ManagedRuntimeDir = filepath.Join(p.RuntimeDir, "active")
	}
	if p.ManagedRcloneBin == "" {
		p.ManagedRcloneBin = filepath.Join(p.ManagedRuntimeDir, "bin", "rclone")
	}
	if p.ManagedConfigDir == "" {
		p.ManagedConfigDir = filepath.Join(p.ConfigDir, "rclone")
	}
	if p.ManagedRcloneConfig == "" {
		p.ManagedRcloneConfig = filepath.Join(p.ManagedConfigDir, "rclone.conf")
	}
	if p.RcloneConfig == "" {
		p.RcloneConfig = p.ManagedRcloneConfig
	}
	if p.Socket == "" {
		p.Socket = filepath.Join(p.RunDir, "racd.sock")
	}
	if p.DaemonLock == "" {
		p.DaemonLock = filepath.Join(p.RunDir, "racd.lock")
	}
	return p
}

func FromEnv() Paths {
	moduleDir := env("RNEXUS_MODULE_DIR", "/data/adb/modules/rclone_nexus")
	providerDir := env("RNEXUS_PROVIDER_MODULE_DIR", "/data/adb/modules/rclone")
	stateDir := env("RNEXUS_STATE_DIR", "/data/adb/rclone-nexus")
	runDir := env("RNEXUS_RUN_DIR", filepath.Join(stateDir, "run"))
	p := Paths{
		ModuleDir:           moduleDir,
		ProviderModuleDir:   providerDir,
		StateDir:            stateDir,
		MountsDir:           env("RNEXUS_MOUNTS_DIR", filepath.Join(stateDir, "mounts.d")),
		RunDir:              runDir,
		LogDir:              env("RNEXUS_LOG_DIR", filepath.Join(stateDir, "logs")),
		CacheDir:            env("RNEXUS_CACHE_DIR", filepath.Join(stateDir, "cache")),
		ConfigDir:           env("RNEXUS_CONFIG_DIR", filepath.Join(stateDir, "config")),
		ConfigRegistry:      env("RNEXUS_CONFIG_REGISTRY", filepath.Join(stateDir, "config", "registry-v2.json")),
		ConfigPrevious:      env("RNEXUS_CONFIG_PREVIOUS", filepath.Join(stateDir, "config", "previous-v2.json")),
		DesiredDir:          env("RNEXUS_DESIRED_DIR", filepath.Join(stateDir, "desired")),
		MountRunDir:         env("RNEXUS_MOUNT_RUN_DIR", filepath.Join(runDir, "mounts")),
		LockDir:             env("RNEXUS_LOCK_DIR", filepath.Join(runDir, "locks")),
		HealthDir:           env("RNEXUS_HEALTH_DIR", filepath.Join(stateDir, "health")),
		OperationsDir:       env("RNEXUS_OPERATIONS_DIR", filepath.Join(stateDir, "operations")),
		NamespaceDir:        env("RNEXUS_NAMESPACE_DIR", filepath.Join(stateDir, "namespace")),
		PolicyDir:           env("RNEXUS_POLICY_DIR", filepath.Join(stateDir, "policy")),
		JobsDir:             env("RNEXUS_JOBS_DIR", filepath.Join(stateDir, "jobs")),
		JobStateDir:         env("RNEXUS_JOB_STATE_DIR", filepath.Join(stateDir, "jobs", "state")),
		JobLockDir:          env("RNEXUS_JOB_LOCK_DIR", filepath.Join(runDir, "jobs")),
		JobRegistry:         env("RNEXUS_JOB_REGISTRY", filepath.Join(stateDir, "jobs", "registry-v1.json")),
		RCDir:               env("RNEXUS_RC_DIR", filepath.Join(runDir, "rc")),
		DiagnosticsDir:      env("RNEXUS_DIAGNOSTICS_DIR", filepath.Join(stateDir, "diagnostics")),
		SupportDir:          env("RNEXUS_SUPPORT_DIR", filepath.Join(stateDir, "diagnostics", "support")),
		PlatformDir:         env("RNEXUS_PLATFORM_DIR", filepath.Join(stateDir, "platform")),
		RuntimeDir:          env("RNEXUS_RUNTIME_DIR", filepath.Join(stateDir, "runtime")),
		RuntimeStoreDir:     env("RNEXUS_RUNTIME_STORE_DIR", filepath.Join(stateDir, "runtimes")),
		ManagedRuntimeDir:   env("RNEXUS_MANAGED_RUNTIME_DIR", filepath.Join(stateDir, "runtime", "active")),
		ManagedRcloneBin:    env("RNEXUS_MANAGED_RCLONE_BIN", filepath.Join(stateDir, "runtime", "active", "bin", "rclone")),
		ManagedConfigDir:    env("RNEXUS_MANAGED_CONFIG_DIR", filepath.Join(stateDir, "config", "rclone")),
		ManagedRcloneConfig: env("RNEXUS_MANAGED_RCLONE_CONFIG", filepath.Join(stateDir, "config", "rclone", "rclone.conf")),
		RcloneConfig:        env("RNEXUS_MANAGED_RCLONE_CONFIG", filepath.Join(stateDir, "config", "rclone", "rclone.conf")),
		FuseDevice:          env("RNEXUS_FUSE_DEVICE", "/dev/fuse"),
		Socket:              env("RNEXUS_RACD_SOCKET", filepath.Join(runDir, "racd.sock")),
		DaemonLock:          env("RNEXUS_RACD_LOCK", filepath.Join(runDir, "racd.lock")),
	}
	return p.Normalize()
}

func (p Paths) EnsureState() error {
	p = p.Normalize()
	for _, dir := range []string{
		p.StateDir, p.MountsDir, p.RunDir, p.LogDir, p.CacheDir,
		p.ConfigDir, p.DesiredDir, p.MountRunDir, p.LockDir, p.HealthDir, p.OperationsDir, p.NamespaceDir, p.PolicyDir,
		p.JobsDir, p.JobStateDir, p.JobLockDir, p.RCDir, p.DiagnosticsDir, p.SupportDir, p.PlatformDir, p.RuntimeDir, p.RuntimeStoreDir, p.ManagedConfigDir,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create runtime directory %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("set runtime mode %s: %w", dir, err)
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(dir, 0, 0); err != nil {
				return fmt.Errorf("set root ownership %s: %w", dir, err)
			}
		}
	}
	return nil
}
