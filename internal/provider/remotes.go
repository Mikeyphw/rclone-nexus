package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/redact"
)

type RemoteEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

type BrowseResult struct {
	Remote    string        `json:"remote"`
	Path      string        `json:"path"`
	Entries   []RemoteEntry `json:"entries"`
	Truncated bool          `json:"truncated"`
}

func validRemoteName(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func command(ctx context.Context, p paths.Paths, args ...string) ([]byte, error) {
	bin, err := FindRclone(p)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append(args, "--config", p.Normalize().RcloneConfig)...)
	out := &limitedBuffer{remaining: 1 << 20}
	stderr := &limitedBuffer{remaining: 32 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rclone query failed: %s", redact.BoundedString(stderr.String(), 2048))
	}
	return []byte(out.String()), nil
}

func ListRemotes(ctx context.Context, p paths.Paths) ([]string, error) {
	data, err := command(ctx, p, "listremotes")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSuffix(strings.TrimSpace(line), ":")
		if !validRemoteName(name) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) > 256 {
		out = out[:256]
	}
	return out, nil
}

func Browse(ctx context.Context, p paths.Paths, remote, subpath string, limit int) (BrowseResult, error) {
	if !validRemoteName(remote) {
		return BrowseResult{}, fmt.Errorf("invalid remote name")
	}
	if strings.ContainsAny(subpath, "\x00\r\n") || strings.Contains(subpath, ":") || strings.HasPrefix(strings.TrimSpace(subpath), "-") {
		return BrowseResult{}, fmt.Errorf("invalid remote path")
	}
	subpath = strings.Trim(strings.TrimSpace(subpath), "/")
	remotes, err := ListRemotes(ctx, p)
	if err != nil {
		return BrowseResult{}, err
	}
	found := false
	for _, n := range remotes {
		if n == remote {
			found = true
			break
		}
	}
	if !found {
		return BrowseResult{}, fmt.Errorf("remote is not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 300 {
		limit = 300
	}
	target := remote + ":"
	if subpath != "" {
		target += subpath
	}
	data, err := command(ctx, p, "lsjson", target, "--max-depth", "1", "--no-modtime")
	if err != nil {
		return BrowseResult{}, err
	}
	var raw []struct {
		Name  string `json:"Name"`
		Path  string `json:"Path"`
		IsDir bool   `json:"IsDir"`
		Size  int64  `json:"Size"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return BrowseResult{}, fmt.Errorf("invalid rclone browse response")
	}
	result := BrowseResult{Remote: remote, Path: subpath}
	for i, item := range raw {
		if i >= limit {
			result.Truncated = true
			break
		}
		name := filepath.Base(strings.TrimSpace(item.Name))
		if name == "." || name == "/" || name == "" {
			continue
		}
		result.Entries = append(result.Entries, RemoteEntry{Name: redact.BoundedString(name, 512), Path: redact.BoundedString(strings.Trim(strings.TrimSpace(item.Path), "/"), 2048), IsDir: item.IsDir, Size: item.Size})
	}
	return result, nil
}
