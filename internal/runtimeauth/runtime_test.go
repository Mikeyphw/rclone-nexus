package runtimeauth

import (
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[x]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func managedFixture(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{
		StateDir:            filepath.Join(root, "state"),
		ProviderModuleDir:   filepath.Join(root, "provider-missing"),
		ManagedRcloneBin:    filepath.Join(root, "state", "runtime", "active", "bin", "rclone"),
		ManagedRcloneConfig: filepath.Join(root, "state", "config", "rclone", "rclone.conf"),
	}.Normalize()
	writeExec(t, p.ManagedRcloneBin)
	writeConfig(t, p.ManagedRcloneConfig)
	return p
}

func TestManagedProviderRemovedIsOperational(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	p := managedFixture(t)
	got, err := RequireOperational(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Canonical || !got.Operational || got.Binary != p.ManagedRcloneBin || got.Config != p.ManagedRcloneConfig {
		t.Fatalf("unexpected managed resolution: %+v", got)
	}
}

func TestManagedIgnoresPoisonedPathAndGenericConfig(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	p := managedFixture(t)
	poison := t.TempDir()
	writeExec(t, filepath.Join(poison, "rclone"))
	poisonConfig := filepath.Join(poison, "rclone.conf")
	writeConfig(t, poisonConfig)
	t.Setenv("PATH", poison)
	t.Setenv("RCLONE_CONFIG", poisonConfig)
	// The legacy Paths field is also deliberately poisoned: managed mode must
	// remain pinned to ManagedRcloneConfig.
	p.RcloneConfig = poisonConfig
	got, err := RequireOperational(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Binary != p.ManagedRcloneBin {
		t.Fatalf("PATH redirected managed binary: %q", got.Binary)
	}
	if got.Config != p.ManagedRcloneConfig {
		t.Fatalf("generic config redirected managed config: %q", got.Config)
	}
}

func TestManagedMissingConfigIsNotOperational(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	root := t.TempDir()
	p := paths.Paths{
		StateDir:            filepath.Join(root, "state"),
		ProviderModuleDir:   filepath.Join(root, "provider-missing"),
		ManagedRcloneBin:    filepath.Join(root, "state", "runtime", "active", "bin", "rclone"),
		ManagedRcloneConfig: filepath.Join(root, "state", "config", "rclone", "missing.conf"),
	}.Normalize()
	writeExec(t, p.ManagedRcloneBin)
	got, err := Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Operational {
		t.Fatalf("missing config must not report operational: %+v", got)
	}
	if binary, err := Executable(p); err != nil || binary != p.ManagedRcloneBin {
		t.Fatalf("binary projection should remain truthful independently of config: binary=%q err=%v", binary, err)
	}
	if _, err := RequireOperational(p); err == nil {
		t.Fatal("missing config must fail operational qualification")
	}
}

func TestExternalMissingExecutableFailsClosed(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "external")
	t.Setenv("RNEXUS_EXTERNAL_RCLONE_BIN", filepath.Join(t.TempDir(), "missing-rclone"))
	t.Setenv("PATH", t.TempDir())
	p := paths.Paths{ProviderModuleDir: filepath.Join(t.TempDir(), "provider")}.Normalize()
	if _, err := RequireOperational(p); err == nil {
		t.Fatal("expected external mode to fail without executable")
	}
}

func TestExternalCompatibilityProviderOutranksPath(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "external")
	t.Setenv("RNEXUS_EXTERNAL_RCLONE_BIN", "")
	t.Setenv("RNEXUS_RCLONE_BIN", "")
	root := t.TempDir()
	providerDir := filepath.Join(root, "provider")
	providerBin := filepath.Join(providerDir, "system", "vendor", "bin", "rclone")
	writeExec(t, providerBin)
	writeConfig(t, filepath.Join(providerDir, "conf", "rclone.conf"))
	host := filepath.Join(root, "host")
	writeExec(t, filepath.Join(host, "rclone"))
	t.Setenv("PATH", host)
	got, err := Resolve(paths.Paths{ProviderModuleDir: providerDir}.Normalize())
	if err != nil {
		t.Fatal(err)
	}
	if got.Binary != providerBin {
		t.Fatalf("provider compatibility binary=%q want %q", got.Binary, providerBin)
	}
}

func TestLegacyProviderWithoutManagedRuntimeRequiresMigration(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "")
	t.Setenv("RNEXUS_EXTERNAL_RCLONE_BIN", "")
	t.Setenv("RNEXUS_RCLONE_BIN", "")
	providerDir := filepath.Join(t.TempDir(), "provider")
	if err := os.MkdirAll(providerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(paths.Paths{ProviderModuleDir: providerDir, StateDir: filepath.Join(t.TempDir(), "state")}.Normalize())
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != ModeMigrationRequired || got.Operational {
		t.Fatalf("unexpected resolution: %+v", got)
	}
}

func TestManagedAndEnabledLegacyProviderIsAmbiguous(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	p := managedFixture(t)
	if err := os.MkdirAll(p.ProviderModuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AmbiguousAuthority || got.Operational {
		t.Fatalf("enabled parent provider was not rejected: %+v", got)
	}
	if _, err := RequireOperational(p); err == nil {
		t.Fatal("expected ambiguity to fail closed")
	}
}

func TestDisabledLegacyProviderDoesNotBlockManaged(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	p := managedFixture(t)
	if err := os.MkdirAll(p.ProviderModuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ProviderModuleDir, "disable"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := RequireOperational(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.AmbiguousAuthority || !got.Operational {
		t.Fatalf("disabled legacy provider should be observation-only: %+v", got)
	}
}
