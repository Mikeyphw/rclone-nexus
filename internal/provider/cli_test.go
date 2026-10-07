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

func staticProviderPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{
		ModuleDir:         filepath.Join(root, "module"),
		StateDir:          filepath.Join(root, "state"),
		ProviderModuleDir: filepath.Join(root, "provider"),
	}.Normalize()
	return p
}

func TestUnsupportedMountFlagsUsesBundledCommandAndGlobalHelp(t *testing.T) {
	p := staticProviderPaths(t)
	binary := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
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
	// Neither PATH nor the old runtime override may redirect static authority.
	poison := t.TempDir()
	t.Setenv("PATH", poison)
	t.Setenv("RNEXUS_RCLONE_BIN", filepath.Join(poison, "rclone"))
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
	dir := t.TempDir()
	selected := filepath.Join(dir, "selected-rclone")
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
	missing, err := UnsupportedMountFlagsForBinary(context.Background(), selected, []string{"mount", "x:", "/m", "--selected-only"})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("exact selected binary was not used, missing=%v", missing)
	}
}
