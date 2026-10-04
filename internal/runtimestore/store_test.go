package runtimestore

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"rclone-nexus/internal/paths"
)

func buildFixture(t *testing.T, version string, omitFlag string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	code := `package main
import ("fmt"; "os"; "strings"; "time")
func main() {
 args:=os.Args[1:]
 if len(args)>0 && args[0]=="version" { if marker:=os.Getenv("RNEXUS_FIXTURE_MARKER"); marker!="" { _=os.WriteFile(marker, []byte("ready"), 0600); time.Sleep(350*time.Millisecond) }; fmt.Println("VERSION_PLACEHOLDER"); return }
 if len(args)>0 && args[0]=="listremotes" { fmt.Println("nexus_qual:"); return }
 if len(args)>=2 && args[0]=="mount" && args[1]=="--help" { fmt.Println("--vfs-cache-mode --vfs-cache-max-size --vfs-cache-max-age --dir-cache-time --poll-interval --allow-other --read-only"); return }
 if len(args)>=2 && args[0]=="help" && args[1]=="flags" { fmt.Println("--config --cache-dir --log-file --log-level --rc --rc-addr --rc-user --rc-pass"); return }
 if len(args)>0 && args[0]=="--help" { fmt.Println("--config --cache-dir --log-file --log-level --rc --rc-addr --rc-user --rc-pass"); return }
 if len(args)>0 && args[0]=="mount" { select{} }
 fmt.Fprintln(os.Stderr, strings.Join(args," ")); os.Exit(2)
}`
	code = strings.ReplaceAll(code, "VERSION_PLACEHOLDER", version)
	if omitFlag != "" {
		code = strings.ReplaceAll(code, omitFlag, "")
	}
	if err := os.WriteFile(src, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "rclone")
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, data)
	}
	return out
}

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{
		StateDir:            filepath.Join(root, "state"),
		RuntimeDir:          filepath.Join(root, "state", "runtime"),
		RuntimeStoreDir:     filepath.Join(root, "state", "runtimes"),
		ManagedConfigDir:    filepath.Join(root, "state", "config", "rclone"),
		ManagedRcloneConfig: filepath.Join(root, "state", "config", "rclone", "rclone.conf"),
		ProviderModuleDir:   filepath.Join(root, "provider"),
		FuseDevice:          filepath.Join(root, "fake-fuse"),
	}
	return p.Normalize()
}

func assertStoredRejectedOrBlocked(t *testing.T, p paths.Paths, req ImportRequest) Manifest {
	t.Helper()
	manifest, err := Import(context.Background(), p, req)
	var qerr *QualificationError
	if !errors.As(err, &qerr) {
		t.Fatalf("import should fail closed outside a complete real-device qualification environment, err=%v manifest=%+v", err, manifest)
	}
	if manifest.RuntimeID == "" || manifest.BinarySHA256 == "" || manifest.ArchiveSHA256 == "" {
		t.Fatalf("missing immutable identity/provenance: %+v", manifest)
	}
	if manifest.Qualification.Qualified {
		t.Fatalf("candidate was synthetically qualified: %+v", manifest.Qualification)
	}
	if _, err := os.Stat(BinaryPath(p, manifest.RuntimeID)); err != nil {
		t.Fatalf("stored candidate missing: %v", err)
	}
	if _, err := Inspect(p, manifest.RuntimeID); err != nil {
		t.Fatalf("stored manifest does not resolve to actual bytes: %v", err)
	}
	return manifest
}

func TestAllRequiredImportSourcesSnapshotIntoImmutableStore(t *testing.T) {
	binary := buildFixture(t, "rclone v1.99.0", "")
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()

	cases := []ImportRequest{
		{Engine: "rclone", SourceType: SourceLocalFile, Path: binary},
		{Engine: "rclone", SourceType: SourceExecutablePath, Path: binary},
		{Engine: "rclone", SourceType: SourceURL, URL: server.URL + "/rclone?token=secret"},
		{Engine: "bclone", SourceType: SourceGitHub, Repository: "owner/repo", ResolvedRef: "v1.2.3", AssetName: "bclone-arm64", AssetURL: server.URL + "/asset?token=secret"},
		{Engine: "custom", SourceType: SourceBuild, Path: binary, Repository: "owner/custom", ResolvedRef: "0123456789abcdef"},
		{Engine: "rclone", SourceType: SourceNewFuture, Path: binary, Repository: "NewFuture/rclone-fuse3-magisk", ResolvedRef: "deadbeef"},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		p := testPaths(t)
		manifest := assertStoredRejectedOrBlocked(t, p, tc)
		if manifest.Source.Type != tc.SourceType {
			t.Fatalf("source type=%q want %q", manifest.Source.Type, tc.SourceType)
		}
		if seen[manifest.RuntimeID] {
			t.Fatalf("distinct provenance unexpectedly collided: %s", manifest.RuntimeID)
		}
		seen[manifest.RuntimeID] = true
		if strings.Contains(manifest.Source.AssetURL, "token=") || strings.Contains(manifest.Source.AssetURL, "secret") {
			t.Fatalf("source provenance leaked URL credentials/query: %+v", manifest.Source)
		}
	}
}

func TestManifestBindsActualELFBytesAndMetadata(t *testing.T) {
	binary := buildFixture(t, "rclone v1.99.0", "")
	payload, _ := os.ReadFile(binary)
	digest := sha256.Sum256(payload)
	p := testPaths(t)
	m := assertStoredRejectedOrBlocked(t, p, ImportRequest{Engine: "rclone", SourceType: SourceLocalFile, Path: binary})
	want := hex.EncodeToString(digest[:])
	if m.BinarySHA256 != want || m.ArchiveSHA256 != want {
		t.Fatalf("hashes do not bind imported bytes: %+v want=%s", m, want)
	}
	if m.SchemaVersion != ManifestSchemaVersion || m.Qualifier != QualifierVersion || m.ELF.Class == "" || m.ELF.Machine == "" || m.ELF.Architecture == "" {
		t.Fatalf("manifest metadata incomplete: %+v", m)
	}
	if len(m.Qualification.Evidence) < 2 {
		t.Fatalf("qualification evidence not resolvable: %+v", m.Qualification)
	}
	for _, path := range m.Qualification.Evidence {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("evidence path does not exist: %s: %v", path, err)
		}
	}
}

func TestTruncatedELFFailsClosed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rclone")
	if err := os.WriteFile(file, []byte{0x7f, 'E', 'L', 'F', 2, 1}, 0o700); err != nil {
		t.Fatal(err)
	}
	p := testPaths(t)
	m, err := Import(context.Background(), p, ImportRequest{SourceType: SourceLocalFile, Path: file})
	if err == nil || m.Qualification.Qualified {
		t.Fatalf("truncated ELF accepted: err=%v %+v", err, m.Qualification)
	}
	found := false
	for _, check := range m.Qualification.Checks {
		if check.Name == "elf" && check.Status == "fail" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing ELF failure evidence: %+v", m.Qualification.Checks)
	}
}

func TestWrongArchitectureRuleFailsArm64Android(t *testing.T) {
	if err := architectureCompatible(true, "arm64", "amd64"); err == nil {
		t.Fatal("wrong architecture accepted on arm64 Android")
	}
	if err := architectureCompatible(false, "arm64", "amd64"); err != nil {
		t.Fatalf("host static inspection should not pretend to be Android qualification: %v", err)
	}
}

func TestFakeVersionAndUnsupportedFlagAreRejected(t *testing.T) {
	if versionPattern.MatchString("totally legit runtime") {
		t.Fatal("fake version output accepted")
	}
	binary := buildFixture(t, "rclone v1.99.0", "--rc-pass")
	pinned, err := openPinned(binary, "")
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	if err := cliContractCheck(context.Background(), pinned, t.TempDir()); err == nil || !strings.Contains(err.Error(), "--rc-pass") {
		t.Fatalf("unsupported global flag was not rejected: %v", err)
	}
}

func TestCommandSpecificAndGlobalHelpAreMerged(t *testing.T) {
	binary := buildFixture(t, "rclone v1.99.0", "")
	pinned, err := openPinned(binary, "")
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	if err := cliContractCheck(context.Background(), pinned, t.TempDir()); err != nil {
		t.Fatalf("split command/global help should satisfy contract: %v", err)
	}
}

func TestPinnedExecutionDefeatsPathReplacementBetweenHashAndLaunch(t *testing.T) {
	original := buildFixture(t, "rclone v1.11.0", "")
	replacement := buildFixture(t, "rclone v9.99.0", "")
	work := t.TempDir()
	path := filepath.Join(work, "rclone")
	data, _ := os.ReadFile(original)
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
	digest, _ := hashFile(path)
	pinned, err := openPinned(path, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	replacementData, _ := os.ReadFile(replacement)
	tmp := filepath.Join(work, "replacement")
	if err := os.WriteFile(tmp, replacementData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	out, err := runPinned(context.Background(), pinned, 8192, "version")
	if err != nil {
		t.Fatalf("pinned original failed after path replacement: %v output=%q", err, out)
	}
	if !strings.Contains(out, "v1.11.0") || strings.Contains(out, "v9.99.0") {
		t.Fatalf("path replacement redirected execution: %q", out)
	}
}

func TestPinnedExecutionSurvivesSourceDisappearanceButFinalPathHashDoesNot(t *testing.T) {
	binary := buildFixture(t, "rclone v1.22.0", "")
	digest, _ := hashFile(binary)
	pinned, err := openPinned(binary, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	out, err := runPinned(context.Background(), pinned, 8192, "version")
	if err != nil || !strings.Contains(out, "v1.22.0") {
		t.Fatalf("open inode did not remain stable: err=%v out=%q", err, out)
	}
	if _, err := hashFile(binary); !os.IsNotExist(err) {
		t.Fatalf("post-qualification path disappearance would not fail closed: %v", err)
	}
}

func waitForMarker(t *testing.T, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fixture marker did not appear: %s", marker)
}

func expectedRuntimePath(p paths.Paths, req ImportRequest, binary string) string {
	digest, _ := hashFile(binary)
	clean, _ := validateRequest(req)
	id := runtimeID(clean.Engine, digest, provenance(clean))
	return BinaryPath(p, id)
}

func TestProductionQualificationRejectsBinaryDisappearanceDuringRun(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skip("host adversarial timing test; real-device gate owns Android FUSE qualification")
	}
	binary := buildFixture(t, "rclone v1.33.0", "")
	p := testPaths(t)
	req := ImportRequest{Engine: "rclone", SourceType: SourceLocalFile, Path: binary}
	stored := expectedRuntimePath(p, req, binary)
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("RNEXUS_FIXTURE_MARKER", marker)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(marker); err == nil {
				_ = os.Remove(stored)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	m, err := Import(context.Background(), p, req)
	<-done
	if err == nil || m.Qualification.State != "rejected" {
		t.Fatalf("disappearing candidate did not fail closed: err=%v state=%s", err, m.Qualification.State)
	}
	found := false
	for _, check := range m.Qualification.Checks {
		if check.Name == "post_qualification_hash" && check.Status == "fail" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing post-hash disappearance evidence: %+v", m.Qualification.Checks)
	}
}

func TestProductionQualificationRejectsPathReplacementButPinnedLaunchUsesOriginal(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skip("host adversarial timing test; real-device gate owns Android FUSE qualification")
	}
	original := buildFixture(t, "rclone v1.44.0", "")
	replacement := buildFixture(t, "rclone v9.44.0", "")
	p := testPaths(t)
	req := ImportRequest{Engine: "rclone", SourceType: SourceLocalFile, Path: original}
	stored := expectedRuntimePath(p, req, original)
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("RNEXUS_FIXTURE_MARKER", marker)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(marker); err == nil {
				data, _ := os.ReadFile(replacement)
				tmp := stored + ".replacement"
				_ = os.WriteFile(tmp, data, 0o500)
				_ = os.Rename(tmp, stored)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	m, err := Import(context.Background(), p, req)
	<-done
	if err == nil || m.Qualification.State != "rejected" {
		t.Fatalf("replaced candidate did not fail closed: err=%v state=%s", err, m.Qualification.State)
	}
	versionWasOriginal := false
	postHashFailed := false
	for _, check := range m.Qualification.Checks {
		if check.Name == "version" && strings.Contains(check.Detail, "v1.44.0") {
			versionWasOriginal = true
		}
		if check.Name == "post_qualification_hash" && check.Status == "fail" {
			postHashFailed = true
		}
	}
	if !versionWasOriginal || !postHashFailed {
		t.Fatalf("TOCTOU defenses not proven: original=%t postHashFailed=%t checks=%+v", versionWasOriginal, postHashFailed, m.Qualification.Checks)
	}
}

func TestDiagnosticsSanitizationRedactsSecretsAndBoundsOutput(t *testing.T) {
	value := sanitizeDetail("token=abc123 password=hunter2 Authorization: bearer-secret " + strings.Repeat("x", 5000))
	if strings.Contains(value, "abc123") || strings.Contains(value, "hunter2") || strings.Contains(value, "bearer-secret") {
		t.Fatalf("secret leaked: %q", value)
	}
	if len(value) > 2100 {
		t.Fatalf("diagnostic detail not bounded: %d", len(value))
	}
}

func TestHostCannotManufactureQualifiedCandidate(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skip("host-only assertion; Android execution is covered by real-device qualification")
	}
	binary := buildFixture(t, "rclone v1.99.0", "")
	p := testPaths(t)
	m := assertStoredRejectedOrBlocked(t, p, ImportRequest{SourceType: SourceLocalFile, Path: binary})
	if m.Qualification.State != "blocked_by_environment" {
		t.Fatalf("non-Android host should be explicitly blocked, got %s", m.Qualification.State)
	}
}

func TestImportExpectedSHA256FailsBeforeCandidatePublication(t *testing.T) {
	binary := buildFixture(t, "rclone v1.99.0", "")
	p := testPaths(t)
	_, err := Import(context.Background(), p, ImportRequest{SourceType: SourceLocalFile, Path: binary, ExpectedSHA256: strings.Repeat("0", 64), ResolutionID: "src-test"})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected hash mismatch did not fail closed: %v", err)
	}
	entries, readErr := os.ReadDir(p.RuntimeStoreDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".candidate-") {
			t.Fatalf("hash mismatch published candidate: %s", entry.Name())
		}
	}
}

func zipBytes(t *testing.T, entries map[string][]byte, symlink string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, payload := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if symlink != "" {
		h := &zip.FileHeader{Name: symlink, Method: zip.Store}
		h.SetMode(os.ModeSymlink | 0o777)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("../../outside"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestZipReleaseAssetExtractsExecutableAndBindsArchiveAndBinaryDigests(t *testing.T) {
	binary := buildFixture(t, "rclone v1.99.0", "")
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	archive := zipBytes(t, map[string][]byte{"rclone-v1-linux-arm64/rclone": payload}, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	p := testPaths(t)
	manifest := assertStoredRejectedOrBlocked(t, p, ImportRequest{Engine: "rclone", SourceType: SourceGitHub, Repository: "owner/repo", ResolvedRef: "deadbeef", AssetName: "rclone-v1-linux-arm64.zip", AssetURL: server.URL + "/asset"})
	archiveSum := sha256.Sum256(archive)
	binarySum := sha256.Sum256(payload)
	if manifest.ArchiveSHA256 != hex.EncodeToString(archiveSum[:]) {
		t.Fatalf("archive digest=%s", manifest.ArchiveSHA256)
	}
	if manifest.BinarySHA256 != hex.EncodeToString(binarySum[:]) {
		t.Fatalf("binary digest=%s", manifest.BinarySHA256)
	}
	if manifest.ArchiveSHA256 == manifest.BinarySHA256 {
		t.Fatal("archive and extracted binary identity collapsed")
	}
}

func TestZipReleaseRejectsTraversalSymlinkAndMalformedArchives(t *testing.T) {
	binary := buildFixture(t, "rclone v1.99.0", "")
	payload, _ := os.ReadFile(binary)
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{"traversal", zipBytes(t, map[string][]byte{"../rclone": payload}, ""), "escapes archive root"},
		{"symlink", zipBytes(t, map[string][]byte{"safe/rclone": payload}, "safe/link"), "contains symlink"},
		{"malformed", []byte("not-a-zip"), "malformed runtime archive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tc.body) }))
			defer server.Close()
			p := testPaths(t)
			_, err := Import(context.Background(), p, ImportRequest{Engine: "rclone", SourceType: SourceGitHub, Repository: "owner/repo", ResolvedRef: "deadbeef", AssetName: "rclone.zip", AssetURL: server.URL + "/asset"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
			items, listErr := List(p)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(items) != 0 {
				t.Fatalf("rejected archive published candidates: %+v", items)
			}
		})
	}
}

func writeGCFixture(t *testing.T, p paths.Paths, id string, imported int64) {
	t.Helper()
	dir := filepath.Join(p.RuntimeStoreDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := []byte("fixture-" + id)
	if err := os.WriteFile(filepath.Join(dir, "rclone"), binary, 0o500); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:])
	m := Manifest{SchemaVersion: ManifestSchemaVersion, RuntimeID: id, Engine: "rclone", ArchiveSHA256: digest, BinarySHA256: digest, ImportedUnixMS: imported, Qualifier: QualifierVersion, Qualification: Qualification{QualifierVersion: QualifierVersion, State: "qualified", Qualified: true, BinarySHA256: digest}}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGarbageCollectPreservesProtectedAndBoundsUnprotectedHistory(t *testing.T) {
	p := testPaths(t)
	writeGCFixture(t, p, "rclone-a", 1)
	writeGCFixture(t, p, "rclone-b", 2)
	writeGCFixture(t, p, "rclone-c", 3)
	writeGCFixture(t, p, "rclone-d", 4)
	result, err := GarbageCollect(p, []string{"rclone-a", "rclone-d"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.RuntimeStoreDir, "rclone-a")); err != nil {
		t.Fatal("protected previous removed")
	}
	if _, err := os.Stat(filepath.Join(p.RuntimeStoreDir, "rclone-d")); err != nil {
		t.Fatal("protected active removed")
	}
	if _, err := os.Stat(filepath.Join(p.RuntimeStoreDir, "rclone-c")); err != nil {
		t.Fatal("newest retained history removed")
	}
	if _, err := os.Stat(filepath.Join(p.RuntimeStoreDir, "rclone-b")); !os.IsNotExist(err) {
		t.Fatalf("old history survived: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "rclone-b" {
		t.Fatalf("unexpected GC result: %+v", result)
	}
}

func TestInterruptedDownloadPublishesNoCandidate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		if f, ok := w.(http.Flusher); ok {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			f.Flush()
		}
		<-release
	}))
	defer server.Close()
	p := testPaths(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Import(ctx, p, ImportRequest{Engine: "rclone", SourceType: SourceURL, URL: server.URL + "/rclone"})
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	close(release)
	if err == nil {
		t.Fatal("cancelled download succeeded")
	}
	items, listErr := List(p)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(items) != 0 {
		t.Fatalf("interrupted download published candidates: %+v", items)
	}
}

func TestAcquirePublishesPendingCandidateWithoutRunningQualifier(t *testing.T) {
	p := testPaths(t)
	// Acquisition must be independent from qualification, so this test does not
	// need a runnable rclone fixture.  Reuse the already-built Go test executable
	// as immutable bytes instead of spawning a nested `go build`, which makes the
	// policy regression deterministic on native Termux/Android validators.
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Acquire(context.Background(), p, ImportRequest{Engine: "rclone", SourceType: SourceLocalFile, Path: binary})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RuntimeID == "" || manifest.BinarySHA256 == "" {
		t.Fatalf("acquire did not publish immutable candidate identity: %+v", manifest)
	}
	if manifest.Qualification.State != "pending" || manifest.Qualification.Qualified {
		t.Fatalf("acquire unexpectedly ran qualification: %+v", manifest.Qualification)
	}
	stored, err := Inspect(p, manifest.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Qualification.State != "pending" || stored.Qualification.Qualified {
		t.Fatalf("stored acquired candidate is not pending: %+v", stored.Qualification)
	}
}
