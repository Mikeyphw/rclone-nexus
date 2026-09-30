package mounts

import (
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/vfs"
)

func TestNamedProfileIsExplicitAndCustomIsLossless(t *testing.T) {
	custom := Config{Name: "drive", VFSProfile: vfs.ProfileCustom, VFSCacheMode: "writes", VFSCacheMaxSize: "123MiB", VFSCacheMaxAge: "7m", DirCacheTime: "9m", PollInterval: "11s"}
	got, err := EffectiveVFS(custom)
	if err != nil {
		t.Fatal(err)
	}
	if got.CacheMode != "writes" || got.CacheMaxSize != "123MiB" || got.CacheMaxAge != "7m" || got.DirCacheTime != "9m" || got.PollInterval != "11s" {
		t.Fatalf("custom mutated: %+v", got)
	}
	custom.VFSProfile = vfs.ProfileStreaming
	got, err = EffectiveVFS(custom)
	if err != nil {
		t.Fatal(err)
	}
	if got.CacheMode != "full" || got.CacheMaxSize != "4GiB" {
		t.Fatalf("profile not expanded: %+v", got)
	}
}

func TestPolicyOnlyDiffDoesNotRestartButProfileDoes(t *testing.T) {
	base := Config{Name: "drive", Enabled: true, Remote: "fake:", Mountpoint: "/storage/emulated/0/Drive", VFSCacheMode: "full", VFSProfile: vfs.ProfileCustom, NetworkMode: "any", CacheHighWater: 90, CacheLowWater: 75}
	policy := base
	policy.NetworkMode = "wifi"
	policy.ChargingOnly = true
	policy.MinBattery = 30
	policy.CacheHighWater = 85
	policy.CacheLowWater = 70
	changes := diffConfigs([]Config{base}, []Config{policy})
	if len(changes) != 1 || changes[0].Restart {
		t.Fatalf("policy-only diff should not restart: %+v", changes)
	}
	profile := base
	profile.VFSProfile = vfs.ProfileStreaming
	changes = diffConfigs([]Config{base}, []Config{profile})
	if len(changes) != 1 || !changes[0].Restart {
		t.Fatalf("profile change must restart: %+v", changes)
	}
}

func TestLegacyPolicyIntegersFailClosed(t *testing.T) {
	p := lifecycleTestPaths(t)
	path := filepath.Join(p.MountsDir, "drive.conf")
	if err := os.WriteFile(path, []byte("enabled=true\nremote=fake:\nmountpoint=/storage/emulated/0/Drive\nmin_battery=not-a-number\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(p, "drive"); err == nil {
		t.Fatal("invalid legacy min_battery should fail")
	}
}
