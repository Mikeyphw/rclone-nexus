package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func writeProviderHelpFixture(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedMountFlagsUsesCommandAndGlobalProviderHelp(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "external")
	dir := t.TempDir()
	binary := filepath.Join(dir, "system", "vendor", "bin", "rclone")
	// Keep this fixture PATH-independent. Android's /system/bin/sh does not
	// guarantee that printf is a shell builtin, and this test deliberately
	// empties PATH so a host rclone cannot leak into provider discovery.
	writeProviderHelpFixture(t, binary, `
case "${1:-}:${2:-}" in
  mount:--help)
    echo 'Flags:'
    echo '      --vfs-cache-mode string'
    exit 0
    ;;
  help:flags)
    echo 'Global Flags:'
    echo '      --config string'
    echo '      --rc'
    echo '      --rc-addr string'
    echo '      --rc-user string'
    echo '      --rc-pass string'
    exit 0
    ;;
  --help:)
    echo 'root help'
    exit 0
    ;;
esac
exit 1
`)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("RNEXUS_RCLONE_BIN", "")
	p := paths.Paths{ProviderModuleDir: dir}.Normalize()
	missing, err := UnsupportedMountFlags(context.Background(), p, []string{"mount", "x:", "/m", "--config", "/c", "--vfs-cache-mode", "full", "--rc", "--rc-addr", "127.0.0.1:1", "--rc-no-open-browser"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != "--rc-no-open-browser" {
		t.Fatalf("missing=%v", missing)
	}
}

func TestUnsupportedMountFlagsFallsBackToRootHelpForGlobalFlags(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "rclone")
	writeProviderHelpFixture(t, binary, `
case "${1:-}:${2:-}" in
  mount:--help)
    echo 'Flags:'
    echo '      --vfs-cache-mode string'
    exit 0
    ;;
  help:flags) exit 1 ;;
  --help:)
    echo 'Global Flags:'
    echo '      --config string'
    echo '      --rc'
    echo '      --rc-addr string'
    exit 0
    ;;
esac
exit 1
`)
	missing, err := UnsupportedMountFlagsForBinary(context.Background(), binary, []string{"mount", "x:", "/m", "--config", "/c", "--vfs-cache-mode", "full", "--rc", "--rc-addr", "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("root global help was not merged, missing=%v", missing)
	}
}

func TestUnsupportedMountFlagsForBinaryUsesExactSelectedExecutable(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "external")
	dir := t.TempDir()
	selected := filepath.Join(dir, "selected-rclone")
	otherDir := filepath.Join(dir, "provider")
	other := filepath.Join(otherDir, "system", "vendor", "bin", "rclone")
	writeProviderHelpFixture(t, selected, `
case "${1:-}:${2:-}" in
  mount:--help)
    echo 'Flags:'
    echo '  --selected-only'
    exit 0
    ;;
esac
exit 1
`)
	writeProviderHelpFixture(t, other, `
case "${1:-}:${2:-}" in
  mount:--help)
    echo 'Flags:'
    echo '  --other-only'
    exit 0
    ;;
esac
exit 1
`)
	p := paths.Paths{ProviderModuleDir: otherDir}.Normalize()
	if found, err := FindRclone(p); err != nil || found != other {
		t.Fatalf("FindRclone=%q err=%v want %q", found, err, other)
	}
	missing, err := UnsupportedMountFlagsForBinary(context.Background(), selected, []string{"mount", "x:", "/m", "--selected-only"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("exact selected provider was not used, missing=%v", missing)
	}
}
