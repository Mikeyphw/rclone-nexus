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
	t.Setenv("RNEXUS_RUNTIME_MODE", "external")
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

func TestProviderModuleWinsOverHostPath(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "external")
	host := t.TempDir()
	hostRclone := filepath.Join(host, "rclone")
	hostFuse := filepath.Join(host, "fusermount3")
	writeProviderExecutable(t, hostRclone)
	writeProviderExecutable(t, hostFuse)
	t.Setenv("PATH", host)
	t.Setenv("RNEXUS_RCLONE_BIN", "")
	t.Setenv("RNEXUS_FUSERMOUNT_BIN", "")

	dir := t.TempDir()
	providerRclone := filepath.Join(dir, "system", "vendor", "bin", "rclone")
	providerFuse := filepath.Join(dir, "system", "vendor", "bin", "fusermount3")
	writeProviderExecutable(t, providerRclone)
	writeProviderExecutable(t, providerFuse)
	p := paths.Paths{ProviderModuleDir: dir}.Normalize()

	if got, err := FindRclone(p); err != nil || got != providerRclone {
		t.Fatalf("FindRclone=%q err=%v want provider %q", got, err, providerRclone)
	}
	if got, err := FindFuseHelper(p); err != nil || got != providerFuse {
		t.Fatalf("FindFuseHelper=%q err=%v want provider %q", got, err, providerFuse)
	}
}
