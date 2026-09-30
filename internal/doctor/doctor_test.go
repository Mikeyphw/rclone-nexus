package doctor

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
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
