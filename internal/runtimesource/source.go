package runtimesource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimestore"
)

const (
	RegistrySchemaVersion   = 1
	ResolutionSchemaVersion = 1
)

type Kind string

type Channel string

const (
	KindGitHub      Kind = "github-release"
	KindURL         Kind = "url"
	KindLocalBinary Kind = "local-binary"
	KindSourceBuild Kind = "source-build"
	KindNewFuture   Kind = "newfuture-derived"

	ChannelLatestStable  Channel = "latest-stable"
	ChannelPinnedRelease Channel = "pinned-release"
	ChannelPinnedCommit  Channel = "pinned-commit"
	ChannelManualOnly    Channel = "manual-only"
)

type Spec struct {
	ID             string  `json:"id"`
	Engine         string  `json:"engine"`
	Kind           Kind    `json:"kind"`
	Repository     string  `json:"repository,omitempty"`
	DefaultChannel Channel `json:"default_channel"`
	Ref            string  `json:"ref,omitempty"`
	AssetName      string  `json:"asset_name,omitempty"`
	AssetPattern   string  `json:"asset_pattern,omitempty"`
	URL            string  `json:"url,omitempty"`
	Path           string  `json:"path,omitempty"`
	ExpectedSHA256 string  `json:"expected_sha256,omitempty"`
	Builtin        bool    `json:"builtin,omitempty"`
}

type Registry struct {
	SchemaVersion int    `json:"schema_version"`
	Revision      uint64 `json:"revision"`
	Sources       []Spec `json:"sources"`
}

type Asset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	APIURL             string `json:"api_url"`
	BrowserDownloadURL string `json:"browser_download_url,omitempty"`
	Digest             string `json:"digest,omitempty"`
	Size               int64  `json:"size,omitempty"`
}

type Resolution struct {
	SchemaVersion    int     `json:"schema_version"`
	ResolutionID     string  `json:"resolution_id"`
	SourceID         string  `json:"source_id"`
	SpecDigest       string  `json:"spec_digest"`
	RegistryRevision uint64  `json:"registry_revision"`
	Engine           string  `json:"engine"`
	Kind             Kind    `json:"kind"`
	Channel          Channel `json:"channel"`
	Repository       string  `json:"repository,omitempty"`
	RepositoryID     int64   `json:"repository_id,omitempty"`
	RequestedRef     string  `json:"requested_ref,omitempty"`
	ReleaseID        int64   `json:"release_id,omitempty"`
	ReleaseTag       string  `json:"release_tag,omitempty"`
	CommitSHA        string  `json:"commit_sha,omitempty"`
	Asset            *Asset  `json:"asset,omitempty"`
	URL              string  `json:"url,omitempty"`
	Path             string  `json:"path,omitempty"`
	ContentSHA256    string  `json:"content_sha256,omitempty"`
	Size             int64   `json:"size,omitempty"`
	ResolvedUnixMS   int64   `json:"resolved_unix_ms"`
}

type ResolveRequest struct {
	Channel        Channel
	Ref            string
	AssetName      string
	AssetPattern   string
	ExpectedSHA256 string
}

func builtinSpecs() []Spec {
	return []Spec{
		{ID: "bclone", Engine: "bclone", Kind: KindGitHub, Repository: "BenjiThatFoxGuy/bclone", DefaultChannel: ChannelLatestStable, AssetPattern: "*linux-arm64.zip", Builtin: true},
		{ID: "rclone", Engine: "rclone", Kind: KindGitHub, Repository: "rclone/rclone", DefaultChannel: ChannelLatestStable, AssetPattern: "*linux-arm64.zip", Builtin: true},
		{ID: "newfuture", Engine: "rclone", Kind: KindNewFuture, Repository: "NewFuture/rclone-fuse3-magisk", DefaultChannel: ChannelLatestStable, AssetName: "magisk-rclone_arm64-v8a.zip", Builtin: true},
	}
}

func validID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validEngine(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func validRepository(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		if len(part) > 100 || strings.ContainsAny(part, "\\\x00\r\n") {
			return false
		}
		for _, r := range part {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
				continue
			}
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("source URL is invalid")
	}
	if u.User != nil {
		return "", errors.New("source URL must not contain userinfo")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return "", errors.New("source URL must use HTTPS")
	}
	u.Fragment = ""
	return u.String(), nil
}

func validateSpec(raw Spec, allowBuiltin bool) (Spec, error) {
	s := raw
	s.ID = strings.TrimSpace(s.ID)
	s.Engine = strings.TrimSpace(s.Engine)
	s.Repository = strings.TrimSpace(s.Repository)
	s.Ref = strings.TrimSpace(s.Ref)
	s.AssetName = strings.TrimSpace(s.AssetName)
	s.AssetPattern = strings.TrimSpace(s.AssetPattern)
	s.URL = strings.TrimSpace(s.URL)
	s.Path = strings.TrimSpace(s.Path)
	s.ExpectedSHA256 = strings.ToLower(strings.TrimSpace(s.ExpectedSHA256))
	if !validID(s.ID) {
		return s, errors.New("invalid runtime source ID")
	}
	if !validEngine(s.Engine) {
		return s, errors.New("invalid runtime source engine")
	}
	if s.Builtin && !allowBuiltin {
		return s, errors.New("custom source cannot claim builtin authority")
	}
	switch s.DefaultChannel {
	case ChannelLatestStable, ChannelPinnedRelease, ChannelPinnedCommit, ChannelManualOnly:
	default:
		return s, fmt.Errorf("unsupported source channel %q", s.DefaultChannel)
	}
	switch s.Kind {
	case KindGitHub, KindNewFuture:
		if !validRepository(s.Repository) {
			return s, errors.New("GitHub source requires OWNER/REPO repository")
		}
		if s.Kind == KindNewFuture && !strings.EqualFold(s.Repository, "NewFuture/rclone-fuse3-magisk") {
			return s, errors.New("NewFuture-derived source must use NewFuture/rclone-fuse3-magisk")
		}
		if s.DefaultChannel == ChannelManualOnly {
			return s, errors.New("GitHub source cannot default to manual-only")
		}
	case KindURL:
		if s.DefaultChannel != ChannelManualOnly {
			return s, errors.New("URL sources are manual-only")
		}
		clean, err := normalizeURL(s.URL)
		if err != nil {
			return s, err
		}
		s.URL = clean
		if !validSHA256(s.ExpectedSHA256) {
			return s, errors.New("URL source requires expected SHA-256")
		}
	case KindLocalBinary:
		if s.DefaultChannel != ChannelManualOnly {
			return s, errors.New("local binary sources are manual-only")
		}
		if s.Path == "" {
			return s, errors.New("local binary source requires path")
		}
	case KindSourceBuild:
		if s.DefaultChannel != ChannelPinnedCommit && s.DefaultChannel != ChannelManualOnly {
			return s, errors.New("source build requires pinned-commit or manual-only channel")
		}
		if s.Path == "" || !validRepository(s.Repository) || !validCommit(s.Ref) {
			return s, errors.New("source build requires path, OWNER/REPO and full 40-hex commit")
		}
	default:
		return s, fmt.Errorf("unsupported source kind %q", s.Kind)
	}
	return s, nil
}

func registryPath(p paths.Paths) string  { return p.Normalize().RuntimeSourceRegistry }
func resolutionDir(p paths.Paths) string { return p.Normalize().RuntimeSourceResolutionsDir }

func withRegistryLock(p paths.Paths, fn func() error) error {
	p = p.Normalize()
	if err := os.MkdirAll(p.RuntimeSourcesDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(p.RuntimeSourcesDir, "registry.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func loadCustomRegistry(p paths.Paths) (Registry, error) {
	path := registryPath(p)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Registry{SchemaVersion: RegistrySchemaVersion, Sources: []Spec{}}, nil
	}
	if err != nil {
		return Registry{}, err
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return Registry{}, err
	}
	if r.SchemaVersion != RegistrySchemaVersion {
		return Registry{}, errors.New("unsupported runtime source registry schema")
	}
	seen := map[string]bool{}
	for i, raw := range r.Sources {
		s, err := validateSpec(raw, false)
		if err != nil {
			return Registry{}, fmt.Errorf("source %d: %w", i, err)
		}
		if seen[s.ID] {
			return Registry{}, fmt.Errorf("duplicate runtime source %q", s.ID)
		}
		for _, b := range builtinSpecs() {
			if s.ID == b.ID {
				return Registry{}, fmt.Errorf("custom source shadows builtin %q", s.ID)
			}
		}
		seen[s.ID] = true
		r.Sources[i] = s
	}
	return r, nil
}

func writeRegistry(p paths.Paths, r Registry) error {
	p = p.Normalize()
	if err := os.MkdirAll(p.RuntimeSourcesDir, 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(p.RuntimeSourcesDir, ".registry-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, p.RuntimeSourceRegistry)
}

func List(p paths.Paths) ([]Spec, error) {
	custom, err := loadCustomRegistry(p)
	if err != nil {
		return nil, err
	}
	out := append([]Spec{}, builtinSpecs()...)
	out = append(out, custom.Sources...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func Get(p paths.Paths, id string) (Spec, uint64, error) {
	custom, err := loadCustomRegistry(p)
	if err != nil {
		return Spec{}, 0, err
	}
	for _, b := range builtinSpecs() {
		if b.ID == id {
			return b, custom.Revision, nil
		}
	}
	for _, s := range custom.Sources {
		if s.ID == id {
			return s, custom.Revision, nil
		}
	}
	return Spec{}, custom.Revision, os.ErrNotExist
}

func Register(p paths.Paths, raw Spec) (Spec, error) {
	spec, err := validateSpec(raw, false)
	if err != nil {
		return Spec{}, err
	}
	for _, b := range builtinSpecs() {
		if b.ID == spec.ID {
			return Spec{}, fmt.Errorf("builtin source %q is immutable", spec.ID)
		}
	}
	err = withRegistryLock(p, func() error {
		r, err := loadCustomRegistry(p)
		if err != nil {
			return err
		}
		replaced := false
		for i := range r.Sources {
			if r.Sources[i].ID == spec.ID {
				r.Sources[i] = spec
				replaced = true
				break
			}
		}
		if !replaced {
			r.Sources = append(r.Sources, spec)
		}
		sort.Slice(r.Sources, func(i, j int) bool { return r.Sources[i].ID < r.Sources[j].ID })
		r.Revision++
		return writeRegistry(p, r)
	})
	return spec, err
}

func Remove(p paths.Paths, id string) error {
	for _, b := range builtinSpecs() {
		if b.ID == id {
			return fmt.Errorf("builtin source %q is immutable", id)
		}
	}
	return withRegistryLock(p, func() error {
		r, err := loadCustomRegistry(p)
		if err != nil {
			return err
		}
		out := r.Sources[:0]
		found := false
		for _, s := range r.Sources {
			if s.ID == id {
				found = true
				continue
			}
			out = append(out, s)
		}
		if !found {
			return os.ErrNotExist
		}
		r.Sources = out
		r.Revision++
		return writeRegistry(p, r)
	})
}

func digestSpec(s Spec) string {
	s.Builtin = false
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashPath(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !st.Mode().IsRegular() {
		return "", 0, errors.New("source path is not a regular file")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func resolutionIdentity(r Resolution) string {
	copy := r
	copy.ResolutionID = ""
	copy.ResolvedUnixMS = 0
	data, _ := json.Marshal(copy)
	sum := sha256.Sum256(data)
	return "src-" + hex.EncodeToString(sum[:])[:32]
}

func persistResolution(p paths.Paths, r Resolution) (Resolution, error) {
	p = p.Normalize()
	if err := os.MkdirAll(p.RuntimeSourceResolutionsDir, 0o700); err != nil {
		return r, err
	}
	r.SchemaVersion = ResolutionSchemaVersion
	r.ResolutionID = resolutionIdentity(r)
	path := filepath.Join(p.RuntimeSourceResolutionsDir, r.ResolutionID+".json")
	if data, err := os.ReadFile(path); err == nil {
		var existing Resolution
		if json.Unmarshal(data, &existing) == nil && existing.ResolutionID == r.ResolutionID {
			return existing, nil
		}
		return r, errors.New("runtime source resolution ID collision")
	} else if !os.IsNotExist(err) {
		return r, err
	}
	payload, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return r, err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(p.RuntimeSourceResolutionsDir, ".resolution-*")
	if err != nil {
		return r, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return r, err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return r, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return r, err
	}
	if err := tmp.Close(); err != nil {
		return r, err
	}
	if err := os.Rename(name, path); err != nil {
		return r, err
	}
	return r, nil
}

func InspectResolution(p paths.Paths, id string) (Resolution, error) {
	if !strings.HasPrefix(id, "src-") || len(id) != 36 || strings.ContainsAny(id, "/\\") {
		return Resolution{}, errors.New("invalid source resolution ID")
	}
	data, err := os.ReadFile(filepath.Join(resolutionDir(p), id+".json"))
	if err != nil {
		return Resolution{}, err
	}
	var r Resolution
	if err := json.Unmarshal(data, &r); err != nil {
		return Resolution{}, err
	}
	if r.SchemaVersion != ResolutionSchemaVersion || r.ResolutionID != id || resolutionIdentity(r) != id {
		return Resolution{}, errors.New("source resolution integrity check failed")
	}
	return r, nil
}

func ListResolutions(p paths.Paths) ([]Resolution, error) {
	dir := resolutionDir(p)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Resolution{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Resolution{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := InspectResolution(p, strings.TrimSuffix(e.Name(), ".json"))
		if err == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResolvedUnixMS > out[j].ResolvedUnixMS })
	return out, nil
}

func Resolve(ctx context.Context, p paths.Paths, resolver *GitHubResolver, sourceID string, req ResolveRequest) (Resolution, error) {
	spec, revision, err := Get(p, sourceID)
	if err != nil {
		return Resolution{}, err
	}
	channel := req.Channel
	if channel == "" {
		channel = spec.DefaultChannel
	}
	r := Resolution{SchemaVersion: ResolutionSchemaVersion, SourceID: spec.ID, SpecDigest: digestSpec(spec), RegistryRevision: revision, Engine: spec.Engine, Kind: spec.Kind, Channel: channel, Repository: spec.Repository, ResolvedUnixMS: time.Now().UnixMilli()}
	switch spec.Kind {
	case KindGitHub, KindNewFuture:
		if resolver == nil {
			resolver = NewGitHubResolver(nil)
		}
		gh, err := resolver.Resolve(ctx, spec, req, channel)
		if err != nil {
			return Resolution{}, err
		}
		r.RepositoryID = gh.RepositoryID
		r.RequestedRef = gh.RequestedRef
		r.ReleaseID = gh.ReleaseID
		r.ReleaseTag = gh.ReleaseTag
		r.CommitSHA = gh.CommitSHA
		r.Asset = gh.Asset
	case KindURL:
		if channel != ChannelManualOnly {
			return Resolution{}, errors.New("URL source supports manual-only channel")
		}
		clean, err := normalizeURL(spec.URL)
		if err != nil {
			return Resolution{}, err
		}
		digest := spec.ExpectedSHA256
		if req.ExpectedSHA256 != "" {
			digest = strings.ToLower(req.ExpectedSHA256)
		}
		if !validSHA256(digest) {
			return Resolution{}, errors.New("URL resolution requires expected SHA-256")
		}
		r.URL = clean
		r.ContentSHA256 = digest
	case KindLocalBinary:
		if channel != ChannelManualOnly {
			return Resolution{}, errors.New("local binary supports manual-only channel")
		}
		digest, size, err := hashPath(spec.Path)
		if err != nil {
			return Resolution{}, err
		}
		r.Path = filepath.Clean(spec.Path)
		r.ContentSHA256 = digest
		r.Size = size
	case KindSourceBuild:
		if channel != ChannelPinnedCommit && channel != ChannelManualOnly {
			return Resolution{}, errors.New("source build requires pinned-commit or manual-only channel")
		}
		ref := spec.Ref
		if req.Ref != "" {
			ref = req.Ref
		}
		if !validCommit(ref) {
			return Resolution{}, errors.New("source build requires immutable 40-hex commit")
		}
		digest, size, err := hashPath(spec.Path)
		if err != nil {
			return Resolution{}, err
		}
		r.RequestedRef = ref
		r.CommitSHA = strings.ToLower(ref)
		r.Path = filepath.Clean(spec.Path)
		r.ContentSHA256 = digest
		r.Size = size
	default:
		return Resolution{}, fmt.Errorf("unsupported source kind %q", spec.Kind)
	}
	return persistResolution(p, r)
}

type BuildResolutionRequest struct {
	SourceID      string
	Engine        string
	Repository    string
	RequestedRef  string
	CommitSHA     string
	Path          string
	ContentSHA256 string
	Size          int64
}

// PersistBuildResolution binds a verified SOURCE-X02 build bundle to the same
// immutable source-resolution authority consumed by the X02 runtime store.
func PersistBuildResolution(p paths.Paths, req BuildResolutionRequest) (Resolution, error) {
	sourceID := strings.TrimSpace(req.SourceID)
	spec, revision, err := Get(p, sourceID)
	if err != nil {
		return Resolution{}, fmt.Errorf("runtime build source %q is not registered: %w", sourceID, err)
	}
	if !strings.EqualFold(strings.TrimSpace(req.Engine), spec.Engine) {
		return Resolution{}, errors.New("runtime build engine does not match registered source")
	}
	if !strings.EqualFold(strings.TrimSpace(req.Repository), spec.Repository) {
		return Resolution{}, errors.New("runtime build repository does not match registered source")
	}
	commit := strings.ToLower(strings.TrimSpace(req.CommitSHA))
	if !validCommit(commit) {
		return Resolution{}, errors.New("runtime build requires immutable 40-hex commit")
	}
	digest := strings.ToLower(strings.TrimSpace(req.ContentSHA256))
	if !validSHA256(digest) || req.Size <= 0 {
		return Resolution{}, errors.New("runtime build content identity is invalid")
	}
	path := filepath.Clean(strings.TrimSpace(req.Path))
	actualDigest, actualSize, err := hashPath(path)
	if err != nil {
		return Resolution{}, err
	}
	if actualDigest != digest || actualSize != req.Size {
		return Resolution{}, errors.New("runtime build bytes do not match verified provenance")
	}
	r := Resolution{
		SchemaVersion:    ResolutionSchemaVersion,
		SourceID:         sourceID,
		SpecDigest:       digestSpec(spec),
		RegistryRevision: revision,
		Engine:           spec.Engine,
		Kind:             KindSourceBuild,
		Channel:          ChannelPinnedCommit,
		Repository:       spec.Repository,
		RequestedRef:     strings.TrimSpace(req.RequestedRef),
		CommitSHA:        commit,
		Path:             path,
		ContentSHA256:    digest,
		Size:             req.Size,
		ResolvedUnixMS:   time.Now().UnixMilli(),
	}
	if r.RequestedRef == "" {
		r.RequestedRef = commit
	}
	return persistResolution(p, r)
}

func ImportRequestForResolution(r Resolution) (runtimestore.ImportRequest, error) {
	if r.ResolutionID == "" || resolutionIdentity(r) != r.ResolutionID {
		return runtimestore.ImportRequest{}, errors.New("invalid source resolution")
	}
	req := runtimestore.ImportRequest{Engine: r.Engine, Repository: r.Repository, ResolvedRef: r.CommitSHA, ResolutionID: r.ResolutionID, ExpectedSHA256: r.ContentSHA256, ReleaseID: r.ReleaseID}
	switch r.Kind {
	case KindGitHub:
		if r.Asset == nil {
			return req, errors.New("GitHub release resolution has no selected asset")
		}
		req.SourceType = runtimestore.SourceGitHub
		req.AssetID = r.Asset.ID
		req.AssetName = r.Asset.Name
		req.AssetURL = r.Asset.APIURL
		if strings.HasPrefix(strings.ToLower(r.Asset.Digest), "sha256:") {
			req.ExpectedSHA256 = strings.TrimPrefix(strings.ToLower(r.Asset.Digest), "sha256:")
		}
	case KindNewFuture:
		if r.Asset == nil {
			return req, errors.New("NewFuture resolution has no selected asset")
		}
		req.SourceType = runtimestore.SourceNewFuture
		req.AssetID = r.Asset.ID
		req.AssetName = r.Asset.Name
		req.URL = r.Asset.APIURL
		if strings.HasPrefix(strings.ToLower(r.Asset.Digest), "sha256:") {
			req.ExpectedSHA256 = strings.TrimPrefix(strings.ToLower(r.Asset.Digest), "sha256:")
		}
	case KindURL:
		req.SourceType = runtimestore.SourceURL
		req.URL = r.URL
	case KindLocalBinary:
		req.SourceType = runtimestore.SourceLocalFile
		req.Path = r.Path
	case KindSourceBuild:
		req.SourceType = runtimestore.SourceBuild
		req.Path = r.Path
	default:
		return req, fmt.Errorf("unsupported resolved source kind %q", r.Kind)
	}
	return req, nil
}

func ImportResolution(ctx context.Context, p paths.Paths, id string) (runtimestore.Manifest, error) {
	r, err := InspectResolution(p, id)
	if err != nil {
		return runtimestore.Manifest{}, err
	}
	req, err := ImportRequestForResolution(r)
	if err != nil {
		return runtimestore.Manifest{}, err
	}
	return runtimestore.Import(ctx, p, req)
}

// GitHubResolver is defined here so tests can point only this metadata client at
// an isolated HTTP server. Production always uses https://api.github.com.
type GitHubResolver struct {
	client       *http.Client
	apiBase      *url.URL
	browserHosts map[string]bool
}

func NewGitHubResolver(client *http.Client) *GitHubResolver {
	base, _ := url.Parse("https://api.github.com")
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &GitHubResolver{client: client, apiBase: base, browserHosts: map[string]bool{"github.com": true}}
}

func newTestGitHubResolver(client *http.Client, base string) (*GitHubResolver, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	return &GitHubResolver{client: client, apiBase: u, browserHosts: map[string]bool{u.Hostname(): true}}, nil
}
