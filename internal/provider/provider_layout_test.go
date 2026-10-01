package provider

import (
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func writeProviderExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/system/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestNewFutureVendorBinLayoutIsDiscovered(t *testing.T) {
	// Keep host tools from satisfying exec.LookPath before provider-module
	// fallbacks are exercised. The production precedence remains unchanged.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("RNEXUS_RCLONE_BIN", "")
	t.Setenv("RNEXUS_FUSERMOUNT_BIN", "")

	dir := t.TempDir()
	rclone := filepath.Join(dir, "system", "vendor", "bin", "rclone")
	fuse := filepath.Join(dir, "system", "vendor", "bin", "fusermount3")
	writeProviderExecutable(t, rclone)
	writeProviderExecutable(t, fuse)

	p := paths.Paths{ProviderModuleDir: dir}.Normalize()
	gotRclone, err := FindRclone(p)
	if err != nil {
		t.Fatalf("FindRclone: %v", err)
	}
	if gotRclone != rclone {
		t.Fatalf("FindRclone=%q want %q", gotRclone, rclone)
	}
	gotFuse, err := FindFuseHelper(p)
	if err != nil {
		t.Fatalf("FindFuseHelper: %v", err)
	}
	if gotFuse != fuse {
		t.Fatalf("FindFuseHelper=%q want %q", gotFuse, fuse)
	}
}
