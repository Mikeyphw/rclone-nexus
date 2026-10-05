package runtimesource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"net/url"
	"path"
	"rclone-nexus/internal/githubapi"
	"strconv"
	"strings"
)

type githubRepository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}
type githubRelease struct {
	ID         int64         `json:"id"`
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}
type githubAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}
type githubRef struct {
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"object"`
}
type githubTag struct {
	Object struct {
		SHA  string `json:"sha"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"object"`
}
type githubCommit struct {
	SHA string `json:"sha"`
}

type githubResolution struct {
	RepositoryID int64
	RequestedRef string
	ReleaseID    int64
	ReleaseTag   string
	CommitSHA    string
	Asset        *Asset
}

func (g *GitHubResolver) requestURL(rel string) (*url.URL, error) {
	if g == nil || g.apiBase == nil {
		return nil, errors.New("GitHub resolver unavailable")
	}
	base := *g.apiBase
	base.Path = strings.TrimRight(base.Path, "/") + rel
	base.RawQuery = ""
	return &base, nil
}

func (g *GitHubResolver) getJSON(ctx context.Context, rel string, out any) error {
	target, err := g.requestURL(rel)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	githubapi.Prepare(req, g.token, "rclone-nexus-source-x01")
	clientCopy := *g.client
	originalRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != target.Scheme || !strings.EqualFold(req.URL.Host, target.Host) {
			return errors.New("GitHub metadata redirect escaped trusted API origin")
		}
		if originalRedirect != nil {
			return originalRedirect(req, via)
		}
		if len(via) >= 5 {
			return errors.New("too many GitHub metadata redirects")
		}
		return nil
	}
	resp, err := clientCopy.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := githubapi.ResponseError(resp, g.token != ""); err != nil {
		return err
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

func escRepo(repo string) string {
	parts := strings.Split(repo, "/")
	return url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}

func (g *GitHubResolver) resolveRepository(ctx context.Context, repository string) (githubRepository, error) {
	var r githubRepository
	if err := g.getJSON(ctx, "/repos/"+escRepo(repository), &r); err != nil {
		return r, err
	}
	if r.ID <= 0 || !strings.EqualFold(r.FullName, repository) {
		return r, fmt.Errorf("GitHub repository identity mismatch: requested %s resolved %s", repository, r.FullName)
	}
	return r, nil
}

func (g *GitHubResolver) resolveTagCommit(ctx context.Context, repository, tag string) (string, error) {
	var ref githubRef
	if err := g.getJSON(ctx, "/repos/"+escRepo(repository)+"/git/ref/tags/"+url.PathEscape(tag), &ref); err != nil {
		return "", err
	}
	sha, typ := strings.ToLower(ref.Object.SHA), ref.Object.Type
	for depth := 0; depth < 4; depth++ {
		switch typ {
		case "commit":
			if !validCommit(sha) {
				return "", errors.New("GitHub tag resolved to invalid commit SHA")
			}
			return sha, nil
		case "tag":
			if !validCommit(sha) {
				return "", errors.New("GitHub annotated tag object has invalid SHA")
			}
			var tagObj githubTag
			if err := g.getJSON(ctx, "/repos/"+escRepo(repository)+"/git/tags/"+sha, &tagObj); err != nil {
				return "", err
			}
			sha, typ = strings.ToLower(tagObj.Object.SHA), tagObj.Object.Type
		default:
			return "", fmt.Errorf("GitHub tag resolved to unsupported object type %q", typ)
		}
	}
	return "", errors.New("GitHub tag indirection too deep")
}

func (g *GitHubResolver) resolveCommit(ctx context.Context, repository, sha string) (string, error) {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if !validCommit(sha) {
		return "", errors.New("pinned-commit requires full 40-hex commit SHA")
	}
	var c githubCommit
	if err := g.getJSON(ctx, "/repos/"+escRepo(repository)+"/git/commits/"+sha, &c); err != nil {
		return "", err
	}
	if !strings.EqualFold(c.SHA, sha) {
		return "", errors.New("GitHub commit response did not match requested immutable SHA")
	}
	return sha, nil
}

func (g *GitHubResolver) validateAsset(repository, tag string, raw githubAsset) (Asset, error) {
	if raw.ID <= 0 || strings.TrimSpace(raw.Name) == "" {
		return Asset{}, errors.New("GitHub release returned malformed asset")
	}
	api, err := url.Parse(raw.URL)
	if err != nil || api.Scheme == "" || api.Host == "" {
		return Asset{}, errors.New("GitHub asset API URL is invalid")
	}
	if api.Scheme != g.apiBase.Scheme || !strings.EqualFold(api.Host, g.apiBase.Host) {
		return Asset{}, errors.New("GitHub asset API URL escaped trusted API origin")
	}
	wantSuffix := "/repos/" + repository + "/releases/assets/" + strconv.FormatInt(raw.ID, 10)
	if !strings.EqualFold(strings.TrimRight(api.Path, "/"), wantSuffix) {
		return Asset{}, errors.New("GitHub asset API URL does not bind requested repository and asset ID")
	}
	browser, err := url.Parse(raw.BrowserDownloadURL)
	if err != nil || browser.Scheme == "" || browser.Host == "" {
		return Asset{}, errors.New("GitHub browser asset URL is invalid")
	}
	if browser.Scheme != "https" && !(g.apiBase.Scheme == "http" && browser.Scheme == "http") {
		return Asset{}, errors.New("GitHub browser asset URL is not secure")
	}
	if !g.browserHosts[browser.Hostname()] {
		return Asset{}, errors.New("GitHub browser asset URL uses untrusted domain")
	}
	expectedPrefix := "/" + repository + "/releases/download/" + tag + "/"
	if !strings.HasPrefix(strings.ToLower(path.Clean(browser.Path))+"/", strings.ToLower(path.Clean(expectedPrefix))+"/") && !strings.HasPrefix(browser.Path, expectedPrefix) {
		return Asset{}, errors.New("GitHub browser asset URL does not bind requested repository/tag")
	}
	digest := strings.ToLower(strings.TrimSpace(raw.Digest))
	if digest != "" && (!strings.HasPrefix(digest, "sha256:") || !validSHA256(strings.TrimPrefix(digest, "sha256:"))) {
		return Asset{}, errors.New("GitHub asset digest is malformed")
	}
	return Asset{ID: raw.ID, Name: raw.Name, APIURL: api.String(), BrowserDownloadURL: browser.String(), Digest: digest, Size: raw.Size}, nil
}

func selectAsset(assets []Asset, name, pattern string) (*Asset, error) {
	name = strings.TrimSpace(name)
	pattern = strings.TrimSpace(pattern)
	if name == "" && pattern == "" {
		return nil, nil
	}
	matches := []Asset{}
	for _, a := range assets {
		ok := false
		if name != "" {
			ok = a.Name == name
		} else {
			matched, err := path.Match(pattern, a.Name)
			if err != nil {
				return nil, fmt.Errorf("invalid asset pattern: %w", err)
			}
			ok = matched
		}
		if ok {
			matches = append(matches, a)
		}
	}
	if len(matches) == 0 {
		return nil, errors.New("release is missing expected asset")
	}
	if len(matches) > 1 {
		return nil, errors.New("asset selector is ambiguous")
	}
	chosen := matches[0]
	return &chosen, nil
}

func (g *GitHubResolver) Resolve(ctx context.Context, spec Spec, req ResolveRequest, channel Channel) (githubResolution, error) {
	repo, err := g.resolveRepository(ctx, spec.Repository)
	if err != nil {
		return githubResolution{}, err
	}
	out := githubResolution{RepositoryID: repo.ID}
	switch channel {
	case ChannelLatestStable, ChannelPinnedRelease:
		var rel githubRelease
		if channel == ChannelLatestStable {
			if err := g.getJSON(ctx, "/repos/"+escRepo(spec.Repository)+"/releases/latest", &rel); err != nil {
				return out, err
			}
			if rel.Draft || rel.Prerelease {
				return out, errors.New("latest-stable resolved to draft or prerelease")
			}
		} else {
			ref := strings.TrimSpace(req.Ref)
			if ref == "" {
				ref = spec.Ref
			}
			if ref == "" {
				return out, errors.New("pinned-release requires release tag")
			}
			out.RequestedRef = ref
			if err := g.getJSON(ctx, "/repos/"+escRepo(spec.Repository)+"/releases/tags/"+url.PathEscape(ref), &rel); err != nil {
				return out, err
			}
			if rel.Draft {
				return out, errors.New("pinned release is draft")
			}
			if rel.TagName != ref {
				return out, errors.New("pinned release tag mismatch")
			}
		}
		if rel.ID <= 0 || strings.TrimSpace(rel.TagName) == "" {
			return out, errors.New("GitHub release metadata is incomplete")
		}
		commit, err := g.resolveTagCommit(ctx, spec.Repository, rel.TagName)
		if err != nil {
			return out, err
		}
		assets := make([]Asset, 0, len(rel.Assets))
		for _, raw := range rel.Assets {
			a, err := g.validateAsset(spec.Repository, rel.TagName, raw)
			if err != nil {
				return out, err
			}
			assets = append(assets, a)
		}
		assetName := req.AssetName
		if assetName == "" {
			assetName = spec.AssetName
		}
		assetPattern := req.AssetPattern
		if assetPattern == "" {
			assetPattern = spec.AssetPattern
		}
		var selected *Asset
		if !spec.BuildRequired {
			selected, err = selectAsset(assets, assetName, assetPattern)
			if err != nil {
				return out, err
			}
		}
		out.ReleaseID, out.ReleaseTag, out.CommitSHA, out.Asset = rel.ID, rel.TagName, commit, selected
		if out.RequestedRef == "" {
			out.RequestedRef = "latest"
		}
		return out, nil
	case ChannelPinnedCommit:
		ref := strings.TrimSpace(req.Ref)
		if ref == "" {
			ref = spec.Ref
		}
		if ref == "" {
			return out, errors.New("pinned-commit requires commit SHA")
		}
		commit, err := g.resolveCommit(ctx, spec.Repository, ref)
		if err != nil {
			return out, err
		}
		out.RequestedRef, out.CommitSHA = ref, commit
		return out, nil
	default:
		return out, fmt.Errorf("GitHub source does not support channel %q", channel)
	}
}
