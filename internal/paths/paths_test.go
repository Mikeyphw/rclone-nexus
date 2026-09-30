package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureStateUsesPrivateModes(t *testing.T) {
	base := t.TempDir()
	p := Paths{
		StateDir:  filepath.Join(base, "state"),
		MountsDir: filepath.Join(base, "state", "mounts.d"),
		RunDir:    filepath.Join(base, "state", "run"),
		LogDir:    filepath.Join(base, "state", "logs"),
		CacheDir:  filepath.Join(base, "state", "cache"),
	}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		p.StateDir, p.MountsDir, p.RunDir, p.LogDir, p.CacheDir,
		p.ConfigDir, p.DesiredDir, p.MountRunDir, p.LockDir, p.HealthDir, p.OperationsDir, p.NamespaceDir,
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("%s mode=%o want=700", dir, got)
		}
	}
}

func TestNormalizeDerivesLifecyclePathsFromStateRoot(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state")
	p := (Paths{StateDir: base}).Normalize()
	checks := map[string]string{
		p.MountsDir:      filepath.Join(base, "mounts.d"),
		p.ConfigDir:      filepath.Join(base, "config"),
		p.ConfigRegistry: filepath.Join(base, "config", "registry-v2.json"),
		p.ConfigPrevious: filepath.Join(base, "config", "previous-v2.json"),
		p.DesiredDir:     filepath.Join(base, "desired"),
		p.MountRunDir:    filepath.Join(base, "run", "mounts"),
		p.LockDir:        filepath.Join(base, "run", "locks"),
		p.HealthDir:      filepath.Join(base, "health"),
		p.OperationsDir:  filepath.Join(base, "operations"),
		p.NamespaceDir:   filepath.Join(base, "namespace"),
	}
	for got, want := range checks {
		if got != want {
			t.Fatalf("path=%s want=%s", got, want)
		}
	}
}
