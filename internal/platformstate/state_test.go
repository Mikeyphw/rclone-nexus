package platformstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func pstate(t *testing.T) paths.Paths { return paths.Paths{StateDir: t.TempDir()}.Normalize() }

func TestMigrateBootstrapAndRejectDowngrade(t *testing.T) {
	p := pstate(t)
	r, err := Migrate(p)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Bootstrapped {
		t.Fatalf("expected bootstrap: %+v", r)
	}
	if err := os.WriteFile(filepath.Join(p.PlatformDir, "state.json"), []byte(`{"schema_version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUpgrade(p); err == nil {
		t.Fatal("newer schema must fail closed")
	}
}

func TestMigrationPreservesPersistentUserState(t *testing.T) {
	p := pstate(t)
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	canaries := []string{filepath.Join(p.ConfigDir, "registry-v2.json"), filepath.Join(p.CacheDir, "keep.bin"), filepath.Join(p.JobsDir, "registry-v1.json")}
	for _, path := range canaries {
		if err := os.WriteFile(path, []byte("canary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Migrate(p); err != nil {
		t.Fatal(err)
	}
	for _, path := range canaries {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "canary" {
			t.Fatalf("persistent state changed: %s %v", path, err)
		}
	}
}

func TestFailedMigrationRestoresOriginal(t *testing.T) {
	p := pstate(t)
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"schema_version\":0,\"updated_unix_ms\":1}\n")
	if err := os.WriteFile(filepath.Join(p.PlatformDir, "state.json"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := migrateWithHook(p, func() error { return errors.New("boom") })
	if err == nil {
		t.Fatal("expected failure")
	}
	got, _ := os.ReadFile(filepath.Join(p.PlatformDir, "state.json"))
	if string(got) != string(original) {
		t.Fatalf("state not restored: %s", got)
	}
}
