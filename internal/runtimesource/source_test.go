package runtimesource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"rclone-nexus/internal/paths"
)

func sourcePaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	return paths.Paths{StateDir: root}.Normalize()
}

func TestBuiltinsCoverCanonicalFirstClassGitHubSources(t *testing.T) {
	items, err := List(sourcePaths(t))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Spec{}
	for _, s := range items {
		byID[s.ID] = s
	}
	checks := map[string]string{"bclone": "BenjiThatFoxGuy/bclone", "rclone": "rclone/rclone", "newfuture": "NewFuture/rclone-fuse3-magisk"}
	for id, repo := range checks {
		s, ok := byID[id]
		if !ok {
			t.Fatalf("missing builtin %s", id)
		}
		if !s.Builtin || s.Repository != repo || s.DefaultChannel != ChannelLatestStable {
			t.Fatalf("malformed builtin %s: %+v", id, s)
		}
	}
}

func TestCustomRegistryCoversGitHubURLLocalAndSourceBuild(t *testing.T) {
	p := sourcePaths(t)
	local := filepath.Join(t.TempDir(), "rclone")
	if err := os.WriteFile(local, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("url"))
	digest := hex.EncodeToString(sum[:])
	commit := strings.Repeat("a", 40)
	specs := []Spec{
		{ID: "fork", Engine: "rclone", Kind: KindGitHub, Repository: "owner/repo", DefaultChannel: ChannelPinnedRelease, Ref: "v1.2.3", AssetName: "rclone"},
		{ID: "direct", Engine: "rclone", Kind: KindURL, DefaultChannel: ChannelManualOnly, URL: "https://example.invalid/rclone", ExpectedSHA256: digest},
		{ID: "local", Engine: "rclone", Kind: KindLocalBinary, DefaultChannel: ChannelManualOnly, Path: local},
		{ID: "build", Engine: "bclone", Kind: KindSourceBuild, DefaultChannel: ChannelPinnedCommit, Path: local, Repository: "owner/repo", Ref: commit},
	}
	for _, s := range specs {
		if _, err := Register(p, s); err != nil {
			t.Fatalf("register %s: %v", s.ID, err)
		}
	}
	items, err := List(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"fork", "direct", "local", "build"} {
		found := false
		for _, s := range items {
			if s.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing registered source %s", id)
		}
	}
	if _, err := Register(p, Spec{ID: "bclone", Engine: "x", Kind: KindLocalBinary, DefaultChannel: ChannelManualOnly, Path: local}); err == nil {
		t.Fatal("builtin source was shadowed")
	}
	if err := Remove(p, "rclone"); err == nil {
		t.Fatal("builtin source was removed")
	}
}

type ghFixture struct {
	mu               sync.Mutex
	repoFull         string
	latestTag        string
	latestPrerelease bool
	latestDraft      bool
	latestAsset      bool
	tagCommit        map[string]string
	redirectLatest   string
}

func (f *ghFixture) handler(base func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		repo := "BenjiThatFoxGuy/bclone"
		switch {
		case r.URL.Path == "/repos/BenjiThatFoxGuy/bclone":
			json.NewEncoder(w).Encode(map[string]any{"id": int64(42), "full_name": f.repoFull})
		case r.URL.Path == "/repos/BenjiThatFoxGuy/bclone/releases/latest":
			if f.redirectLatest != "" {
				http.Redirect(w, r, f.redirectLatest, http.StatusFound)
				return
			}
			assets := []map[string]any{}
			if f.latestAsset {
				assets = append(assets, map[string]any{"id": int64(77), "name": "rclone-" + f.latestTag + "-linux-arm64.zip", "url": base() + "/repos/" + repo + "/releases/assets/77", "browser_download_url": base() + "/" + repo + "/releases/download/" + f.latestTag + "/rclone-" + f.latestTag + "-linux-arm64.zip", "digest": "sha256:" + strings.Repeat("1", 64), "size": 123})
			}
			json.NewEncoder(w).Encode(map[string]any{"id": int64(9), "tag_name": f.latestTag, "draft": f.latestDraft, "prerelease": f.latestPrerelease, "assets": assets})
		case strings.HasPrefix(r.URL.Path, "/repos/BenjiThatFoxGuy/bclone/releases/tags/"):
			tag := strings.TrimPrefix(r.URL.Path, "/repos/BenjiThatFoxGuy/bclone/releases/tags/")
			assets := []map[string]any{{"id": int64(77), "name": "rclone-" + tag + "-linux-arm64.zip", "url": base() + "/repos/" + repo + "/releases/assets/77", "browser_download_url": base() + "/" + repo + "/releases/download/" + tag + "/rclone-" + tag + "-linux-arm64.zip", "digest": "sha256:" + strings.Repeat("1", 64), "size": 123}}
			json.NewEncoder(w).Encode(map[string]any{"id": int64(10), "tag_name": tag, "draft": false, "prerelease": true, "assets": assets})
		case strings.HasPrefix(r.URL.Path, "/repos/BenjiThatFoxGuy/bclone/git/ref/tags/"):
			tag := strings.TrimPrefix(r.URL.Path, "/repos/BenjiThatFoxGuy/bclone/git/ref/tags/")
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": f.tagCommit[tag], "type": "commit"}})
		case strings.HasPrefix(r.URL.Path, "/repos/BenjiThatFoxGuy/bclone/git/commits/"):
			sha := strings.TrimPrefix(r.URL.Path, "/repos/BenjiThatFoxGuy/bclone/git/commits/")
			json.NewEncoder(w).Encode(map[string]any{"sha": sha})
		default:
			http.NotFound(w, r)
		}
	}
}

func newFixtureResolver(t *testing.T, f *ghFixture) (*GitHubResolver, func()) {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(f.handler(func() string { return server.URL }))
	resolver, err := newTestGitHubResolver(server.Client(), server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return resolver, server.Close
}

func TestLatestStableResolvesToImmutableCommitAndAssetIDAndPersists(t *testing.T) {
	f := &ghFixture{repoFull: "BenjiThatFoxGuy/bclone", latestTag: "v9.1.0", latestAsset: true, tagCommit: map[string]string{"v9.1.0": strings.Repeat("a", 40), "v9.2.0": strings.Repeat("b", 40)}}
	resolver, closeFn := newFixtureResolver(t, f)
	defer closeFn()
	p := sourcePaths(t)
	first, err := Resolve(context.Background(), p, resolver, "bclone", ResolveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if first.ReleaseTag != "v9.1.0" || first.CommitSHA != strings.Repeat("a", 40) || first.Asset == nil || first.Asset.ID != 77 {
		t.Fatalf("resolution not immutable: %+v", first)
	}
	if strings.Contains(first.Asset.APIURL, "/latest") || !strings.Contains(first.Asset.APIURL, "/releases/assets/77") {
		t.Fatalf("asset URL is mutable: %s", first.Asset.APIURL)
	}
	persisted, err := InspectResolution(p, first.ResolutionID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.CommitSHA != first.CommitSHA {
		t.Fatal("persisted resolution changed")
	}
	f.mu.Lock()
	f.latestTag = "v9.2.0"
	f.mu.Unlock()
	second, err := Resolve(context.Background(), p, resolver, "bclone", ResolveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if second.CommitSHA != strings.Repeat("b", 40) || second.ResolutionID == first.ResolutionID {
		t.Fatalf("latest change was not resolved as a new immutable identity: first=%+v second=%+v", first, second)
	}
	persistedAgain, _ := InspectResolution(p, first.ResolutionID)
	if persistedAgain.CommitSHA != strings.Repeat("a", 40) {
		t.Fatal("old latest resolution was rewritten")
	}
}

func TestPinnedReleaseRetargetDoesNotRewritePriorResolution(t *testing.T) {
	f := &ghFixture{repoFull: "BenjiThatFoxGuy/bclone", latestTag: "v1", latestAsset: true, tagCommit: map[string]string{"v1": strings.Repeat("c", 40)}}
	resolver, closeFn := newFixtureResolver(t, f)
	defer closeFn()
	p := sourcePaths(t)
	first, err := Resolve(context.Background(), p, resolver, "bclone", ResolveRequest{Channel: ChannelPinnedRelease, Ref: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.tagCommit["v1"] = strings.Repeat("d", 40)
	f.mu.Unlock()
	second, err := Resolve(context.Background(), p, resolver, "bclone", ResolveRequest{Channel: ChannelPinnedRelease, Ref: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.CommitSHA == second.CommitSHA || first.ResolutionID == second.ResolutionID {
		t.Fatalf("retargeted tag did not produce distinct immutable resolution: %+v %+v", first, second)
	}
	old, _ := InspectResolution(p, first.ResolutionID)
	if old.CommitSHA != strings.Repeat("c", 40) {
		t.Fatal("prior tag resolution was mutated")
	}
}

func TestNegativeGitHubCasesFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ghFixture)
	}{
		{"wrong-owner", func(f *ghFixture) { f.repoFull = "evil/bclone" }},
		{"prerelease", func(f *ghFixture) { f.latestPrerelease = true }},
		{"draft", func(f *ghFixture) { f.latestDraft = true }},
		{"missing-asset", func(f *ghFixture) { f.latestAsset = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &ghFixture{repoFull: "BenjiThatFoxGuy/bclone", latestTag: "v1", latestAsset: true, tagCommit: map[string]string{"v1": strings.Repeat("a", 40)}}
			tc.mutate(f)
			resolver, closeFn := newFixtureResolver(t, f)
			defer closeFn()
			if _, err := Resolve(context.Background(), sourcePaths(t), resolver, "bclone", ResolveRequest{}); err == nil {
				t.Fatalf("negative case %s accepted", tc.name)
			}
		})
	}
}

func TestPoisonedMetadataRedirectIsRejected(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "{}") }))
	defer evil.Close()
	f := &ghFixture{repoFull: "BenjiThatFoxGuy/bclone", latestTag: "v1", latestAsset: true, tagCommit: map[string]string{"v1": strings.Repeat("a", 40)}, redirectLatest: evil.URL + "/steal"}
	resolver, closeFn := newFixtureResolver(t, f)
	defer closeFn()
	if _, err := Resolve(context.Background(), sourcePaths(t), resolver, "bclone", ResolveRequest{}); err == nil || !strings.Contains(err.Error(), "trusted API origin") {
		t.Fatalf("poisoned redirect not rejected: %v", err)
	}
}

func TestPinnedCommitRequiresFullImmutableSHA(t *testing.T) {
	f := &ghFixture{repoFull: "BenjiThatFoxGuy/bclone", tagCommit: map[string]string{}}
	resolver, closeFn := newFixtureResolver(t, f)
	defer closeFn()
	p := sourcePaths(t)
	if _, err := Resolve(context.Background(), p, resolver, "bclone", ResolveRequest{Channel: ChannelPinnedCommit, Ref: "main"}); err == nil {
		t.Fatal("mutable branch accepted as pinned commit")
	}
	sha := strings.Repeat("e", 40)
	r, err := Resolve(context.Background(), p, resolver, "bclone", ResolveRequest{Channel: ChannelPinnedCommit, Ref: sha})
	if err != nil {
		t.Fatal(err)
	}
	if r.CommitSHA != sha || r.Asset != nil {
		t.Fatalf("pinned commit malformed: %+v", r)
	}
}

func TestManualLocalURLAndSourceBuildResolutionsBindBytes(t *testing.T) {
	p := sourcePaths(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "rclone")
	data := []byte("runtime-bytes")
	if err := os.WriteFile(file, data, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	for _, spec := range []Spec{
		{ID: "local", Engine: "rclone", Kind: KindLocalBinary, DefaultChannel: ChannelManualOnly, Path: file},
		{ID: "url", Engine: "rclone", Kind: KindURL, DefaultChannel: ChannelManualOnly, URL: "https://example.invalid/rclone", ExpectedSHA256: digest},
		{ID: "build", Engine: "bclone", Kind: KindSourceBuild, DefaultChannel: ChannelPinnedCommit, Path: file, Repository: "owner/repo", Ref: strings.Repeat("f", 40)},
	} {
		if _, err := Register(p, spec); err != nil {
			t.Fatal(err)
		}
	}
	local, _ := Resolve(context.Background(), p, nil, "local", ResolveRequest{})
	if local.ContentSHA256 != digest {
		t.Fatalf("local hash mismatch: %+v", local)
	}
	direct, _ := Resolve(context.Background(), p, nil, "url", ResolveRequest{})
	if direct.ContentSHA256 != digest || direct.URL == "" {
		t.Fatalf("url resolution not digest-bound: %+v", direct)
	}
	build, _ := Resolve(context.Background(), p, nil, "build", ResolveRequest{})
	if build.ContentSHA256 != digest || build.CommitSHA != strings.Repeat("f", 40) {
		t.Fatalf("build resolution not commit+byte bound: %+v", build)
	}
}

func TestImportRequestUsesImmutableAssetAPIURLNotLatestOrTagURL(t *testing.T) {
	r := Resolution{SchemaVersion: 1, SourceID: "bclone", SpecDigest: strings.Repeat("a", 64), Engine: "bclone", Kind: KindGitHub, Channel: ChannelLatestStable, Repository: "BenjiThatFoxGuy/bclone", RepositoryID: 42, ReleaseID: 9, ReleaseTag: "v1", CommitSHA: strings.Repeat("b", 40), Asset: &Asset{ID: 77, Name: "rclone-v1-linux-arm64.zip", APIURL: "https://api.github.com/repos/BenjiThatFoxGuy/bclone/releases/assets/77", BrowserDownloadURL: "https://github.com/BenjiThatFoxGuy/bclone/releases/download/v1/x", Digest: "sha256:" + strings.Repeat("1", 64)}}
	r.ResolutionID = resolutionIdentity(r)
	req, err := ImportRequestForResolution(r)
	if err != nil {
		t.Fatal(err)
	}
	if req.AssetID != 77 || req.ReleaseID != 9 || req.ResolvedRef != strings.Repeat("b", 40) || strings.Contains(req.AssetURL, "latest") || strings.Contains(req.AssetURL, "/download/v1/") {
		t.Fatalf("import request reintroduced mutable source: %+v", req)
	}
	if req.ExpectedSHA256 != strings.Repeat("1", 64) {
		t.Fatalf("GitHub digest not propagated: %+v", req)
	}
}
