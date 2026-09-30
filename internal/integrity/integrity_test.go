package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	// File creation mode is subject to the host umask (Termux commonly differs
	// from desktop hosts), so set the fixture mode explicitly before asserting
	// integrity semantics.
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	m := Manifest{SchemaVersion: 1, Entries: []Entry{{Path: "x", SHA256: hex.EncodeToString(sum[:]), Mode: 0o755, Size: 5}}}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "integrity.manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	r := Verify(dir)
	if !r.OK || r.Checked != 1 {
		t.Fatalf("unexpected: %+v", r)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := Verify(dir); r.OK || !hasIssue(r.Issues, "mode_mismatch:x") {
		t.Fatalf("mode mismatch must fail explicitly: %+v", r)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("tamper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := Verify(dir); r.OK || !hasIssue(r.Issues, "hash_mismatch:x") {
		t.Fatalf("tamper must fail explicitly: %+v", r)
	}
}

func hasIssue(issues []string, want string) bool {
	for _, issue := range issues {
		if strings.EqualFold(issue, want) {
			return true
		}
	}
	return false
}
