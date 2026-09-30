package mounts

import (
	"time"

	cachegov "rclone-nexus/internal/cache"
	"rclone-nexus/internal/policy"
	"rclone-nexus/internal/vfs"
)

func EffectiveVFS(cfg Config) (vfs.Options, error) {
	return vfs.Expand(cfg.VFSProfile, vfs.Options{CacheMode: cfg.VFSCacheMode, CacheMaxSize: cfg.VFSCacheMaxSize, CacheMaxAge: cfg.VFSCacheMaxAge, DirCacheTime: cfg.DirCacheTime, PollInterval: cfg.PollInterval})
}

func PolicySpec(cfg Config) (policy.Spec, error) {
	minFree, err := cachegov.ParseSize(cfg.MinFreeCacheSpace)
	if err != nil {
		return policy.Spec{}, err
	}
	var bootSettle, networkSettle time.Duration
	if cfg.BootSettle != "" {
		bootSettle, err = parseDuration(cfg.BootSettle)
		if err != nil {
			return policy.Spec{}, err
		}
	}
	if cfg.NetworkSettle != "" {
		networkSettle, err = parseDuration(cfg.NetworkSettle)
		if err != nil {
			return policy.Spec{}, err
		}
	}
	return policy.Spec{NetworkMode: cfg.NetworkMode, ChargingOnly: cfg.ChargingOnly, MinBattery: cfg.MinBattery, MinFreeCache: minFree, BootSettle: bootSettle, NetworkSettle: networkSettle}, nil
}

func CacheLimits(cfg Config) (cachegov.Limits, error) {
	effective, err := EffectiveVFS(cfg)
	if err != nil {
		return cachegov.Limits{}, err
	}
	maxBytes, err := cachegov.ParseSize(effective.CacheMaxSize)
	if err != nil {
		return cachegov.Limits{}, err
	}
	minFree, err := cachegov.ParseSize(cfg.MinFreeCacheSpace)
	if err != nil {
		return cachegov.Limits{}, err
	}
	return cachegov.Limits{MaxBytes: maxBytes, HighPercent: cfg.CacheHighWater, LowPercent: cfg.CacheLowWater, MinFree: minFree}, nil
}

func parseDuration(value string) (time.Duration, error) {
	if value == "" || value == "0" || value == "off" {
		return 0, nil
	}
	lower := value
	if len(lower) > 1 {
		suffix := lower[len(lower)-1]
		if suffix == 'd' || suffix == 'w' {
			n := lower[:len(lower)-1]
			parsed, err := time.ParseDuration(n + "h")
			if err != nil {
				return 0, err
			}
			if suffix == 'd' {
				return parsed * 24, nil
			}
			return parsed * 24 * 7, nil
		}
	}
	return time.ParseDuration(value)
}
