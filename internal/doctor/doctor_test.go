package doctor

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"rclone-nexus/internal/paths"
)

func TestSupportBundleIsDeterministicForSameInputs(t *testing.T) {
	base := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(base, "state"), ModuleDir: filepath.Join(base, "module"), ProviderModuleDir: filepath.Join(base, "provider"), FuseDevice: "/dev/null"}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_TEST_NOW_MS", "1234567890")
	one, err := BuildBundle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	two, err := BuildBundle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if one.SHA256 != two.SHA256 {
		t.Fatalf("bundle not deterministic: %s != %s", one.SHA256, two.SHA256)
	}
}

func TestSupportBundleRedactsSecretsAndPrivatePaths(t *testing.T) {
	base := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(base, "state"), ModuleDir: filepath.Join(base, "module"), ProviderModuleDir: filepath.Join(base, "provider"), FuseDevice: "/dev/null"}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.ProviderModuleDir, "conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	p.RcloneConfig = filepath.Join(p.ProviderModuleDir, "conf", "rclone.conf")
	_ = os.WriteFile(p.RcloneConfig, []byte("[x]\ntoken=SUPERSECRET\n"), 0o600)
	_ = os.WriteFile(filepath.Join(p.LogDir, "test.log"), []byte("password=hunter2 token=abc path="+p.StateDir), 0o600)
	result, err := BuildBundle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(p.SupportDir, result.Filename))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		all.Write(b)
	}
	for _, forbidden := range []string{"hunter2", "SUPERSECRET", "token=abc", p.StateDir, p.ProviderModuleDir} {
		if strings.Contains(all.String(), forbidden) {
			t.Fatalf("bundle leaked %q", forbidden)
		}
	}
}

func TestSupportBundleRepairsPrivateModeAndOwnership(t *testing.T) {
	base := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(base, "state"), ModuleDir: filepath.Join(base, "module"), ProviderModuleDir: filepath.Join(base, "provider"), FuseDevice: "/dev/null"}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_TEST_NOW_MS", "1234567890")
	first, err := BuildBundle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.SupportDir, first.Filename)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 12345, 12345); err != nil {
			t.Fatal(err)
		}
	}
	second, err := BuildBundle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if first.Filename != second.Filename {
		t.Fatalf("deterministic bundle filename changed: %s != %s", first.Filename, second.Filename)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%o want=600", got)
	}
	if os.Geteuid() == 0 {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 {
			t.Fatalf("owner=%+v want root:root", info.Sys())
		}
	}
}

func TestBundleBytesIsFixedAndBounded(t *testing.T) {
	base := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(base, "state"), ModuleDir: filepath.Join(base, "module"), ProviderModuleDir: filepath.Join(base, "provider"), FuseDevice: "/dev/null"}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	result, err := BuildBundle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	meta, data, err := BundleBytes(p, result.BundleID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SHA256 != result.SHA256 || len(data) == 0 {
		t.Fatalf("bad bundle read: %+v", meta)
	}
	if result.Size > maxSupportBundleBytes {
		t.Fatalf("generated bundle exceeds read budget: %d", result.Size)
	}
	if _, _, err := BundleBytes(p, "../../etc/passwd"); err == nil {
		t.Fatal("path-like bundle id accepted")
	}
}
