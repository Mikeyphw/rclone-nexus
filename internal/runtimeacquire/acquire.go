package runtimeacquire

import (
	"context"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimebuild"
	"rclone-nexus/internal/runtimesource"
	"rclone-nexus/internal/runtimestore"
)

type publishedAcquirer func(context.Context, paths.Paths, runtimesource.Resolution) (runtimestore.Manifest, runtimesource.Resolution, error)

func requiresPublishedBuild(r runtimesource.Resolution) bool {
	return r.Kind == runtimesource.KindGitHub && (r.Asset == nil || r.BuildRequired)
}

func acquireResolutionWith(ctx context.Context, p paths.Paths, id string, published publishedAcquirer) (runtimestore.Manifest, runtimesource.Resolution, error) {
	r, err := runtimesource.InspectResolution(p, id)
	if err != nil {
		return runtimestore.Manifest{}, runtimesource.Resolution{}, err
	}
	if requiresPublishedBuild(r) {
		return published(ctx, p, r)
	}
	m, err := runtimesource.AcquireResolution(ctx, p, id)
	return m, r, err
}

func AcquireResolution(ctx context.Context, p paths.Paths, id string) (runtimestore.Manifest, runtimesource.Resolution, error) {
	return acquireResolutionWith(ctx, p, id, runtimebuild.AcquirePublished)
}

func ImportResolution(ctx context.Context, p paths.Paths, id string) (runtimestore.Manifest, runtimesource.Resolution, error) {
	m, r, err := AcquireResolution(ctx, p, id)
	if err != nil {
		return m, r, err
	}
	m, err = runtimestore.Test(ctx, p, m.RuntimeID)
	return m, r, err
}
