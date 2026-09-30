package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureStateUsesPrivateModes(t *testing.T) {
	base := t.TempDir()
	p := Paths{StateDir: filepath.Join(base, "state"), MountsDir: filepath.Join(base, "state", "mounts.d"), RunDir: filepath.Join(base, "state", "run"), LogDir: filepath.Join(base, "state", "logs"), CacheDir: filepath.Join(base, "state", "cache")}
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{p.StateDir, p.MountsDir, p.RunDir, p.LogDir, p.CacheDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("%s mode=%o want=700", dir, got)
		}
	}
}
