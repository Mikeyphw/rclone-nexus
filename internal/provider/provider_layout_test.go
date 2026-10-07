package provider

import (
	"os"
	"path/filepath"
	"strings"
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

func TestBundledRuntimeWinsOverNewFutureVendorLayout(t *testing.T) {
	root := t.TempDir()
	providerDir := filepath.Join(root, "provider")
	providerRclone := filepath.Join(providerDir, "system", "vendor", "bin", "rclone")
	providerFuse := filepath.Join(providerDir, "system", "vendor", "bin", "fusermount3")
	writeProviderExecutable(t, providerRclone)
	writeProviderExecutable(t, providerFuse)

	p := paths.Paths{ModuleDir: filepath.Join(root, "module"), StateDir: filepath.Join(root, "state"), ProviderModuleDir: providerDir}.Normalize()
	bundled := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
	writeProviderExecutable(t, bundled)

	got, err := FindRclone(p)
	if err != nil {
		t.Fatalf("FindRclone: %v", err)
	}
	if got != bundled {
		t.Fatalf("FindRclone=%q want bundled %q", got, bundled)
	}
	if got, err := FindFuseHelper(p); err == nil || got != "" {
		t.Fatalf("legacy provider fuse helper unexpectedly became managed authority: got=%q err=%v", got, err)
	}
}

func TestBundledRuntimeWinsOverHostPathAndEnvironment(t *testing.T) {
	root := t.TempDir()
	host := filepath.Join(root, "host")
	hostRclone := filepath.Join(host, "rclone")
	writeProviderExecutable(t, hostRclone)
	t.Setenv("PATH", host)
	t.Setenv("RNEXUS_RCLONE_BIN", hostRclone)
	t.Setenv("RNEXUS_EXTERNAL_RCLONE_BIN", hostRclone)

	p := paths.Paths{ModuleDir: filepath.Join(root, "module"), StateDir: filepath.Join(root, "state"), ProviderModuleDir: filepath.Join(root, "provider")}.Normalize()
	bundled := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
	writeProviderExecutable(t, bundled)
	if got, err := FindRclone(p); err != nil || got != bundled {
		t.Fatalf("FindRclone=%q err=%v want bundled %q", got, err, bundled)
	}
}

func TestManagedFuseHelperPrecedesLegacyProviderAndIsInjectedIntoRuntimePath(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(root, "state"), ProviderModuleDir: filepath.Join(root, "provider")}.Normalize()
	managed := p.ManagedFuseHelperBin
	if err := os.MkdirAll(filepath.Dir(managed), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(p.ProviderModuleDir, "system", "vendor", "bin", "fusermount3")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_FUSERMOUNT_BIN", "")
	got, err := FindFuseHelper(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != managed {
		t.Fatalf("FindFuseHelper=%q want managed %q", got, managed)
	}
	env := RuntimeEnv(p)
	wantPrefix := filepath.Dir(managed) + string(os.PathListSeparator)
	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			found = true
			if !strings.HasPrefix(strings.TrimPrefix(entry, "PATH="), wantPrefix) {
				t.Fatalf("managed helper path not first: %q", entry)
			}
		}
	}
	if !found {
		t.Fatal("RuntimeEnv did not emit PATH")
	}
}
