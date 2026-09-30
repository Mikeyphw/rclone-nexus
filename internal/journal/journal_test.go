package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(root, "state"), ModuleDir: filepath.Join(root, "module"), ProviderModuleDir: filepath.Join(root, "provider"), RcloneConfig: filepath.Join(root, "provider", "conf", "rclone.conf"), FuseDevice: "/dev/null"}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJournalPersistsRedactedEventsAndTerminalState(t *testing.T) {
	p := testPaths(t)
	if _, err := Begin(p, "req-1", "mount.start", "run", true); err != nil {
		t.Fatal(err)
	}
	if err := Append(p, "req-1", "progress", "working", map[string]any{"access_token": "secret", "name": "drive"}); err != nil {
		t.Fatal(err)
	}
	if err := Complete(p, "req-1", StateSucceeded, map[string]any{"state": "started"}, nil); err != nil {
		t.Fatal(err)
	}
	r, err := Get(p, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateSucceeded || len(r.Events) != 1 {
		t.Fatalf("unexpected record: %+v", r)
	}
	payload, _ := json.Marshal(r)
	if strings.Contains(string(payload), "secret") || !strings.Contains(string(payload), "redacted") {
		t.Fatalf("journal redaction failed: %s", payload)
	}
}

func TestJournalRecoversDeadOwnerAsInterrupted(t *testing.T) {
	p := testPaths(t)
	if _, err := Begin(p, "req-orphan", "mount.reconcile", "reconcile", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.OperationsDir, "req-orphan.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	r.OwnerPID = 99999999
	if err := atomicWrite(path, r); err != nil {
		t.Fatal(err)
	}
	recovered, err := Get(p, "req-orphan")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != StateInterrupted || recovered.Error == nil || recovered.Error.Code != "operation_interrupted" {
		t.Fatalf("unexpected recovery: %+v", recovered)
	}
}

func TestJournalRejectsRequestIDReuse(t *testing.T) {
	p := testPaths(t)
	if _, err := Begin(p, "same-id", "mount.start", "run", true); err != nil {
		t.Fatal(err)
	}
	if _, err := Begin(p, "same-id", "mount.start", "run", true); err == nil {
		t.Fatal("expected duplicate request id rejection")
	}
}

func TestJournalScrubsPrivatePathsFromTerminalErrors(t *testing.T) {
	p := testPaths(t)
	if _, err := Begin(p, "req-private", "mount.start", "run", true); err != nil {
		t.Fatal(err)
	}
	machineErr := protocol.Error("operation_failed", "failed", p.StateDir+"/private/file")
	if err := Complete(p, "req-private", StateFailed, nil, machineErr); err != nil {
		t.Fatal(err)
	}
	r, err := Get(p, "req-private")
	if err != nil {
		t.Fatal(err)
	}
	if r.Error == nil || strings.Contains(r.Error.Detail, p.StateDir) || !strings.Contains(r.Error.Detail, "<private-path>") {
		t.Fatalf("private path not scrubbed: %+v", r.Error)
	}
}
