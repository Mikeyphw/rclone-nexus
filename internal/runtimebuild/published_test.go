package runtimebuild

import (
	"archive/tar"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimesource"
	"strings"
	"testing"
)

func tarBundle(t *testing.T, dir string) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.Name(), Mode: 0600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func publishedPaths(t *testing.T) paths.Paths {
	root := t.TempDir()
	return paths.Paths{StateDir: filepath.Join(root, "state"), RuntimeDir: filepath.Join(root, "state", "runtime"), RuntimeStoreDir: filepath.Join(root, "state", "runtimes")}.Normalize()
}

func TestPublishedBuildBecomesPendingSourceBuildCandidate(t *testing.T) {
	commit := strings.Repeat("a", 40)
	dir := writeFixture(t, nil, minimalAndroidARM64ELF())
	payload := tarBundle(t, dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer srv.Close()
	upstream := runtimesource.Resolution{SourceID: "bclone", Engine: "bclone", Kind: runtimesource.KindGitHub, Repository: "BenjiThatFoxGuy/bclone", CommitSHA: commit, BuildRepository: "Mikeyphw/rclone-nexus", BuildRequired: true}
	m, r, err := AcquirePublishedFromURL(context.Background(), publishedPaths(t), upstream, srv.URL+"/runtime-source-build.tar")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != runtimesource.KindSourceBuild || r.CommitSHA != commit || m.Source.Type != "source-build" {
		t.Fatalf("published build did not converge on source-build authority: r=%+v m=%+v", r, m)
	}
	if m.Qualification.State != "pending" || m.Qualification.Qualified {
		t.Fatalf("published acquisition ran qualifier: %+v", m.Qualification)
	}
}

func TestPublishedBuildResolutionRemainsReplayableAfterDownloadTempRemoved(t *testing.T) {
	commit := strings.Repeat("a", 40)
	dir := writeFixture(t, nil, minimalAndroidARM64ELF())
	payload := tarBundle(t, dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer srv.Close()
	p := publishedPaths(t)
	upstream := runtimesource.Resolution{SourceID: "bclone", Engine: "bclone", Kind: runtimesource.KindGitHub, Repository: "BenjiThatFoxGuy/bclone", CommitSHA: commit, BuildRepository: "Mikeyphw/rclone-nexus", BuildRequired: true}
	_, r, err := AcquirePublishedFromURL(context.Background(), p, upstream, srv.URL+"/runtime-source-build.tar")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Clean(r.Path), filepath.Clean(p.RuntimeSourcesDir)+string(os.PathSeparator)) {
		t.Fatalf("published build resolution escaped durable source state: %q", r.Path)
	}
	if _, err := os.Stat(r.Path); err != nil {
		t.Fatalf("durable source-build bytes disappeared after download cleanup: %v", err)
	}
	if _, err := runtimesource.AcquireResolution(context.Background(), p, r.ResolutionID); err != nil {
		t.Fatalf("persisted source-build resolution is not replayable: %v", err)
	}
}

func TestPublishedBuildProvenanceMustMatchUpstreamResolution(t *testing.T) {
	dir := writeFixture(t, nil, minimalAndroidARM64ELF())
	payload := tarBundle(t, dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer srv.Close()
	upstream := runtimesource.Resolution{SourceID: "bclone", Engine: "bclone", Kind: runtimesource.KindGitHub, Repository: "BenjiThatFoxGuy/bclone", CommitSHA: strings.Repeat("b", 40), BuildRepository: "Mikeyphw/rclone-nexus", BuildRequired: true}
	if _, _, err := AcquirePublishedFromURL(context.Background(), publishedPaths(t), upstream, srv.URL); err == nil || !strings.Contains(err.Error(), "provenance does not match") {
		t.Fatalf("mismatched build accepted: %v", err)
	}
}
