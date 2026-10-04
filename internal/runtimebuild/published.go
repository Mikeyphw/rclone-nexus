package runtimebuild

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimesource"
	"rclone-nexus/internal/runtimestore"
)

const PublishedBuildContract = "v1"
const maxPublishedBundleBytes = int64(600 << 20)

type PublishedBuildUnavailableError struct{ URL string }

func (e *PublishedBuildUnavailableError) Error() string {
	return "SOURCE-X02 build result is not published yet: " + e.URL
}
func (e *PublishedBuildUnavailableError) Timeout() bool   { return false }
func (e *PublishedBuildUnavailableError) Temporary() bool { return true }

func PublishedTag(sourceID, commit string) (string, error) {
	sourceID = strings.TrimSpace(sourceID)
	commit = strings.ToLower(strings.TrimSpace(commit))
	if sourceID == "" || !fullCommitRE.MatchString(commit) || strings.ContainsAny(sourceID, "/\\\x00\r\n") {
		return "", errors.New("invalid published build identity")
	}
	for _, r := range sourceID {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return "", errors.New("invalid published build source ID")
	}
	return "runtime-build-" + PublishedBuildContract + "-" + sourceID + "-" + commit, nil
}

func PublishedURL(buildRepository, sourceID, commit string) (string, error) {
	if !cleanRepo(buildRepository) {
		return "", errors.New("invalid SOURCE-X02 build repository")
	}
	tag, err := PublishedTag(sourceID, commit)
	if err != nil {
		return "", err
	}
	return "https://github.com/" + buildRepository + "/releases/download/" + tag + "/runtime-source-build.tar", nil
}

func trustedPublishedClient(initial *url.URL) *http.Client {
	c := &http.Client{Timeout: 2 * time.Minute}
	c.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("published build redirected too many times")
		}
		if next.URL.Scheme != "https" && !(initial != nil && initial.Scheme == "http" && (initial.Hostname() == "127.0.0.1" || initial.Hostname() == "localhost" || initial.Hostname() == "::1")) {
			return errors.New("published build redirect downgraded transport")
		}
		if initial != nil && initial.Hostname() == "github.com" {
			allowed := map[string]bool{"github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true, "github-releases.githubusercontent.com": true}
			if !allowed[strings.ToLower(next.URL.Hostname())] {
				return errors.New("published build redirect escaped trusted GitHub domains")
			}
		} else if initial != nil && !strings.EqualFold(initial.Host, next.URL.Host) {
			return errors.New("published build redirect escaped trusted origin")
		}
		return nil
	}
	return c
}

func extractPublishedTar(r io.Reader, dir string) error {
	tr := tar.NewReader(io.LimitReader(r, maxPublishedBundleBytes+1))
	var total int64
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid published build tar: %w", err)
		}
		name := filepath.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." || filepath.Base(name) != name || strings.HasPrefix(name, "../") || filepath.IsAbs(name) {
			return errors.New("published build tar path escapes bundle root")
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return errors.New("published build tar contains non-regular entry")
		}
		if seen[name] {
			return errors.New("published build tar contains duplicate entry")
		}
		seen[name] = true
		if h.Size < 0 || h.Size > maxPublishedBundleBytes || total+h.Size > maxPublishedBundleBytes {
			return errors.New("published build tar exceeds size limit")
		}
		total += h.Size
		out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(tr, h.Size+1))
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != h.Size {
			return errors.New("published build tar entry size mismatch")
		}
	}
	return nil
}

func AcquirePublishedFromURL(ctx context.Context, p paths.Paths, upstream runtimesource.Resolution, rawURL string) (runtimestore.Manifest, runtimesource.Resolution, error) {
	if upstream.Kind != runtimesource.KindGitHub || upstream.CommitSHA == "" {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, errors.New("published build requires immutable GitHub resolution")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, errors.New("published build URL is invalid")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "rclone-nexus-runtime-build")
	resp, err := trustedPublishedClient(u).Do(req)
	if err != nil {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, &PublishedBuildUnavailableError{URL: u.String()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, fmt.Errorf("published build download failed with HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxPublishedBundleBytes {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, errors.New("published build exceeds size limit")
	}
	dir, err := os.MkdirTemp("", "rclone-nexus-published-build-*")
	if err != nil {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, err
	}
	defer os.RemoveAll(dir)
	if err := extractPublishedTar(resp.Body, dir); err != nil {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, err
	}
	bundle, err := VerifyBundle(dir)
	if err != nil {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, err
	}
	m := bundle.Manifest
	if m.SourceID != upstream.SourceID || !strings.EqualFold(m.Engine, upstream.Engine) || !strings.EqualFold(m.Repository, upstream.Repository) || !strings.EqualFold(m.ResolvedCommit, upstream.CommitSHA) {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, errors.New("published build provenance does not match immutable upstream resolution")
	}
	return AcquireBundle(ctx, p, dir)
}

func AcquirePublished(ctx context.Context, p paths.Paths, upstream runtimesource.Resolution) (runtimestore.Manifest, runtimesource.Resolution, error) {
	if upstream.BuildRepository == "" {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, errors.New("source resolution has no SOURCE-X02 build repository")
	}
	raw, err := PublishedURL(upstream.BuildRepository, upstream.SourceID, upstream.CommitSHA)
	if err != nil {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, err
	}
	return AcquirePublishedFromURL(ctx, p, upstream, raw)
}
