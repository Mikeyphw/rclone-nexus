package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
