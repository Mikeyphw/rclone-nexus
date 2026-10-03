package runtimestore

import (
	"context"
	"crypto/sha256"
	"debug/elf"
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
	"time"

	"rclone-nexus/internal/paths"
)

const (
	ManifestSchemaVersion = 1
	QualifierVersion      = "runtime-standalone-x02-v1"
	maxImportBytes        = int64(512 << 20)
)

type SourceType string

const (
	SourceLocalFile      SourceType = "local-file"
	SourceExecutablePath SourceType = "executable-path"
	SourceURL            SourceType = "url"
	SourceGitHub         SourceType = "github-release"
	SourceBuild          SourceType = "source-build"
	SourceNewFuture      SourceType = "newfuture-derived"
)

type ImportRequest struct {
	Engine         string     `json:"engine"`
	SourceType     SourceType `json:"source_type"`
	Path           string     `json:"path,omitempty"`
	URL            string     `json:"url,omitempty"`
	Repository     string     `json:"repository,omitempty"`
	ResolvedRef    string     `json:"resolved_ref,omitempty"`
	AssetName      string     `json:"asset_name,omitempty"`
	AssetURL       string     `json:"asset_url,omitempty"`
	ResolutionID   string     `json:"resolution_id,omitempty"`
	ReleaseID      int64      `json:"release_id,omitempty"`
	AssetID        int64      `json:"asset_id,omitempty"`
	ExpectedSHA256 string     `json:"expected_sha256,omitempty"`
}

type SourceProvenance struct {
	Type           SourceType `json:"type"`
	Repository     string     `json:"repository,omitempty"`
	ResolvedRef    string     `json:"resolved_ref,omitempty"`
	AssetName      string     `json:"asset_name,omitempty"`
	AssetURL       string     `json:"asset_url,omitempty"`
	OriginPath     string     `json:"origin_path,omitempty"`
	ResolutionID   string     `json:"resolution_id,omitempty"`
	ReleaseID      int64      `json:"release_id,omitempty"`
	AssetID        int64      `json:"asset_id,omitempty"`
	ExpectedSHA256 string     `json:"expected_sha256,omitempty"`
}

type ELFMetadata struct {
	Class        string `json:"class"`
	Data         string `json:"data"`
	Machine      string `json:"machine"`
	OSABI        string `json:"osabi"`
	Type         string `json:"type"`
	Architecture string `json:"architecture"`
}

type Check struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Required bool   `json:"required"`
	Detail   string `json:"detail,omitempty"`
}

type Qualification struct {
	QualifierVersion string   `json:"qualifier_version"`
	State            string   `json:"state"`
	Qualified        bool     `json:"qualified"`
	StartedUnixMS    int64    `json:"started_unix_ms"`
	FinishedUnixMS   int64    `json:"finished_unix_ms"`
	BinarySHA256     string   `json:"binary_sha256"`
	Checks           []Check  `json:"checks"`
	Evidence         []string `json:"evidence,omitempty"`
}

type Manifest struct {
	SchemaVersion  int              `json:"schema_version"`
	RuntimeID      string           `json:"runtime_id"`
	Engine         string           `json:"engine"`
	VersionOutput  string           `json:"version_output,omitempty"`
	Source         SourceProvenance `json:"source"`
	ArchiveSHA256  string           `json:"archive_sha256"`
	BinarySHA256   string           `json:"binary_sha256"`
	ELF            ELFMetadata      `json:"elf"`
	ImportedUnixMS int64            `json:"imported_unix_ms"`
	Qualifier      string           `json:"qualifier_version"`
	Qualification  Qualification    `json:"qualification"`
}

type QualificationError struct {
	State string
}

func (e *QualificationError) Error() string {
	if e.State == "blocked_by_environment" {
		return "runtime qualification blocked by environment"
	}
	return "runtime candidate failed qualification"
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

func cleanURL(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func validateRequest(req ImportRequest) (ImportRequest, error) {
	req.Engine = strings.TrimSpace(req.Engine)
	if req.Engine == "" {
		req.Engine = "rclone"
	}
	if !validEngine(req.Engine) {
		return req, errors.New("invalid runtime engine name")
	}
	req.Path = strings.TrimSpace(req.Path)
	req.URL = strings.TrimSpace(req.URL)
	req.Repository = strings.TrimSpace(req.Repository)
	req.ResolvedRef = strings.TrimSpace(req.ResolvedRef)
	req.AssetName = strings.TrimSpace(req.AssetName)
	req.AssetURL = strings.TrimSpace(req.AssetURL)
	req.ResolutionID = strings.TrimSpace(req.ResolutionID)
	req.ExpectedSHA256 = strings.ToLower(strings.TrimSpace(req.ExpectedSHA256))
	if req.ExpectedSHA256 != "" {
		if len(req.ExpectedSHA256) != 64 {
			return req, errors.New("expected SHA-256 must be 64 hex characters")
		}
		if _, err := hex.DecodeString(req.ExpectedSHA256); err != nil {
			return req, errors.New("expected SHA-256 is invalid")
		}
	}

	switch req.SourceType {
	case SourceLocalFile, SourceExecutablePath:
		if req.Path == "" {
			return req, fmt.Errorf("%s import requires --path", req.SourceType)
		}
	case SourceURL:
		if req.URL == "" || cleanURL(req.URL) == "" {
			return req, errors.New("url import requires a valid --url")
		}
	case SourceGitHub:
		if req.Repository == "" || req.ResolvedRef == "" || req.AssetName == "" || req.AssetURL == "" || cleanURL(req.AssetURL) == "" {
			return req, errors.New("github import requires repository, resolved ref, asset name and asset URL")
		}
	case SourceBuild:
		if req.Path == "" || req.Repository == "" || req.ResolvedRef == "" {
			return req, errors.New("source-build import requires path, repository and immutable resolved ref")
		}
	case SourceNewFuture:
		if req.Repository == "" {
			req.Repository = "NewFuture/rclone-fuse3-magisk"
		}
		if req.Path == "" && req.URL == "" {
			return req, errors.New("newfuture-derived import requires path or URL")
		}
		if req.URL != "" && cleanURL(req.URL) == "" {
			return req, errors.New("newfuture-derived URL is invalid")
		}
	default:
		return req, fmt.Errorf("unsupported runtime source type %q", req.SourceType)
	}
	return req, nil
}

func provenance(req ImportRequest) SourceProvenance {
	p := SourceProvenance{Type: req.SourceType, Repository: req.Repository, ResolvedRef: req.ResolvedRef, AssetName: req.AssetName, ResolutionID: req.ResolutionID, ReleaseID: req.ReleaseID, AssetID: req.AssetID, ExpectedSHA256: req.ExpectedSHA256}
	switch req.SourceType {
	case SourceURL:
		p.AssetURL = cleanURL(req.URL)
	case SourceGitHub:
		p.AssetURL = cleanURL(req.AssetURL)
	case SourceNewFuture:
		p.AssetURL = cleanURL(req.URL)
	}
	if req.Path != "" {
		p.OriginPath = filepath.Clean(req.Path)
	}
	return p
}

type sourceReader struct {
	Reader io.ReadCloser
	Name   string
}

func openSource(ctx context.Context, req ImportRequest) (sourceReader, error) {
	if req.Path != "" {
		f, err := os.Open(req.Path)
		if err != nil {
			return sourceReader{}, err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return sourceReader{}, err
		}
		if !info.Mode().IsRegular() {
			f.Close()
			return sourceReader{}, errors.New("runtime source path is not a regular file")
		}
		return sourceReader{Reader: f, Name: req.Path}, nil
	}
	raw := req.URL
	if req.SourceType == SourceGitHub {
		raw = req.AssetURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return sourceReader{}, err
	}
	isGitHubAsset := (req.SourceType == SourceGitHub || req.SourceType == SourceNewFuture) && req.AssetID > 0
	if isGitHubAsset {
		httpReq.Header.Set("Accept", "application/octet-stream")
		httpReq.Header.Set("User-Agent", "rclone-nexus-runtime-import")
	}
	initial, _ := url.Parse(raw)
	client := &http.Client{Timeout: 2 * time.Minute}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("runtime source redirected too many times")
		}
		if next.URL.Scheme != "https" && !(initial != nil && initial.Scheme == "http" && (initial.Hostname() == "127.0.0.1" || initial.Hostname() == "localhost" || initial.Hostname() == "::1")) {
			return errors.New("runtime source redirect downgraded transport")
		}
		if isGitHubAsset && initial != nil && strings.EqualFold(initial.Hostname(), "api.github.com") {
			allowed := map[string]bool{"api.github.com": true, "github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true, "github-releases.githubusercontent.com": true}
			if !allowed[strings.ToLower(next.URL.Hostname())] {
				return errors.New("GitHub asset redirect escaped trusted domains")
			}
		} else if initial != nil && !strings.EqualFold(next.URL.Host, initial.Host) {
			return errors.New("runtime source redirect escaped trusted origin")
		}
		return nil
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return sourceReader{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return sourceReader{}, fmt.Errorf("runtime source download failed with HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxImportBytes {
		resp.Body.Close()
		return sourceReader{}, errors.New("runtime source exceeds import size limit")
	}
	return sourceReader{Reader: resp.Body, Name: cleanURL(raw)}, nil
}

func elfMetadata(file *elf.File) ELFMetadata {
	arch := "unknown"
	switch file.Machine {
	case elf.EM_AARCH64:
		arch = "arm64"
	case elf.EM_X86_64:
		arch = "amd64"
	case elf.EM_386:
		arch = "386"
	case elf.EM_ARM:
		arch = "arm"
	}
	return ELFMetadata{
		Class:        file.Class.String(),
		Data:         file.Data.String(),
		Machine:      file.Machine.String(),
		OSABI:        file.OSABI.String(),
		Type:         file.Type.String(),
		Architecture: arch,
	}
}

func inspectELF(path string) (ELFMetadata, error) {
	file, err := elf.Open(path)
	if err != nil {
		return ELFMetadata{}, err
	}
	defer file.Close()
	return elfMetadata(file), nil
}

func inspectELFReader(reader io.ReaderAt) (ELFMetadata, error) {
	file, err := elf.NewFile(reader)
	if err != nil {
		return ELFMetadata{}, err
	}
	defer file.Close()
	return elfMetadata(file), nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func runtimeID(engine, digest string, source SourceProvenance) string {
	payload, _ := json.Marshal(source)
	sourceDigest := sha256.Sum256(payload)
	return strings.ToLower(engine) + "-" + digest[:20] + "-" + hex.EncodeToString(sourceDigest[:])[:10]
}

func manifestPath(p paths.Paths, id string) string {
	return filepath.Join(p.Normalize().RuntimeStoreDir, id, "manifest.json")
}

func BinaryPath(p paths.Paths, id string) string {
	return filepath.Join(p.Normalize().RuntimeStoreDir, id, "rclone")
}

func writeManifest(p paths.Paths, manifest Manifest) error {
	path := manifestPath(p, manifest.RuntimeID)
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*")
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
	return os.Rename(name, path)
}

func Import(ctx context.Context, p paths.Paths, raw ImportRequest) (Manifest, error) {
	p = p.Normalize()
	req, err := validateRequest(raw)
	if err != nil {
		return Manifest{}, err
	}
	if err := p.EnsureState(); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(p.RuntimeStoreDir, 0o700); err != nil {
		return Manifest{}, err
	}
	tmpDir, err := os.MkdirTemp(p.RuntimeStoreDir, ".candidate-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(tmpDir)
	tmpBin := filepath.Join(tmpDir, "rclone")
	out, err := os.OpenFile(tmpBin, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o500)
	if err != nil {
		return Manifest{}, err
	}
	source, err := openSource(ctx, req)
	if err != nil {
		out.Close()
		return Manifest{}, err
	}
	h := sha256.New()
	limited := io.LimitReader(source.Reader, maxImportBytes+1)
	n, copyErr := io.Copy(io.MultiWriter(out, h), limited)
	closeOutErr := out.Close()
	closeSourceErr := source.Reader.Close()
	if copyErr != nil {
		return Manifest{}, copyErr
	}
	if closeOutErr != nil {
		return Manifest{}, closeOutErr
	}
	if closeSourceErr != nil {
		return Manifest{}, closeSourceErr
	}
	if n == 0 {
		return Manifest{}, errors.New("runtime source is empty")
	}
	if n > maxImportBytes {
		return Manifest{}, errors.New("runtime source exceeds import size limit")
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if req.ExpectedSHA256 != "" && !strings.EqualFold(digest, req.ExpectedSHA256) {
		return Manifest{}, fmt.Errorf("runtime source SHA-256 mismatch: got %s want %s", digest, req.ExpectedSHA256)
	}
	prov := provenance(req)
	id := runtimeID(req.Engine, digest, prov)
	finalDir := filepath.Join(p.RuntimeStoreDir, id)
	finalBin := filepath.Join(finalDir, "rclone")
	if info, statErr := os.Lstat(finalDir); statErr == nil {
		if !info.IsDir() {
			return Manifest{}, errors.New("runtime store entry is not a directory")
		}
		existing, hashErr := hashFile(finalBin)
		if hashErr != nil || existing != digest {
			return Manifest{}, errors.New("runtime ID collision or existing candidate bytes changed")
		}
	} else if !os.IsNotExist(statErr) {
		return Manifest{}, statErr
	} else {
		if err := os.Chmod(tmpBin, 0o500); err != nil {
			return Manifest{}, err
		}
		if err := os.Rename(tmpDir, finalDir); err != nil {
			return Manifest{}, err
		}
		if err := os.Chmod(finalDir, 0o700); err != nil {
			return Manifest{}, err
		}
	}

	elfMeta, elfErr := inspectELF(finalBin)
	manifest := Manifest{
		SchemaVersion:  ManifestSchemaVersion,
		RuntimeID:      id,
		Engine:         req.Engine,
		Source:         prov,
		ArchiveSHA256:  digest,
		BinarySHA256:   digest,
		ELF:            elfMeta,
		ImportedUnixMS: time.Now().UnixMilli(),
		Qualifier:      QualifierVersion,
	}
	if elfErr != nil {
		manifest.ELF = ELFMetadata{}
	}
	qualification := Qualify(ctx, p, manifest)
	manifest.Qualification = qualification
	manifest.VersionOutput = versionFromQualification(qualification)
	if err := writeManifest(p, manifest); err != nil {
		return Manifest{}, err
	}
	if !qualification.Qualified {
		return manifest, &QualificationError{State: qualification.State}
	}
	return manifest, nil
}

func versionFromQualification(q Qualification) string {
	for _, check := range q.Checks {
		if check.Name == "version" && check.Status == "pass" {
			return check.Detail
		}
	}
	return ""
}

func Inspect(p paths.Paths, id string) (Manifest, error) {
	if !validRuntimeID(id) {
		return Manifest{}, errors.New("invalid runtime ID")
	}
	data, err := os.ReadFile(manifestPath(p, id))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.RuntimeID != id || manifest.BinarySHA256 == "" {
		return Manifest{}, errors.New("invalid runtime manifest")
	}
	actual, err := hashFile(BinaryPath(p, id))
	if err != nil {
		return Manifest{}, err
	}
	if actual != manifest.BinarySHA256 {
		return Manifest{}, errors.New("runtime candidate bytes do not match manifest")
	}
	return manifest, nil
}

func validRuntimeID(id string) bool {
	if id == "" || len(id) > 96 || strings.Contains(id, "..") || strings.ContainsAny(id, "/\\\x00\r\n") {
		return false
	}
	return filepath.Base(id) == id
}

func List(p paths.Paths) ([]Manifest, error) {
	root := p.Normalize().RuntimeStoreDir
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		manifest, err := Inspect(p, entry.Name())
		if err != nil {
			continue
		}
		out = append(out, manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ImportedUnixMS > out[j].ImportedUnixMS })
	return out, nil
}

func Test(ctx context.Context, p paths.Paths, id string) (Manifest, error) {
	manifest, err := Inspect(p, id)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Qualification = Qualify(ctx, p, manifest)
	manifest.Qualifier = QualifierVersion
	manifest.VersionOutput = versionFromQualification(manifest.Qualification)
	if err := writeManifest(p, manifest); err != nil {
		return Manifest{}, err
	}
	if !manifest.Qualification.Qualified {
		return manifest, &QualificationError{State: manifest.Qualification.State}
	}
	return manifest, nil
}
