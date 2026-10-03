package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/runtimestore"
)

func TestRuntimeImportCLIStoresEvidenceAndFailsClosed(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	source := filepath.Join(root, "truncated-rclone")
	if err := os.WriteFile(source, []byte{0x7f, 'E', 'L', 'F', 2, 1}, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_STATE_DIR", state)
	t.Setenv("RNEXUS_RUNTIME_STORE_DIR", filepath.Join(state, "runtimes"))
	t.Setenv("RNEXUS_PROVIDER_MODULE_DIR", filepath.Join(root, "provider-absent"))

	var stdout, stderr bytes.Buffer
	err := run([]string{"runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", source}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("truncated ELF must fail qualification: %s", stdout.String())
	}
	var manifest runtimestore.Manifest
	if jsonErr := json.Unmarshal(stdout.Bytes(), &manifest); jsonErr != nil {
		t.Fatalf("import did not emit inspectable manifest before failing closed: %v output=%q", jsonErr, stdout.String())
	}
	if manifest.RuntimeID == "" || manifest.Qualification.Qualified {
		t.Fatalf("unexpected import result: %+v", manifest)
	}
	if _, statErr := os.Stat(filepath.Join(state, "runtimes", manifest.RuntimeID, "manifest.json")); statErr != nil {
		t.Fatalf("durable manifest missing: %v", statErr)
	}

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"runtime", "inspect", manifest.RuntimeID}, &stdout, &stderr); err != nil {
		t.Fatalf("inspect failed: %v stderr=%q", err, stderr.String())
	}
	var inspected runtimestore.Manifest
	if err := json.Unmarshal(stdout.Bytes(), &inspected); err != nil || inspected.BinarySHA256 != manifest.BinarySHA256 {
		t.Fatalf("inspect did not resolve actual stored candidate: err=%v %+v", err, inspected)
	}

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"runtime", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("list failed: %v stderr=%q", err, stderr.String())
	}
	var listed []runtimestore.Manifest
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].RuntimeID != manifest.RuntimeID {
		t.Fatalf("list did not project runtime store: err=%v %+v", err, listed)
	}
}

func TestRuntimeImportCLIRequiresExplicitSource(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"runtime", "import", "--engine", "rclone"}, &stdout, &stderr); err == nil {
		t.Fatal("runtime import accepted missing --source")
	}
}
