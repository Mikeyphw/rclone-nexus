package githubapi

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rclone-nexus/internal/paths"
)

func TestTokenPrefersEnvironmentAndRejectsLooseFileMode(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{StateDir: root, ConfigDir: filepath.Join(root, "config")}.Normalize()
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.GitHubTokenFile, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Do not rely on process umask to create an intentionally unsafe fixture.
	if err := os.Chmod(p.GitHubTokenFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(p.GitHubTokenFile); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("fixture mode=%#o, want 0644", got)
	}
	if _, err := Token(p); err == nil {
		t.Fatal("loose token file mode accepted")
	}
	t.Setenv("RNEXUS_GITHUB_TOKEN", "env-token")
	got, err := Token(p)
	if err != nil || got != "env-token" {
		t.Fatalf("token=%q err=%v", got, err)
	}
}

func TestTokenAcceptsPrivateRegularFile(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{StateDir: root, ConfigDir: filepath.Join(root, "config")}.Normalize()
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.GitHubTokenFile, []byte("private-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.GitHubTokenFile, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Token(p)
	if err != nil || got != "private-token" {
		t.Fatalf("token=%q err=%v", got, err)
	}
}

func TestTokenRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{StateDir: root, ConfigDir: filepath.Join(root, "config")}.Normalize()
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "token-target")
	if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p.GitHubTokenFile); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Token(p); err == nil {
		t.Fatal("symlink token file accepted")
	}
}

func TestResponseErrorExposesRateLimitWithoutSecrets(t *testing.T) {
	resp := &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"API rate limit exceeded"}`))}
	resp.Header.Set("X-RateLimit-Remaining", "0")
	resp.Header.Set("X-RateLimit-Reset", "1791159999")
	err := ResponseError(resp, false)
	got, ok := err.(*HTTPError)
	if !ok || !got.RateLimited || got.Remaining != 0 || got.ResetUnix != 1791159999 {
		t.Fatalf("unexpected error: %#v", err)
	}
	if strings.Contains(err.Error(), "token") {
		t.Fatalf("error leaks auth detail: %v", err)
	}
}
