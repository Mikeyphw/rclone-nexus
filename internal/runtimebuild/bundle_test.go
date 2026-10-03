package runtimebuild

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func writeFixture(t *testing.T, mutate func(*Manifest), binary []byte) string {
	t.Helper()
	dir := t.TempDir()
	if binary == nil {
		binary = []byte{0x7f, 'E', 'L', 'F'}
	}
	m := Manifest{SchemaVersion: 1, State: "complete", SourceID: "bclone", Engine: "bclone", Repository: "BenjiThatFoxGuy/bclone", RequestedRef: "main", ResolvedCommit: strings.Repeat("a", 40), GoVersion: "go1.27.1", NDKVersion: "28.2.13676358", Compiler: "/ndk/bin/aarch64-linux-android21-clang", CompilerVersion: "clang 19", APILevel: 21, GOOS: "android", GOARCH: "arm64", ABI: "arm64-v8a", CGOEnabled: true, Tags: []string{"android"}, Trimpath: true, BinaryName: "bclone-android-arm64", BinarySHA256: digest(binary), BinarySize: int64(len(binary)), ProducedUnixMS: 1}
	if mutate != nil {
		mutate(&m)
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	mb = append(mb, '\n')
	os.WriteFile(filepath.Join(dir, m.BinaryName), binary, 0o755)
	os.WriteFile(filepath.Join(dir, "provenance.json"), mb, 0o644)
	sums := digest(binary) + "  " + m.BinaryName + "\n" + digest(mb) + "  provenance.json\n"
	os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644)
	return dir
}

func TestUnpinnedMutableResultRejected(t *testing.T) {
	dir := writeFixture(t, func(m *Manifest) { m.ResolvedCommit = "main" }, nil)
	if _, e := VerifyBundle(dir); e == nil || !strings.Contains(e.Error(), "40-hex") {
		t.Fatalf("want immutable commit rejection, got %v", e)
	}
}
func TestPartialBundleRejected(t *testing.T) {
	dir := writeFixture(t, nil, nil)
	os.Remove(filepath.Join(dir, "SHA256SUMS"))
	if _, e := VerifyBundle(dir); e == nil || !strings.Contains(e.Error(), "partial build bundle") {
		t.Fatalf("want partial rejection, got %v", e)
	}
}
func TestManifestHashMismatchRejected(t *testing.T) {
	dir := writeFixture(t, nil, nil)
	p := filepath.Join(dir, "provenance.json")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(" \n")
	f.Close()
	if _, e := VerifyBundle(dir); e == nil || !strings.Contains(e.Error(), "provenance manifest SHA-256 mismatch") {
		t.Fatalf("want manifest hash rejection, got %v", e)
	}
}
func TestWrongABIRejectedBeforeELF(t *testing.T) {
	dir := writeFixture(t, func(m *Manifest) { m.GOARCH = "amd64"; m.ABI = "x86_64" }, nil)
	if _, e := VerifyBundle(dir); e == nil || !strings.Contains(e.Error(), "Android arm64-v8a") {
		t.Fatalf("want ABI rejection, got %v", e)
	}
}
func minimalLinuxARM64ELF() []byte {
	b := make([]byte, 64)
	copy(b[:4], []byte{0x7f, 'E', 'L', 'F'})
	b[4], b[5], b[6] = 2, 1, 1
	binary.LittleEndian.PutUint16(b[16:18], 2)
	binary.LittleEndian.PutUint16(b[18:20], 183)
	binary.LittleEndian.PutUint32(b[20:24], 1)
	binary.LittleEndian.PutUint16(b[52:54], 64)
	binary.LittleEndian.PutUint16(b[54:56], 56)
	binary.LittleEndian.PutUint16(b[58:60], 64)
	return b
}

func TestLinuxARM64ClaimingAndroidRejectedByInterpreter(t *testing.T) {
	dir := writeFixture(t, nil, minimalLinuxARM64ELF())
	if _, e := VerifyBundle(dir); e == nil || !strings.Contains(e.Error(), "Android arm64 interpreter") {
		t.Fatalf("want Android interpreter rejection, got %v", e)
	}
}

func TestSymlinkBundleMemberRejected(t *testing.T) {
	dir := writeFixture(t, nil, minimalLinuxARM64ELF())
	target := filepath.Join(dir, "real-provenance.json")
	if err := os.Rename(filepath.Join(dir, "provenance.json"), target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "provenance.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBundle(dir); err == nil || !strings.Contains(err.Error(), "non-symlink regular file") {
		t.Fatalf("want symlink rejection, got %v", err)
	}
}
