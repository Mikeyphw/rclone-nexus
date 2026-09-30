package mounts

import (
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func TestStatusIsReadOnlyForStalePID(t *testing.T) {
	base := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(base, "state"), MountsDir: filepath.Join(base, "state", "mounts.d"), RunDir: filepath.Join(base, "state", "run"), LogDir: filepath.Join(base, "state", "logs"), CacheDir: filepath.Join(base, "state", "cache")}
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.MountsDir, "drive.conf"), []byte("enabled=true\nremote=fake:\nmountpoint=/tmp/drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pid := filepath.Join(p.RunDir, "drive.pid")
	if err := os.WriteFile(pid, []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := StatusOne(p, "drive")
	if status.State != "stale" {
		t.Fatalf("state=%s", status.State)
	}
	if _, err := os.Stat(pid); err != nil {
		t.Fatalf("status mutated stale pid: %v", err)
	}
}
