package previewproof

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rclone-nexus/internal/paths"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	return paths.Paths{StateDir: root, RunDir: filepath.Join(root, "run")}.Normalize()
}

func TestProofIsBoundAndOneUse(t *testing.T) {
	p := testPaths(t)
	proof, err := Issue(p, "config", 7, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if proof.Token == "" || proof.ExpiresUnixMS <= proof.IssuedUnixMS {
		t.Fatalf("bad proof: %+v", proof)
	}
	if err := Consume(p, proof.Token, "config", 7, "abc"); err != nil {
		t.Fatal(err)
	}
	if code := Code(Consume(p, proof.Token, "config", 7, "abc")); code != "preview_required" {
		t.Fatalf("expected consumed proof rejection, got %q", code)
	}
}

func TestMismatchConsumesProof(t *testing.T) {
	p := testPaths(t)
	proof, err := Issue(p, "config", 2, "digest")
	if err != nil {
		t.Fatal(err)
	}
	if code := Code(Consume(p, proof.Token, "config", 3, "digest")); code != "preview_mismatch" {
		t.Fatalf("unexpected code %q", code)
	}
	if code := Code(Consume(p, proof.Token, "config", 2, "digest")); code != "preview_required" {
		t.Fatalf("mismatched proof must be consumed, got %q", code)
	}
}

func TestExpiredProofFailsClosed(t *testing.T) {
	p := testPaths(t)
	proof, err := Issue(p, "config", 1, "digest")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := pathFor(p, proof.Token)
	proof.ExpiresUnixMS = time.Now().Add(-time.Second).UnixMilli()
	payload, _ := json.Marshal(proof)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Code(Consume(p, proof.Token, "config", 1, "digest")); code != "preview_expired" {
		t.Fatalf("unexpected code %q", code)
	}
}
