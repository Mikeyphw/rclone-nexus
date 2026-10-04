package runtimeacquire

import (
	"rclone-nexus/internal/runtimesource"
	"testing"
)

func TestPinnedCommitRequiresPublishedBuildAuthority(t *testing.T) {
	r := runtimesource.Resolution{Kind: runtimesource.KindGitHub, Channel: runtimesource.ChannelPinnedCommit, BuildRepository: "Mikeyphw/rclone-nexus"}
	if !requiresPublishedBuild(r) {
		t.Fatal("GitHub pinned commit bypassed SOURCE-X02 build authority")
	}
}

func TestBuildRequiredReleaseResolutionRequiresPublishedBuild(t *testing.T) {
	r := runtimesource.Resolution{Kind: runtimesource.KindGitHub, Asset: &runtimesource.Asset{ID: 1}, BuildRequired: true, BuildRepository: "Mikeyphw/rclone-nexus"}
	if !requiresPublishedBuild(r) {
		t.Fatal("build-required release asset bypassed SOURCE-X02")
	}
}

func TestOrdinaryGitHubReleaseMayUseSelectedAsset(t *testing.T) {
	r := runtimesource.Resolution{Kind: runtimesource.KindGitHub, Asset: &runtimesource.Asset{ID: 1}}
	if requiresPublishedBuild(r) {
		t.Fatal("ordinary GitHub release was forced through SOURCE-X02")
	}
}
