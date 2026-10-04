package runtimebuild

import (
	"bufio"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimesource"
	"rclone-nexus/internal/runtimestore"
)

const ManifestSchemaVersion = 1

var fullCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Manifest struct {
	SchemaVersion   int      `json:"schema_version"`
	State           string   `json:"state"`
	SourceID        string   `json:"source_id"`
	Engine          string   `json:"engine"`
	Repository      string   `json:"repository"`
	RequestedRef    string   `json:"requested_ref"`
	ResolvedCommit  string   `json:"resolved_commit"`
	GoVersion       string   `json:"go_version"`
	NDKVersion      string   `json:"ndk_version"`
	Compiler        string   `json:"compiler"`
	CompilerVersion string   `json:"compiler_version"`
	APILevel        int      `json:"api_level"`
	GOOS            string   `json:"goos"`
	GOARCH          string   `json:"goarch"`
	ABI             string   `json:"abi"`
	CGOEnabled      bool     `json:"cgo_enabled"`
	Tags            []string `json:"tags"`
	Trimpath        bool     `json:"trimpath"`
	BuildFlags      []string `json:"build_flags"`
	LDFlags         []string `json:"ldflags"`
	BinaryName      string   `json:"binary_name"`
	BinarySHA256    string   `json:"binary_sha256"`
	BinarySize      int64    `json:"binary_size"`
	ProducedUnixMS  int64    `json:"produced_unix_ms"`
}

type VerifiedBundle struct {
	Dir          string   `json:"dir"`
	BinaryPath   string   `json:"binary_path"`
	ManifestPath string   `json:"manifest_path"`
	SumsPath     string   `json:"sums_path"`
	Manifest     Manifest `json:"manifest"`
}

func cleanRepo(v string) bool {
	parts := strings.Split(strings.TrimSpace(v), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, p := range parts {
		for _, r := range p {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
				continue
			}
			return false
		}
	}
	return true
}

func fileSHA256(path string) (string, int64, error) {
	lst, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !lst.Mode().IsRegular() || lst.Mode()&os.ModeSymlink != 0 {
		return "", 0, errors.New("bundle member is not a non-symlink regular file")
	}
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
		return "", 0, errors.New("bundle member changed while opening")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func readSums(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, errors.New("invalid SHA256SUMS entry")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, errors.New("invalid SHA256SUMS digest")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(name) != name || name == "." || name == ".." {
			return nil, errors.New("SHA256SUMS contains unsafe path")
		}
		if _, exists := out[name]; exists {
			return nil, errors.New("duplicate SHA256SUMS entry")
		}
		out[name] = strings.ToLower(fields[0])
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func elfAndroidArm64(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("runtime build is not ELF: %w", err)
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != elf.EM_AARCH64 {
		return fmt.Errorf("runtime build ELF is not Android arm64: class=%s data=%s machine=%s", f.Class, f.Data, f.Machine)
	}
	interp := ""
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		r := p.Open()
		data, err := io.ReadAll(io.LimitReader(r, 4096))
		if err != nil {
			return err
		}
		interp = strings.TrimRight(string(data), "\x00")
		break
	}
	if interp != "/system/bin/linker64" {
		return fmt.Errorf("runtime build does not carry Android arm64 interpreter: %q", interp)
	}
	return nil
}

func VerifyBundle(dir string) (VerifiedBundle, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return VerifiedBundle{}, err
	}
	manifestPath := filepath.Join(dir, "provenance.json")
	sumsPath := filepath.Join(dir, "SHA256SUMS")
	if _, _, err := fileSHA256(manifestPath); err != nil {
		return VerifiedBundle{}, fmt.Errorf("partial build bundle: provenance.json: %w", err)
	}
	if _, _, err := fileSHA256(sumsPath); err != nil {
		return VerifiedBundle{}, fmt.Errorf("partial build bundle: SHA256SUMS: %w", err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return VerifiedBundle{}, fmt.Errorf("partial build bundle: provenance.json: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return VerifiedBundle{}, fmt.Errorf("invalid provenance manifest: %w", err)
	}
	if m.SchemaVersion != ManifestSchemaVersion || m.State != "complete" {
		return VerifiedBundle{}, errors.New("build provenance is not complete")
	}
	if !cleanRepo(m.Repository) || strings.TrimSpace(m.SourceID) == "" || strings.TrimSpace(m.Engine) == "" {
		return VerifiedBundle{}, errors.New("build provenance source identity is invalid")
	}
	m.ResolvedCommit = strings.ToLower(strings.TrimSpace(m.ResolvedCommit))
	if !fullCommitRE.MatchString(m.ResolvedCommit) {
		return VerifiedBundle{}, errors.New("build provenance requires an immutable 40-hex commit")
	}
	if strings.TrimSpace(m.RequestedRef) == "" {
		return VerifiedBundle{}, errors.New("build provenance requested_ref is missing")
	}
	if m.GoVersion == "" || m.NDKVersion == "" || m.Compiler == "" || m.CompilerVersion == "" {
		return VerifiedBundle{}, errors.New("build toolchain provenance is incomplete")
	}
	if m.APILevel < 21 || m.GOOS != "android" || m.GOARCH != "arm64" || m.ABI != "arm64-v8a" || !m.CGOEnabled {
		return VerifiedBundle{}, errors.New("build target is not Android arm64-v8a with cgo")
	}
	if !m.Trimpath {
		return VerifiedBundle{}, errors.New("build must use -trimpath")
	}
	haveAndroidTag := false
	for _, tag := range m.Tags {
		if tag == "android" {
			haveAndroidTag = true
		}
	}
	if !haveAndroidTag {
		return VerifiedBundle{}, errors.New("build provenance is missing android build tag")
	}
	if !strings.Contains(m.Compiler, "aarch64-linux-android"+strconv.Itoa(m.APILevel)+"-clang") {
		return VerifiedBundle{}, errors.New("compiler does not match Android arm64 NDK target")
	}
	if filepath.Base(m.BinaryName) != m.BinaryName || m.BinaryName == "" {
		return VerifiedBundle{}, errors.New("invalid runtime build binary name")
	}
	binaryPath := filepath.Join(dir, m.BinaryName)
	sums, err := readSums(sumsPath)
	if err != nil {
		return VerifiedBundle{}, fmt.Errorf("partial build bundle: SHA256SUMS: %w", err)
	}
	if len(sums) != 2 {
		return VerifiedBundle{}, errors.New("SHA256SUMS must bind exactly binary and provenance.json")
	}
	manifestDigest, _, err := fileSHA256(manifestPath)
	if err != nil {
		return VerifiedBundle{}, err
	}
	if sums["provenance.json"] != manifestDigest {
		return VerifiedBundle{}, errors.New("provenance manifest SHA-256 mismatch")
	}
	binaryDigest, binarySize, err := fileSHA256(binaryPath)
	if err != nil {
		return VerifiedBundle{}, fmt.Errorf("partial build bundle: binary: %w", err)
	}
	if sums[m.BinaryName] != binaryDigest || strings.ToLower(m.BinarySHA256) != binaryDigest || m.BinarySize != binarySize {
		return VerifiedBundle{}, errors.New("runtime build binary SHA-256/size mismatch")
	}
	if err := elfAndroidArm64(binaryPath); err != nil {
		return VerifiedBundle{}, err
	}
	return VerifiedBundle{Dir: dir, BinaryPath: binaryPath, ManifestPath: manifestPath, SumsPath: sumsPath, Manifest: m}, nil
}

func AcquireBundle(ctx context.Context, p paths.Paths, dir string) (runtimestore.Manifest, runtimesource.Resolution, error) {
	bundle, err := VerifyBundle(dir)
	if err != nil {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, err
	}
	r, err := runtimesource.PersistBuildResolution(p, runtimesource.BuildResolutionRequest{
		SourceID: bundle.Manifest.SourceID, Engine: bundle.Manifest.Engine, Repository: bundle.Manifest.Repository,
		RequestedRef: bundle.Manifest.RequestedRef, CommitSHA: bundle.Manifest.ResolvedCommit,
		Path: bundle.BinaryPath, ContentSHA256: bundle.Manifest.BinarySHA256, Size: bundle.Manifest.BinarySize,
	})
	if err != nil {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, err
	}
	manifest, acquireErr := runtimesource.AcquireResolution(ctx, p, r.ResolutionID)
	return manifest, r, acquireErr
}

func ImportBundle(ctx context.Context, p paths.Paths, dir string) (runtimestore.Manifest, runtimesource.Resolution, error) {
	manifest, r, err := AcquireBundle(ctx, p, dir)
	if err != nil {
		return manifest, r, err
	}
	manifest, err = runtimestore.Test(ctx, p, manifest.RuntimeID)
	return manifest, r, err
}

func SortedFlags(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
