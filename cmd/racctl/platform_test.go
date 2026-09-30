package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/paths"
)

func TestVerifyIntegrityFailsClosedWhenManifestMissing(t *testing.T) {
	base := t.TempDir()
	p := paths.Paths{
		StateDir:  filepath.Join(base, "state"),
		ModuleDir: filepath.Join(base, "module"),
	}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	engine := control.New(p)
	var stdout, stderr bytes.Buffer
	err := compatPlatform(context.Background(), p, engine, []string{"verify-integrity"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("missing integrity manifest must fail closed; output=%q", stdout.String())
	}
}
