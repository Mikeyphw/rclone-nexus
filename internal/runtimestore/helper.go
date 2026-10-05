package runtimestore

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"rclone-nexus/internal/githubapi"
	"rclone-nexus/internal/paths"
)

const (
	FuseHelperSchemaVersion = 1
	newFutureRepository     = "NewFuture/rclone-fuse3-magisk"
	newFutureHelperAsset    = "magisk-rclone_arm64-v8a.zip"
	newFutureLatestRelease  = "https://api.github.com/repos/NewFuture/rclone-fuse3-magisk/releases/latest"
)

type FuseHelperManifest struct {
	SchemaVersion  int         `json:"schema_version"`
	Repository     string      `json:"repository"`
	ReleaseID      int64       `json:"release_id"`
	ReleaseTag     string      `json:"release_tag"`
	AssetID        int64       `json:"asset_id"`
	AssetName      string      `json:"asset_name"`
	AssetURL       string      `json:"asset_url"`
	ArchiveSHA256  string      `json:"archive_sha256"`
	HelperSHA256   string      `json:"helper_sha256"`
	ELF            ELFMetadata `json:"elf"`
	ImportedUnixMS int64       `json:"imported_unix_ms"`
}

type githubReleaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type githubRelease struct {
	ID      int64                `json:"id"`
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

func helperManifestPath(p paths.Paths) string { return p.Normalize().FuseHelperManifest }
func helperBinaryPath(p paths.Paths) string   { return p.Normalize().ManagedFuseHelperBin }

func readFuseHelperManifest(p paths.Paths) (FuseHelperManifest, error) {
	var m FuseHelperManifest
	payload, err := os.ReadFile(helperManifestPath(p))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		return m, err
	}
	if m.SchemaVersion != FuseHelperSchemaVersion || m.Repository != newFutureRepository || m.AssetName != newFutureHelperAsset || len(m.HelperSHA256) != 64 || len(m.ArchiveSHA256) != 64 {
		return FuseHelperManifest{}, errors.New("invalid managed fusermount3 manifest")
	}
	return m, nil
}

func InspectFuseHelper(p paths.Paths) (FuseHelperManifest, error) {
	p = p.Normalize()
	m, err := readFuseHelperManifest(p)
	if err != nil {
		return FuseHelperManifest{}, err
	}
	info, err := os.Stat(p.ManagedFuseHelperBin)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return FuseHelperManifest{}, errors.New("managed fusermount3 is unavailable")
	}
	digest, err := hashFile(p.ManagedFuseHelperBin)
	if err != nil || !strings.EqualFold(digest, m.HelperSHA256) {
		return FuseHelperManifest{}, errors.New("managed fusermount3 bytes do not match manifest")
	}
	elfMeta, err := inspectELF(p.ManagedFuseHelperBin)
	if err != nil {
		return FuseHelperManifest{}, fmt.Errorf("managed fusermount3 is not ELF: %w", err)
	}
	if runtime.GOOS == "android" && elfMeta.Architecture != "arm64" {
		return FuseHelperManifest{}, fmt.Errorf("managed fusermount3 architecture %s is not Android arm64", elfMeta.Architecture)
	}
	return m, nil
}

func githubJSON(ctx context.Context, p paths.Paths, raw string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	token, err := githubapi.Token(p)
	if err != nil {
		return err
	}
	githubapi.Prepare(req, token, "rclone-nexus-fusermount3")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := githubapi.ResponseError(resp, token != ""); err != nil {
		return fmt.Errorf("NewFuture release lookup: %w", err)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(dst)
}

func downloadGitHubAsset(ctx context.Context, p paths.Paths, asset githubReleaseAsset, dst string) (string, error) {
	if asset.ID <= 0 || asset.Name != newFutureHelperAsset || !strings.HasPrefix(asset.URL, "https://api.github.com/") {
		return "", errors.New("invalid NewFuture fusermount3 asset identity")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", err
	}
	token, err := githubapi.Token(p)
	if err != nil {
		return "", err
	}
	githubapi.Prepare(req, token, "rclone-nexus-fusermount3")
	req.Header.Set("Accept", "application/octet-stream")
	client := &http.Client{Timeout: 2 * time.Minute}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("NewFuture helper asset redirected too many times")
		}
		if next.URL.Scheme != "https" {
			return errors.New("NewFuture helper asset redirect downgraded transport")
		}
		allowed := map[string]bool{"api.github.com": true, "github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true, "github-releases.githubusercontent.com": true}
		if !allowed[strings.ToLower(next.URL.Hostname())] {
			return errors.New("NewFuture helper asset redirect escaped trusted GitHub domains")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := githubapi.ResponseError(resp, token != ""); err != nil {
		return "", fmt.Errorf("NewFuture helper asset download: %w", err)
	}
	if resp.ContentLength > maxImportBytes {
		return "", errors.New("NewFuture helper archive exceeds import size limit")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o400)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, maxImportBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n <= 0 || n > maxImportBytes {
		return "", errors.New("invalid NewFuture helper archive size")
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if strings.HasPrefix(strings.ToLower(asset.Digest), "sha256:") {
		expected := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:"))
		if expected != "" && digest != expected {
			return "", fmt.Errorf("NewFuture helper archive SHA-256 mismatch: got %s want %s", digest, expected)
		}
	}
	return digest, nil
}

func extractFuseHelper(archivePath, outputPath string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("malformed NewFuture helper archive: %w", err)
	}
	defer zr.Close()
	var selected *zip.File
	for _, entry := range zr.File {
		clean, err := validateZipEntry(entry)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() || path.Base(clean) != "fusermount3" {
			continue
		}
		if selected != nil {
			return errors.New("NewFuture helper archive contains multiple fusermount3 executables")
		}
		selected = entry
	}
	if selected == nil {
		return errors.New("NewFuture helper archive contains no fusermount3 executable")
	}
	in, err := selected.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o500)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, maxImportBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n <= 0 || n > maxImportBytes {
		return errors.New("invalid NewFuture fusermount3 size")
	}
	return nil
}

func writeFuseHelperManifest(p paths.Paths, m FuseHelperManifest) error {
	payload, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(p.FuseHelperDir, ".helper-manifest-*")
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
	return os.Rename(name, p.FuseHelperManifest)
}

func EnsureFuseHelper(ctx context.Context, p paths.Paths) (FuseHelperManifest, error) {
	p = p.Normalize()
	if current, err := InspectFuseHelper(p); err == nil {
		return current, nil
	}
	if err := os.MkdirAll(filepath.Join(p.FuseHelperDir, "objects"), 0o700); err != nil {
		return FuseHelperManifest{}, err
	}
	var release githubRelease
	if err := githubJSON(ctx, p, newFutureLatestRelease, &release); err != nil {
		return FuseHelperManifest{}, fmt.Errorf("resolve canonical NewFuture fusermount3: %w", err)
	}
	if release.ID <= 0 || strings.TrimSpace(release.TagName) == "" {
		return FuseHelperManifest{}, errors.New("NewFuture helper release has no immutable release identity")
	}
	var asset githubReleaseAsset
	for _, candidate := range release.Assets {
		if candidate.Name == newFutureHelperAsset {
			asset = candidate
			break
		}
	}
	if asset.ID <= 0 {
		return FuseHelperManifest{}, errors.New("latest NewFuture release has no Android arm64 helper asset")
	}
	tmpDir, err := os.MkdirTemp(p.FuseHelperDir, ".acquire-*")
	if err != nil {
		return FuseHelperManifest{}, err
	}
	defer os.RemoveAll(tmpDir)
	archivePath := filepath.Join(tmpDir, "newfuture.zip")
	archiveDigest, err := downloadGitHubAsset(ctx, p, asset, archivePath)
	if err != nil {
		return FuseHelperManifest{}, err
	}
	helperTmp := filepath.Join(tmpDir, "fusermount3")
	if err := extractFuseHelper(archivePath, helperTmp); err != nil {
		return FuseHelperManifest{}, err
	}
	helperDigest, err := hashFile(helperTmp)
	if err != nil {
		return FuseHelperManifest{}, err
	}
	elfMeta, err := inspectELF(helperTmp)
	if err != nil {
		return FuseHelperManifest{}, fmt.Errorf("NewFuture fusermount3 is not an ELF executable: %w", err)
	}
	if elfMeta.Architecture != "arm64" {
		return FuseHelperManifest{}, fmt.Errorf("NewFuture fusermount3 architecture is %s, expected arm64", elfMeta.Architecture)
	}
	objectDir := filepath.Join(p.FuseHelperDir, "objects", helperDigest)
	objectBin := filepath.Join(objectDir, "fusermount3")
	if err := os.MkdirAll(objectDir, 0o700); err != nil {
		return FuseHelperManifest{}, err
	}
	if existing, err := hashFile(objectBin); err == nil {
		if existing != helperDigest {
			return FuseHelperManifest{}, errors.New("managed fusermount3 object digest collision")
		}
	} else if os.IsNotExist(err) {
		if err := os.Rename(helperTmp, objectBin); err != nil {
			return FuseHelperManifest{}, err
		}
		if err := os.Chmod(objectBin, 0o500); err != nil {
			return FuseHelperManifest{}, err
		}
	} else {
		return FuseHelperManifest{}, err
	}
	current := filepath.Join(p.FuseHelperDir, "current")
	tmpLink := filepath.Join(p.FuseHelperDir, fmt.Sprintf(".current-%d-%d", os.Getpid(), time.Now().UnixNano()))
	_ = os.Remove(tmpLink)
	if err := os.Symlink(filepath.Join("objects", helperDigest), tmpLink); err != nil {
		return FuseHelperManifest{}, err
	}
	if err := os.Rename(tmpLink, current); err != nil {
		_ = os.Remove(tmpLink)
		return FuseHelperManifest{}, err
	}
	manifest := FuseHelperManifest{
		SchemaVersion:  FuseHelperSchemaVersion,
		Repository:     newFutureRepository,
		ReleaseID:      release.ID,
		ReleaseTag:     release.TagName,
		AssetID:        asset.ID,
		AssetName:      asset.Name,
		AssetURL:       asset.URL,
		ArchiveSHA256:  archiveDigest,
		HelperSHA256:   helperDigest,
		ELF:            elfMeta,
		ImportedUnixMS: time.Now().UnixMilli(),
	}
	if err := writeFuseHelperManifest(p, manifest); err != nil {
		return FuseHelperManifest{}, err
	}
	return InspectFuseHelper(p)
}
