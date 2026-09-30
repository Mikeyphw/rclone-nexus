package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	ModuleDir         string
	ProviderModuleDir string
	StateDir          string
	MountsDir         string
	RunDir            string
	LogDir            string
	CacheDir          string
	ConfigDir         string
	ConfigRegistry    string
	ConfigPrevious    string
	DesiredDir        string
	MountRunDir       string
	LockDir           string
	HealthDir         string
	OperationsDir     string
	NamespaceDir      string
	RcloneConfig      string
	FuseDevice        string
	Socket            string
	DaemonLock        string
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
		ModuleDir:         moduleDir,
		ProviderModuleDir: providerDir,
		StateDir:          stateDir,
		MountsDir:         env("RNEXUS_MOUNTS_DIR", filepath.Join(stateDir, "mounts.d")),
		RunDir:            runDir,
		LogDir:            env("RNEXUS_LOG_DIR", filepath.Join(stateDir, "logs")),
		CacheDir:          env("RNEXUS_CACHE_DIR", filepath.Join(stateDir, "cache")),
		ConfigDir:         env("RNEXUS_CONFIG_DIR", filepath.Join(stateDir, "config")),
		ConfigRegistry:    env("RNEXUS_CONFIG_REGISTRY", filepath.Join(stateDir, "config", "registry-v2.json")),
		ConfigPrevious:    env("RNEXUS_CONFIG_PREVIOUS", filepath.Join(stateDir, "config", "previous-v2.json")),
		DesiredDir:        env("RNEXUS_DESIRED_DIR", filepath.Join(stateDir, "desired")),
		MountRunDir:       env("RNEXUS_MOUNT_RUN_DIR", filepath.Join(runDir, "mounts")),
		LockDir:           env("RNEXUS_LOCK_DIR", filepath.Join(runDir, "locks")),
		HealthDir:         env("RNEXUS_HEALTH_DIR", filepath.Join(stateDir, "health")),
		OperationsDir:     env("RNEXUS_OPERATIONS_DIR", filepath.Join(stateDir, "operations")),
		NamespaceDir:      env("RNEXUS_NAMESPACE_DIR", filepath.Join(stateDir, "namespace")),
		RcloneConfig:      env("RCLONE_CONFIG", filepath.Join(providerDir, "conf", "rclone.conf")),
		FuseDevice:        env("RNEXUS_FUSE_DEVICE", "/dev/fuse"),
		Socket:            env("RNEXUS_RACD_SOCKET", filepath.Join(runDir, "racd.sock")),
		DaemonLock:        env("RNEXUS_RACD_LOCK", filepath.Join(runDir, "racd.lock")),
	}
	return p.Normalize()
}

func (p Paths) EnsureState() error {
	p = p.Normalize()
	for _, dir := range []string{
		p.StateDir, p.MountsDir, p.RunDir, p.LogDir, p.CacheDir,
		p.ConfigDir, p.DesiredDir, p.MountRunDir, p.LockDir, p.HealthDir, p.OperationsDir, p.NamespaceDir,
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
