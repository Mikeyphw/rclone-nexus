package runtimestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
