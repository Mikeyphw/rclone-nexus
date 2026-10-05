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
	m := Manifest{SchemaVersion: 1, State: "complete", SourceID: "bclone", Engine: "bclone", Repository: "BenjiThatFoxGuy/bclone", RequestedRef: "main", ResolvedCommit: strings.Repeat("a", 40), GoVersion: "go1.27.1", GoModSHA256: strings.Repeat("d", 64), GoSumSHA256: strings.Repeat("e", 64), GoModuleGraphSHA256: strings.Repeat("f", 64), GoModuleCount: 2, GoModuleMode: "readonly", GoWorkspaceMode: "off", GoModuleCacheScope: "isolated-ephemeral", NDKVersion: "28.2.13676358", Compiler: "/ndk/bin/aarch64-linux-android21-clang", CompilerVersion: "clang 19", APILevel: 21, GOOS: "android", GOARCH: "arm64", ABI: "arm64-v8a", CGOEnabled: true, Tags: []string{"android"}, Trimpath: true, BinaryName: "bclone-android-arm64", BinarySHA256: digest(binary), BinarySize: int64(len(binary)), ProducedUnixMS: 1}
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

func TestNativeClangNDKSysrootProvenanceAccepted(t *testing.T) {
	dir := writeFixture(t, func(m *Manifest) {
		m.Compiler = "/data/data/com.termux/files/usr/bin/clang"
		m.CompilerMode = "native-clang-ndk-sysroot"
		m.CompilerTarget = "aarch64-linux-android21"
		m.NDKHost = "linux-x86_64"
		m.NDKSysroot = "/sdk/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64/sysroot"
		m.CompilerResourceDir = "/sdk/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64/lib/clang/19"
		m.CompilerRTBuiltinsSHA256 = strings.Repeat("b", 64)
		m.CompilerLibunwindSHA256 = strings.Repeat("c", 64)
		m.AndroidSystemLibraries = []string{"log"}
	}, minimalAndroidARM64ELF())
	if _, err := VerifyBundle(dir); err != nil {
		t.Fatalf("native clang + pinned NDK sysroot provenance rejected: %v", err)
	}
}

func TestNativeClangNDKResourceDirRequired(t *testing.T) {
	dir := writeFixture(t, func(m *Manifest) {
		m.Compiler = "/data/data/com.termux/files/usr/bin/clang"
		m.CompilerMode = "native-clang-ndk-sysroot"
		m.CompilerTarget = "aarch64-linux-android21"
		m.NDKHost = "linux-x86_64"
		m.NDKSysroot = "/sdk/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64/sysroot"
	}, minimalAndroidARM64ELF())
	if _, err := VerifyBundle(dir); err == nil || !strings.Contains(err.Error(), "resource-dir provenance") {
		t.Fatalf("want resource-dir provenance rejection, got %v", err)
	}
}

func TestNativeClangNDKSysrootWrongTargetRejected(t *testing.T) {
	dir := writeFixture(t, func(m *Manifest) {
		m.Compiler = "/data/data/com.termux/files/usr/bin/clang"
		m.CompilerMode = "native-clang-ndk-sysroot"
		m.CompilerTarget = "aarch64-linux-android24"
		m.NDKHost = "linux-x86_64"
		m.NDKSysroot = "/sdk/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64/sysroot"
		m.CompilerResourceDir = "/sdk/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64/lib/clang/19"
		m.CompilerRTBuiltinsSHA256 = strings.Repeat("b", 64)
		m.CompilerLibunwindSHA256 = strings.Repeat("c", 64)
		m.AndroidSystemLibraries = []string{"log"}
	}, minimalAndroidARM64ELF())
	if _, err := VerifyBundle(dir); err == nil || !strings.Contains(err.Error(), "target does not match") {
		t.Fatalf("want native target rejection, got %v", err)
	}
}

func TestNativeClangAndroidLiblogProvenanceRequired(t *testing.T) {
	dir := writeFixture(t, func(m *Manifest) {
		m.Compiler = "/data/data/com.termux/files/usr/bin/clang"
		m.CompilerMode = "native-clang-ndk-sysroot"
		m.CompilerTarget = "aarch64-linux-android21"
		m.NDKHost = "linux-x86_64"
		m.NDKSysroot = "/sdk/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64/sysroot"
		m.CompilerResourceDir = "/sdk/ndk/28.2.13676358/toolchains/llvm/prebuilt/linux-x86_64/lib/clang/19"
		m.CompilerRTBuiltinsSHA256 = strings.Repeat("b", 64)
		m.CompilerLibunwindSHA256 = strings.Repeat("c", 64)
		m.AndroidSystemLibraries = nil
	}, minimalAndroidARM64ELF())
	if _, err := VerifyBundle(dir); err == nil || !strings.Contains(err.Error(), "liblog linkage provenance") {
		t.Fatalf("want liblog provenance rejection, got %v", err)
	}
}

func TestGoModuleIsolationProvenanceRequired(t *testing.T) {
	dir := writeFixture(t, func(m *Manifest) {
		m.GoModuleCacheScope = "host-global"
	}, minimalAndroidARM64ELF())
	if _, err := VerifyBundle(dir); err == nil || !strings.Contains(err.Error(), "isolated/read-only") {
		t.Fatalf("want isolated module provenance rejection, got %v", err)
	}
}

func TestGoModuleGraphDigestRequired(t *testing.T) {
	dir := writeFixture(t, func(m *Manifest) {
		m.GoModuleGraphSHA256 = ""
	}, minimalAndroidARM64ELF())
	if _, err := VerifyBundle(dir); err == nil || !strings.Contains(err.Error(), "Go module graph SHA-256") {
		t.Fatalf("want module graph digest rejection, got %v", err)
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

func minimalAndroidARM64ELF() []byte {
	interp := []byte("/system/bin/linker64\x00")
	b := make([]byte, 128+len(interp))
	copy(b[:4], []byte{0x7f, 'E', 'L', 'F'})
	b[4], b[5], b[6] = 2, 1, 1
	binary.LittleEndian.PutUint16(b[16:18], 2)
	binary.LittleEndian.PutUint16(b[18:20], 183)
	binary.LittleEndian.PutUint32(b[20:24], 1)
	binary.LittleEndian.PutUint64(b[32:40], 64)
	binary.LittleEndian.PutUint16(b[52:54], 64)
	binary.LittleEndian.PutUint16(b[54:56], 56)
	binary.LittleEndian.PutUint16(b[56:58], 1)
	binary.LittleEndian.PutUint32(b[64:68], 3)
	binary.LittleEndian.PutUint64(b[72:80], 128)
	binary.LittleEndian.PutUint64(b[96:104], uint64(len(interp)))
	binary.LittleEndian.PutUint64(b[104:112], uint64(len(interp)))
	copy(b[128:], interp)
	return b
}
