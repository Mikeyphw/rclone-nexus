package platformlifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func TestUninstallPreservesPersistentStateUnlessExplicitlyArmed(t *testing.T) {
	p := paths.Paths{StateDir: filepath.Join(t.TempDir(), "state")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(p.ConfigDir, "keep")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupForUninstall(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("persistent config lost: %v", err)
	}
	if err := SetPurge(p, true); err != nil {
		t.Fatal(err)
	}
	result, err := CleanupForUninstall(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if !result.PersistentPurged {
		t.Fatal("expected explicit purge")
	}
	if _, err := os.Stat(p.StateDir); !os.IsNotExist(err) {
		t.Fatalf("state remains: %v", err)
	}
}
