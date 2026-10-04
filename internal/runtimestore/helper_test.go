package runtimestore

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func writeZip(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, payload := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractFuseHelperSelectsCanonicalSidecar(t *testing.T) {
	zipPath := writeZip(t, map[string][]byte{
		"system/vendor/bin/rclone":      []byte("rclone"),
		"system/vendor/bin/fusermount3": []byte("helper-bytes"),
	})
	out := filepath.Join(t.TempDir(), "fusermount3")
	if err := extractFuseHelper(zipPath, out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("helper-bytes")) {
		t.Fatalf("unexpected helper bytes: %q", got)
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("helper not executable: %v %v", info, err)
	}
}

func TestExtractFuseHelperRejectsAmbiguousArchive(t *testing.T) {
	zipPath := writeZip(t, map[string][]byte{
		"system/bin/fusermount3": []byte("one"),
		"vendor/bin/fusermount3": []byte("two"),
	})
	if err := extractFuseHelper(zipPath, filepath.Join(t.TempDir(), "fusermount3")); err == nil {
		t.Fatal("ambiguous helper archive accepted")
	}
}

func TestInspectFuseHelperBindsManifestToManagedBytes(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{StateDir: root}.Normalize()
	objectDir := filepath.Join(p.FuseHelperDir, "objects", "fixture")
	if err := os.MkdirAll(objectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(objectDir, "fusermount3")
	if err := os.WriteFile(bin, payload, 0o500); err != nil {
		t.Fatal(err)
	}
	digest, err := hashFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	elfMeta, err := inspectELF(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("objects", "fixture"), filepath.Join(p.FuseHelperDir, "current")); err != nil {
		t.Fatal(err)
	}
	manifest := FuseHelperManifest{
		SchemaVersion: FuseHelperSchemaVersion,
		Repository:    newFutureRepository,
		ReleaseID:     1,
		ReleaseTag:    "vfixture",
		AssetID:       2,
		AssetName:     newFutureHelperAsset,
		AssetURL:      "https://api.github.com/repos/NewFuture/rclone-fuse3-magisk/releases/assets/2",
		ArchiveSHA256: string(bytes.Repeat([]byte{'a'}, 64)),
		HelperSHA256:  digest,
		ELF:           elfMeta,
	}
	if err := os.MkdirAll(p.FuseHelperDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFuseHelperManifest(p, manifest); err != nil {
		t.Fatal(err)
	}
	got, err := InspectFuseHelper(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Repository != newFutureRepository || got.HelperSHA256 != digest {
		t.Fatalf("unexpected manifest: %+v", got)
	}
	// The managed helper is intentionally read/execute-only (0500). To simulate
	// post-publication tampering without relying on root bypass semantics, make
	// the fixture owner-writable only for the mutation, then restore its managed
	// mode before inspecting it. This mirrors an out-of-band byte change while
	// remaining portable to unprivileged Termux test execution.
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, append(payload, 'x'), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bin, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectFuseHelper(p); err == nil {
		t.Fatal("mutated helper bytes accepted")
	}
}
