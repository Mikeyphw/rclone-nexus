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

func staticFixture(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{
		ModuleDir:           filepath.Join(root, "module"),
		StateDir:            filepath.Join(root, "state"),
		ProviderModuleDir:   filepath.Join(root, "provider-missing"),
		ManagedRcloneConfig: filepath.Join(root, "state", "config", "rclone", "rclone.conf"),
	}.Normalize()
	writeExec(t, filepath.Join(p.ModuleDir, "system", "bin", "rclone"))
	writeConfig(t, p.ManagedRcloneConfig)
	return p
}

func TestStaticBundledRuntimeIsOperational(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "")
	p := staticFixture(t)
	got, err := RequireOperational(p)
	if err != nil {
		t.Fatal(err)
	}
	wantBin := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
	if !got.Canonical || !got.Operational || got.Mode != ModeManaged || got.Source != "nexus-bundled-static" || got.Binary != wantBin || got.Config != p.ManagedRcloneConfig {
		t.Fatalf("unexpected static resolution: %+v", got)
	}
}

func TestStaticRuntimeRejectsExternalMode(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "external")
	p := staticFixture(t)
	if _, err := Resolve(p); err == nil {
		t.Fatal("external compatibility mode must not resolve in static-runtime builds")
	}
}

func TestStaticRuntimeIgnoresEnvPathAndGenericConfig(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	p := staticFixture(t)
	poison := t.TempDir()
	writeExec(t, filepath.Join(poison, "rclone"))
	poisonConfig := filepath.Join(poison, "rclone.conf")
	writeConfig(t, poisonConfig)
	t.Setenv("PATH", poison)
	t.Setenv("RNEXUS_RCLONE_BIN", filepath.Join(poison, "rclone"))
	t.Setenv("RNEXUS_EXTERNAL_RCLONE_BIN", filepath.Join(poison, "rclone"))
	t.Setenv("RCLONE_CONFIG", poisonConfig)
	p.RcloneConfig = poisonConfig
	got, err := RequireOperational(p)
	if err != nil {
		t.Fatal(err)
	}
	wantBin := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
	if got.Binary != wantBin {
		t.Fatalf("environment redirected static runtime binary: %q", got.Binary)
	}
	if got.Config != p.ManagedRcloneConfig {
		t.Fatalf("generic config redirected managed config: %q", got.Config)
	}
}

func TestStaticRuntimeMissingBundledBinaryFails(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	root := t.TempDir()
	p := paths.Paths{
		ModuleDir:           filepath.Join(root, "module"),
		StateDir:            filepath.Join(root, "state"),
		ProviderModuleDir:   filepath.Join(root, "provider-missing"),
		ManagedRcloneConfig: filepath.Join(root, "state", "config", "rclone", "rclone.conf"),
	}.Normalize()
	writeConfig(t, p.ManagedRcloneConfig)
	got, err := Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Operational {
		t.Fatalf("missing bundled runtime must not report operational: %+v", got)
	}
	if _, err := RequireOperational(p); err == nil {
		t.Fatal("missing bundled runtime must fail operational qualification")
	}
}

func TestStaticRuntimeMissingConfigIsNotOperational(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	root := t.TempDir()
	p := paths.Paths{
		ModuleDir:           filepath.Join(root, "module"),
		StateDir:            filepath.Join(root, "state"),
		ProviderModuleDir:   filepath.Join(root, "provider-missing"),
		ManagedRcloneConfig: filepath.Join(root, "state", "config", "rclone", "missing.conf"),
	}.Normalize()
	writeExec(t, filepath.Join(p.ModuleDir, "system", "bin", "rclone"))
	got, err := Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Operational {
		t.Fatalf("missing config must not report operational: %+v", got)
	}
	if binary, err := Executable(p); err != nil || binary != filepath.Join(p.ModuleDir, "system", "bin", "rclone") {
		t.Fatalf("binary should remain truthful independently of config: binary=%q err=%v", binary, err)
	}
	if _, err := RequireOperational(p); err == nil {
		t.Fatal("missing config must fail operational qualification")
	}
}

func TestStaticRuntimeIgnoresLegacyProviderAuthority(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	p := staticFixture(t)
	providerBin := filepath.Join(p.ProviderModuleDir, "system", "vendor", "bin", "rclone")
	writeExec(t, providerBin)
	writeConfig(t, filepath.Join(p.ProviderModuleDir, "conf", "rclone.conf"))
	got, err := RequireOperational(p)
	if err != nil {
		t.Fatal(err)
	}
	wantBin := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
	if got.Binary != wantBin || got.AmbiguousAuthority || !got.LegacyProviderPresent || !got.LegacyProviderEnabled {
		t.Fatalf("legacy provider affected static runtime authority: %+v", got)
	}
}

func TestStaticRuntimeIgnoresLegacyRuntimeStateFiles(t *testing.T) {
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	p := staticFixture(t)
	legacy := filepath.Join(p.RuntimeDir, "activation-v1.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"phase":"QUIESCING","active_runtime_id":"retired"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := RequireOperational(p)
	if err != nil {
		t.Fatal(err)
	}
	wantBin := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
	if got.Binary != wantBin || got.Source != "nexus-bundled-static" || got.ActivationPhase != "" || got.ActiveRuntimeID != "" {
		t.Fatalf("legacy activation file unexpectedly affected static execution: %+v", got)
	}
}
