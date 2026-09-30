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

func FromEnv() Paths {
	moduleDir := env("RNEXUS_MODULE_DIR", "/data/adb/modules/rclone_nexus")
	providerDir := env("RNEXUS_PROVIDER_MODULE_DIR", "/data/adb/modules/rclone")
	stateDir := env("RNEXUS_STATE_DIR", "/data/adb/rclone-nexus")
	runDir := env("RNEXUS_RUN_DIR", filepath.Join(stateDir, "run"))
	return Paths{
		ModuleDir:         moduleDir,
		ProviderModuleDir: providerDir,
		StateDir:          stateDir,
		MountsDir:         env("RNEXUS_MOUNTS_DIR", filepath.Join(stateDir, "mounts.d")),
		RunDir:            runDir,
		LogDir:            env("RNEXUS_LOG_DIR", filepath.Join(stateDir, "logs")),
		CacheDir:          env("RNEXUS_CACHE_DIR", filepath.Join(stateDir, "cache")),
		RcloneConfig:      env("RCLONE_CONFIG", filepath.Join(providerDir, "conf", "rclone.conf")),
		FuseDevice:        env("RNEXUS_FUSE_DEVICE", "/dev/fuse"),
		Socket:            env("RNEXUS_RACD_SOCKET", filepath.Join(runDir, "racd.sock")),
		DaemonLock:        env("RNEXUS_RACD_LOCK", filepath.Join(runDir, "racd.lock")),
	}
}

func (p Paths) EnsureState() error {
	for _, dir := range []string{p.StateDir, p.MountsDir, p.RunDir, p.LogDir, p.CacheDir} {
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
