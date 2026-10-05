package githubapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
)

const APIVersion = "2022-11-28"

type HTTPError struct {
	StatusCode        int
	Message           string
	RateLimited       bool
	Remaining         int64
	ResetUnix         int64
	RetryAfterSeconds int64
	Authenticated     bool
}

func (e *HTTPError) Error() string {
	if e == nil {
		return "GitHub API request failed"
	}
	if e.RateLimited {
		reset := "unknown"
		if e.ResetUnix > 0 {
			reset = time.Unix(e.ResetUnix, 0).UTC().Format(time.RFC3339)
		}
		return fmt.Sprintf("GitHub API rate limited (HTTP %d, remaining=%d, reset=%s, authenticated=%t)", e.StatusCode, e.Remaining, reset, e.Authenticated)
	}
	if e.Message != "" {
		return fmt.Sprintf("GitHub API request failed with HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("GitHub API request failed with HTTP %d", e.StatusCode)
}
func (e *HTTPError) Timeout() bool { return false }
func (e *HTTPError) Temporary() bool {
	return e != nil && (e.RateLimited || e.StatusCode == 429 || e.StatusCode >= 500)
}

func Token(p paths.Paths) (string, error) {
	for _, name := range []string{"RNEXUS_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	file := p.Normalize().GitHubTokenFile
	info, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("GitHub token file must be a regular file, not a symlink")
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 || perm&0o100 != 0 || perm&0o400 == 0 {
		return "", errors.New("GitHub token file permissions must be owner-readable and not group/world accessible or executable")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsAny(token, "\r\n\x00") {
		return "", errors.New("GitHub token file is empty or malformed")
	}
	return token, nil
}

func Prepare(req *http.Request, token, userAgent string) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func ResponseError(resp *http.Response, authenticated bool) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	parseInt := func(name string, fallback int64) int64 {
		v, err := strconv.ParseInt(strings.TrimSpace(resp.Header.Get(name)), 10, 64)
		if err != nil {
			return fallback
		}
		return v
	}
	remaining := parseInt("X-RateLimit-Remaining", -1)
	reset := parseInt("X-RateLimit-Reset", 0)
	retry := parseInt("Retry-After", 0)
	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body)
	msg := strings.TrimSpace(body.Message)
	low := strings.ToLower(msg)
	limited := (resp.StatusCode == 403 || resp.StatusCode == 429) && (remaining == 0 || retry > 0 || strings.Contains(low, "rate limit") || strings.Contains(low, "secondary rate"))
	return &HTTPError{StatusCode: resp.StatusCode, Message: msg, RateLimited: limited, Remaining: remaining, ResetUnix: reset, RetryAfterSeconds: retry, Authenticated: authenticated}
}
