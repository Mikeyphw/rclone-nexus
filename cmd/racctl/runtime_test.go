package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeRuntimeTestExec(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimeTestConfig(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[managed]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeStatusCLIUsesStaticBundledAuthorityAndIgnoresPoison(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	module := filepath.Join(root, "module")
	bundledBin := filepath.Join(module, "system", "bin", "rclone")
	managedConfig := filepath.Join(state, "config", "rclone", "rclone.conf")
	writeRuntimeTestExec(t, bundledBin)
	writeRuntimeTestConfig(t, managedConfig)
	poison := filepath.Join(root, "poison.conf")
	writeRuntimeTestConfig(t, poison)
	poisonPath := filepath.Join(root, "poison-bin")
	writeRuntimeTestExec(t, filepath.Join(poisonPath, "rclone"))

	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	t.Setenv("RNEXUS_MODULE_DIR", module)
	t.Setenv("RNEXUS_STATE_DIR", state)
	t.Setenv("RNEXUS_MANAGED_RCLONE_CONFIG", managedConfig)
	t.Setenv("RNEXUS_PROVIDER_MODULE_DIR", filepath.Join(root, "provider-removed"))
	t.Setenv("RNEXUS_RCLONE_BIN", filepath.Join(poisonPath, "rclone"))
	t.Setenv("RNEXUS_EXTERNAL_RCLONE_BIN", filepath.Join(poisonPath, "rclone"))
	t.Setenv("RCLONE_CONFIG", poison)
	t.Setenv("PATH", poisonPath)

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
		Source      string `json:"source"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != "managed" || !got.Canonical || !got.Operational || got.Source != "nexus-bundled-static" {
		t.Fatalf("unexpected CLI runtime status: %+v", got)
	}
	if got.Binary != bundledBin || got.Config != managedConfig {
		t.Fatalf("CLI was redirected: %+v", got)
	}
}

func TestRuntimeExecutableAndConfigCommandsProjectStaticResolver(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	module := filepath.Join(root, "module")
	bundledBin := filepath.Join(module, "system", "bin", "rclone")
	managedConfig := filepath.Join(state, "config", "rclone", "rclone.conf")
	writeRuntimeTestExec(t, bundledBin)
	writeRuntimeTestConfig(t, managedConfig)
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	t.Setenv("RNEXUS_MODULE_DIR", module)
	t.Setenv("RNEXUS_STATE_DIR", state)
	t.Setenv("RNEXUS_MANAGED_RCLONE_CONFIG", managedConfig)
	t.Setenv("RNEXUS_PROVIDER_MODULE_DIR", filepath.Join(root, "provider-removed"))

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"runtime", "executable"}, bundledBin + "\n"},
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

func TestRuntimeStatusCLIRequiresBundledRuntime(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	module := filepath.Join(root, "module")
	managedConfig := filepath.Join(state, "config", "rclone", "rclone.conf")
	writeRuntimeTestConfig(t, managedConfig)
	t.Setenv("RNEXUS_RUNTIME_MODE", "")
	t.Setenv("RNEXUS_MODULE_DIR", module)
	t.Setenv("RNEXUS_STATE_DIR", state)
	t.Setenv("RNEXUS_MANAGED_RCLONE_CONFIG", managedConfig)
	t.Setenv("RNEXUS_PROVIDER_MODULE_DIR", filepath.Join(root, "provider"))
	var stdout, stderr bytes.Buffer
	if err := run([]string{"runtime", "status", "--json", "--require-operational"}, &stdout, &stderr); err == nil {
		t.Fatalf("missing bundled runtime must fail --require-operational: %s", stdout.String())
	}
}

func TestRuntimeCLIRejectsExternalCompatibilityMode(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	state := filepath.Join(root, "state")
	writeRuntimeTestExec(t, filepath.Join(module, "system", "bin", "rclone"))
	writeRuntimeTestConfig(t, filepath.Join(state, "config", "rclone", "rclone.conf"))
	t.Setenv("RNEXUS_RUNTIME_MODE", "external")
	t.Setenv("RNEXUS_MODULE_DIR", module)
	t.Setenv("RNEXUS_STATE_DIR", state)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"runtime", "status"}, &stdout, &stderr); err == nil {
		t.Fatal("external compatibility mode must be rejected in static runtime builds")
	}
}
