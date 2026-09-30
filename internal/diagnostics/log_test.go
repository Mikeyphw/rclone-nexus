package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"rclone-nexus/internal/paths"
)

func TestSanitizeTextSecretsAndPaths(t *testing.T) {
	text := "password=hunter2 token:abc Authorization: Bearer zzz path=/data/adb/rclone/conf/rclone.conf"
	got := SanitizeText(text, "/data/adb/rclone")
	for _, forbidden := range []string{"hunter2", "abc", "zzz", "/data/adb/rclone"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("secret/private path leaked: %q in %q", forbidden, got)
		}
	}
}

func TestRotateBoundedBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Rotate(path, 20, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("y", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Rotate(path, 20, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatal(err)
	}
}

func TestAppendRepairsPrivateFileModeAndOwnership(t *testing.T) {
	base := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(base, "state")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.DiagnosticsDir, "events.jsonl")
	if err := os.WriteFile(path, []byte("old\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 12345, 12345); err != nil {
			t.Fatal(err)
		}
	}
	if err := Append(p, "gate", "permissions", "ok", "", map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
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
