package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeStatusCLIUsesManagedAuthorityAndIgnoresPoison(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	managedBin := filepath.Join(state, "runtime", "active", "bin", "rclone")
	managedConfig := filepath.Join(state, "config", "rclone", "rclone.conf")
	if err := os.MkdirAll(filepath.Dir(managedBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(managedConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedConfig, []byte("[managed]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	poison := filepath.Join(root, "poison.conf")
	if err := os.WriteFile(poison, []byte("[poison]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	t.Setenv("RNEXUS_STATE_DIR", state)
	t.Setenv("RNEXUS_MANAGED_RCLONE_BIN", managedBin)
	t.Setenv("RNEXUS_MANAGED_RCLONE_CONFIG", managedConfig)
	t.Setenv("RNEXUS_PROVIDER_MODULE_DIR", filepath.Join(root, "provider-removed"))
	t.Setenv("RCLONE_CONFIG", poison)
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	if err := run([]string{"runtime", "status", "--json", "--require-operational"}, &stdout, &stderr); err != nil {
		t.Fatalf("runtime status failed: %v stderr=%q", err, stderr.String())
	}
	var got struct {
		Mode        string `json:"mode"`
		Canonical   bool   `json:"canonical"`
		Operational bool   `json:"operational"`
		Binary      string `json:"binary"`
		Config      string `json:"config"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != "managed" || !got.Canonical || !got.Operational {
		t.Fatalf("unexpected CLI runtime status: %+v", got)
	}
	if got.Binary != managedBin || got.Config != managedConfig {
		t.Fatalf("CLI was redirected: %+v", got)
	}
}

func TestRuntimeExecutableAndConfigCommandsProjectCanonicalResolver(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	managedBin := filepath.Join(state, "runtime", "active", "bin", "rclone")
	managedConfig := filepath.Join(state, "config", "rclone", "rclone.conf")
	if err := os.MkdirAll(filepath.Dir(managedBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(managedConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedConfig, []byte("[managed]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	t.Setenv("RNEXUS_STATE_DIR", state)
	t.Setenv("RNEXUS_MANAGED_RCLONE_BIN", managedBin)
	t.Setenv("RNEXUS_MANAGED_RCLONE_CONFIG", managedConfig)
	t.Setenv("RNEXUS_PROVIDER_MODULE_DIR", filepath.Join(root, "provider-removed"))

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"runtime", "executable"}, managedBin + "\n"},
		{[]string{"runtime", "config"}, managedConfig + "\n"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(tc.args, &stdout, &stderr); err != nil {
			t.Fatalf("%v failed: %v stderr=%q", tc.args, err, stderr.String())
		}
		if stdout.String() != tc.want {
			t.Fatalf("%v output=%q want %q", tc.args, stdout.String(), tc.want)
		}
	}
}

func TestRuntimeStatusCLIRequiresMigrationForLegacyOnlyState(t *testing.T) {
	root := t.TempDir()
	provider := filepath.Join(root, "provider")
	if err := os.MkdirAll(provider, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RUNTIME_MODE", "")
	t.Setenv("RNEXUS_EXTERNAL_RCLONE_BIN", "")
	t.Setenv("RNEXUS_RCLONE_BIN", "")
	t.Setenv("RNEXUS_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("RNEXUS_PROVIDER_MODULE_DIR", provider)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"runtime", "status", "--json", "--require-operational"}, &stdout, &stderr); err == nil {
		t.Fatalf("legacy-only state must fail --require-operational: %s", stdout.String())
	}
}
