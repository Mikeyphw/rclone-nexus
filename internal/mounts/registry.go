package mounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"rclone-nexus/internal/paths"
)

const RegistrySchemaVersion = 2

type persistedConfig struct {
	Name              string `json:"name"`
	Enabled           bool   `json:"enabled"`
	Remote            string `json:"remote"`
	Mountpoint        string `json:"mountpoint"`
	VFSCacheMode      string `json:"vfs_cache_mode"`
	VFSCacheMaxSize   string `json:"vfs_cache_max_size,omitempty"`
	VFSCacheMaxAge    string `json:"vfs_cache_max_age,omitempty"`
	DirCacheTime      string `json:"dir_cache_time,omitempty"`
	PollInterval      string `json:"poll_interval,omitempty"`
	AllowOther        bool   `json:"allow_other"`
	ReadOnly          bool   `json:"read_only,omitempty"`
	LogLevel          string `json:"log_level"`
	RequireNetwork    bool   `json:"require_network,omitempty"`
	ProbeRemote       bool   `json:"probe_remote,omitempty"`
	NetworkMode       string `json:"network_mode,omitempty"`
	ChargingOnly      bool   `json:"charging_only,omitempty"`
	MinBattery        int    `json:"min_battery,omitempty"`
	MinFreeCacheSpace string `json:"min_free_cache_space,omitempty"`
	BootSettle        string `json:"boot_settle,omitempty"`
	NetworkSettle     string `json:"network_settle,omitempty"`
	VFSProfile        string `json:"vfs_profile,omitempty"`
	CacheHighWater    int    `json:"cache_high_water,omitempty"`
	CacheLowWater     int    `json:"cache_low_water,omitempty"`
	ArgsFile          string `json:"args_file,omitempty"`
}

type persistedRegistry struct {
	SchemaVersion int               `json:"schema_version"`
	Revision      uint64            `json:"revision"`
	Digest        string            `json:"digest"`
	Mounts        []persistedConfig `json:"mounts"`
}

type Registry struct {
	SchemaVersion int
	Revision      uint64
	Digest        string
	Source        string
	Mounts        []Config
}

type PublicRegistry struct {
	SchemaVersion int            `json:"schema_version"`
	Revision      uint64         `json:"revision"`
	Digest        string         `json:"digest"`
	Source        string         `json:"source"`
	Mounts        []PublicConfig `json:"mounts"`
}

type ConfigChange struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Restart      bool     `json:"restart"`
	Reasons      []string `json:"reasons,omitempty"`
	Consequences []string `json:"consequences,omitempty"`
}

type ConfigPreview struct {
	SchemaVersion     int            `json:"schema_version"`
	PreviewProof      string         `json:"preview_proof,omitempty"`
	PreviewExpiresMS  int64          `json:"preview_expires_unix_ms,omitempty"`
	CurrentRevision   uint64         `json:"current_revision"`
	CurrentDigest     string         `json:"current_digest"`
	CandidateDigest   string         `json:"candidate_digest"`
	Source            string         `json:"source"`
	Mounts            []PublicConfig `json:"mounts"`
	Changes           []ConfigChange `json:"changes"`
	RequiresRestart   []string       `json:"requires_restart,omitempty"`
	PreviousAvailable bool           `json:"previous_available"`
}

type ConfigApplyReport struct {
	Applied           bool               `json:"applied"`
	Registry          PublicRegistry     `json:"registry"`
	Changes           []ConfigChange     `json:"changes"`
	LifecycleActions  []ActionResult     `json:"lifecycle_actions,omitempty"`
	LifecycleFailures []LifecycleFailure `json:"lifecycle_failures,omitempty"`
}

type staleRevisionError struct {
	Expected uint64
	Actual   uint64
}

func (e staleRevisionError) Error() string {
	return fmt.Sprintf("stale configuration revision: expected=%d actual=%d", e.Expected, e.Actual)
}

type candidateDigestError struct {
	Expected string
	Actual   string
}

func (e candidateDigestError) Error() string {
	return fmt.Sprintf("candidate digest mismatch: expected=%s actual=%s", e.Expected, e.Actual)
}

type previousUnavailableError struct{}

func (previousUnavailableError) Error() string {
	return "previous-known-good configuration is unavailable"
}

func IsStaleRevision(err error) bool { var target staleRevisionError; return errors.As(err, &target) }
func IsCandidateDigestMismatch(err error) bool {
	var target candidateDigestError
	return errors.As(err, &target)
}
func IsPreviousUnavailable(err error) bool {
	var target previousUnavailableError
	return errors.As(err, &target)
}

func LoadRegistry(p paths.Paths) (Registry, error) {
	p = p.Normalize()
	data, err := os.ReadFile(p.ConfigRegistry)
	if err == nil {
		return decodeRegistry(p, data, "registry-v2")
	}
	if !os.IsNotExist(err) {
		return Registry{}, err
	}
	return loadLegacyRegistry(p)
}

func LoadPreviousRegistry(p paths.Paths) (Registry, error) {
	p = p.Normalize()
	data, err := os.ReadFile(p.ConfigPrevious)
	if err != nil {
		if os.IsNotExist(err) {
			return Registry{}, previousUnavailableError{}
		}
		return Registry{}, err
	}
	return decodeRegistry(p, data, "previous-v2")
}

func decodeRegistry(p paths.Paths, data []byte, source string) (Registry, error) {
	var persisted persistedRegistry
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return Registry{}, fmt.Errorf("decode configuration registry: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Registry{}, fmt.Errorf("configuration registry contains trailing JSON")
	}
	if persisted.SchemaVersion != RegistrySchemaVersion {
		return Registry{}, fmt.Errorf("unsupported configuration registry schema %d", persisted.SchemaVersion)
	}
	registry := Registry{
		SchemaVersion: persisted.SchemaVersion,
		Revision:      persisted.Revision,
		Digest:        persisted.Digest,
		Source:        source,
		Mounts:        configsFromPersisted(persisted.Mounts),
	}
	normalizeSortConfigs(registry.Mounts)
	if err := validateConfigs(p, registry.Mounts); err != nil {
		return Registry{}, fmt.Errorf("invalid configuration registry: %w", err)
	}
	actual, err := digestConfigs(registry.Mounts)
	if err != nil {
		return Registry{}, err
	}
	if registry.Digest == "" || registry.Digest != actual {
		return Registry{}, fmt.Errorf("configuration registry digest mismatch")
	}
	return registry, nil
}

func loadLegacyRegistry(p paths.Paths) (Registry, error) {
	p = p.Normalize()
	entries, err := os.ReadDir(p.MountsDir)
	if err != nil {
		if os.IsNotExist(err) {
			entries = nil
		} else {
			return Registry{}, err
		}
	}
	configs := make([]Config, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".conf")
		if !ValidName(name) {
			return Registry{}, fmt.Errorf("invalid legacy mount filename %q", entry.Name())
		}
		cfg, err := parseLegacyFile(p, name)
		if err != nil {
			return Registry{}, err
		}
		configs = append(configs, cfg)
	}
	normalizeSortConfigs(configs)
	if err := validateConfigs(p, configs); err != nil {
		return Registry{}, err
	}
	digest, err := digestConfigs(configs)
	if err != nil {
		return Registry{}, err
	}
	return Registry{SchemaVersion: RegistrySchemaVersion, Revision: 0, Digest: digest, Source: "legacy-v0.1", Mounts: configs}, nil
}

func PublicSnapshot(p paths.Paths) (PublicRegistry, error) {
	registry, err := LoadRegistry(p)
	if err != nil {
		return PublicRegistry{}, err
	}
	return publicRegistry(registry), nil
}

func publicRegistry(registry Registry) PublicRegistry {
	mounts := make([]PublicConfig, 0, len(registry.Mounts))
	for _, cfg := range registry.Mounts {
		mounts = append(mounts, publicFromConfig(cfg))
	}
	return PublicRegistry{
		SchemaVersion: RegistrySchemaVersion,
		Revision:      registry.Revision,
		Digest:        registry.Digest,
		Source:        registry.Source,
		Mounts:        mounts,
	}
}

func PreviewCandidate(p paths.Paths, candidate []CandidateConfig) (ConfigPreview, error) {
	p = p.Normalize()
	current, err := LoadRegistry(p)
	if err != nil {
		return ConfigPreview{}, err
	}
	next, err := materializeCandidate(p, current, candidate)
	if err != nil {
		return ConfigPreview{}, err
	}
	return previewBetween(p, current, next)
}

func PreviewPrevious(p paths.Paths) (ConfigPreview, error) {
	p = p.Normalize()
	current, err := LoadRegistry(p)
	if err != nil {
		return ConfigPreview{}, err
	}
	previous, err := LoadPreviousRegistry(p)
	if err != nil {
		return ConfigPreview{}, err
	}
	return previewBetween(p, current, previous.Mounts)
}

func previewBetween(p paths.Paths, current Registry, next []Config) (ConfigPreview, error) {
	normalizeSortConfigs(next)
	if err := validateConfigs(p, next); err != nil {
		return ConfigPreview{}, err
	}
	digest, err := digestConfigs(next)
	if err != nil {
		return ConfigPreview{}, err
	}
	changes := diffConfigs(current.Mounts, next)
	restart := make([]string, 0)
	for _, change := range changes {
		if change.Restart {
			restart = append(restart, change.Name)
		}
	}
	public := make([]PublicConfig, 0, len(next))
	for _, cfg := range next {
		public = append(public, publicFromConfig(cfg))
	}
	_, prevErr := os.Stat(p.Normalize().ConfigPrevious)
	return ConfigPreview{
		SchemaVersion:     RegistrySchemaVersion,
		CurrentRevision:   current.Revision,
		CurrentDigest:     current.Digest,
		CandidateDigest:   digest,
		Source:            current.Source,
		Mounts:            public,
		Changes:           changes,
		RequiresRestart:   restart,
		PreviousAvailable: prevErr == nil,
	}, nil
}

func ApplyCandidate(ctx context.Context, p paths.Paths, expectedRevision uint64, expectedDigest string, candidate []CandidateConfig, progress func(string, string)) (ConfigApplyReport, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return ConfigApplyReport{}, err
	}
	var report ConfigApplyReport
	err := withConfigLock(p, func() error {
		current, err := LoadRegistry(p)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return staleRevisionError{Expected: expectedRevision, Actual: current.Revision}
		}
		next, err := materializeCandidate(p, current, candidate)
		if err != nil {
			return err
		}
		preview, err := previewBetween(p, current, next)
		if err != nil {
			return err
		}
		if expectedDigest == "" || preview.CandidateDigest != expectedDigest {
			return candidateDigestError{Expected: expectedDigest, Actual: preview.CandidateDigest}
		}
		report, err = publishAndApply(ctx, p, current, next, preview.Changes, progress)
		return err
	})
	return report, err
}

func RollbackPrevious(ctx context.Context, p paths.Paths, expectedRevision uint64, previousDigest string, progress func(string, string)) (ConfigApplyReport, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return ConfigApplyReport{}, err
	}
	var report ConfigApplyReport
	err := withConfigLock(p, func() error {
		current, err := LoadRegistry(p)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return staleRevisionError{Expected: expectedRevision, Actual: current.Revision}
		}
		previous, err := LoadPreviousRegistry(p)
		if err != nil {
			return err
		}
		actual, err := digestConfigs(previous.Mounts)
		if err != nil {
			return err
		}
		if previousDigest == "" || actual != previousDigest {
			return candidateDigestError{Expected: previousDigest, Actual: actual}
		}
		preview, err := previewBetween(p, current, previous.Mounts)
		if err != nil {
			return err
		}
		report, err = publishAndApply(ctx, p, current, previous.Mounts, preview.Changes, progress)
		return err
	})
	return report, err
}

func materializeCandidate(p paths.Paths, current Registry, candidate []CandidateConfig) ([]Config, error) {
	currentByName := map[string]Config{}
	for _, cfg := range current.Mounts {
		currentByName[cfg.Name] = cfg
	}
	next := make([]Config, 0, len(candidate))
	for _, item := range candidate {
		cfg := Config{
			Name: item.Name, Enabled: item.Enabled, Remote: item.Remote, Mountpoint: item.Mountpoint,
			VFSCacheMode: item.VFSCacheMode, VFSCacheMaxSize: item.VFSCacheMaxSize,
			VFSCacheMaxAge: item.VFSCacheMaxAge, DirCacheTime: item.DirCacheTime,
			PollInterval: item.PollInterval, AllowOther: item.AllowOther, ReadOnly: item.ReadOnly,
			LogLevel: item.LogLevel, RequireNetwork: item.RequireNetwork, ProbeRemote: item.ProbeRemote,
			NetworkMode: item.NetworkMode, ChargingOnly: item.ChargingOnly, MinBattery: item.MinBattery,
			MinFreeCacheSpace: item.MinFreeCacheSpace, BootSettle: item.BootSettle, NetworkSettle: item.NetworkSettle,
			VFSProfile: item.VFSProfile, CacheHighWater: item.CacheHighWater, CacheLowWater: item.CacheLowWater,
		}
		if old, ok := currentByName[item.Name]; ok {
			cfg.ArgsFile = old.ArgsFile
		}
		cfg = normalizeConfig(cfg)
		next = append(next, cfg)
	}
	normalizeSortConfigs(next)
	if err := validateConfigs(p, next); err != nil {
		return nil, err
	}
	return next, nil
}

func publishAndApply(ctx context.Context, p paths.Paths, current Registry, next []Config, changes []ConfigChange, progress func(string, string)) (ConfigApplyReport, error) {
	next = cloneConfigs(next)
	normalizeSortConfigs(next)
	digest, err := digestConfigs(next)
	if err != nil {
		return ConfigApplyReport{}, err
	}
	plan := planLifecycle(p, current.Mounts, next, changes)
	previous := Registry{SchemaVersion: RegistrySchemaVersion, Revision: current.Revision, Digest: current.Digest, Source: "previous-v2", Mounts: cloneConfigs(current.Mounts)}
	if previous.Digest == "" {
		previous.Digest, err = digestConfigs(previous.Mounts)
		if err != nil {
			return ConfigApplyReport{}, err
		}
	}
	if err := writeRegistryAtomic(p.ConfigPrevious, previous); err != nil {
		return ConfigApplyReport{}, fmt.Errorf("publish previous-known-good configuration: %w", err)
	}
	published := Registry{
		SchemaVersion: RegistrySchemaVersion,
		Revision:      current.Revision + 1,
		Digest:        digest,
		Source:        "registry-v2",
		Mounts:        next,
	}
	if err := writeRegistryAtomic(p.ConfigRegistry, published); err != nil {
		return ConfigApplyReport{}, fmt.Errorf("publish configuration registry: %w", err)
	}
	actions, failures := executeLifecyclePlan(ctx, p, plan, progress)
	return ConfigApplyReport{
		Applied:           true,
		Registry:          publicRegistry(published),
		Changes:           changes,
		LifecycleActions:  actions,
		LifecycleFailures: failures,
	}, nil
}

func writeRegistryAtomic(path string, registry Registry) error {
	return writeRegistryAtomicWithHook(path, registry, nil)
}

func writeRegistryAtomicWithHook(path string, registry Registry, beforeRename func() error) error {
	mounts := configsToPersisted(registry.Mounts)
	persisted := persistedRegistry{
		SchemaVersion: RegistrySchemaVersion,
		Revision:      registry.Revision,
		Digest:        registry.Digest,
		Mounts:        mounts,
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".registry-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	defer cleanup()
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if beforeRename != nil {
		if err := beforeRename(); err != nil {
			return err
		}
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	return nil
}

func syncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

func digestConfigs(configs []Config) (string, error) {
	normalized := cloneConfigs(configs)
	normalizeSortConfigs(normalized)
	payload, err := json.Marshal(configsToPersisted(normalized))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeSortConfigs(configs []Config) {
	for i := range configs {
		configs[i] = normalizeConfig(configs[i])
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].Name < configs[j].Name })
}

func cloneConfigs(configs []Config) []Config {
	out := make([]Config, len(configs))
	copy(out, configs)
	return out
}

func configsToPersisted(configs []Config) []persistedConfig {
	out := make([]persistedConfig, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, persistedConfig{
			Name: cfg.Name, Enabled: cfg.Enabled, Remote: cfg.Remote, Mountpoint: cfg.Mountpoint,
			VFSCacheMode: cfg.VFSCacheMode, VFSCacheMaxSize: cfg.VFSCacheMaxSize,
			VFSCacheMaxAge: cfg.VFSCacheMaxAge, DirCacheTime: cfg.DirCacheTime,
			PollInterval: cfg.PollInterval, AllowOther: cfg.AllowOther, ReadOnly: cfg.ReadOnly,
			LogLevel: cfg.LogLevel, RequireNetwork: cfg.RequireNetwork, ProbeRemote: cfg.ProbeRemote,
			NetworkMode: cfg.NetworkMode, ChargingOnly: cfg.ChargingOnly, MinBattery: cfg.MinBattery,
			MinFreeCacheSpace: cfg.MinFreeCacheSpace, BootSettle: cfg.BootSettle, NetworkSettle: cfg.NetworkSettle,
			VFSProfile: cfg.VFSProfile, CacheHighWater: cfg.CacheHighWater, CacheLowWater: cfg.CacheLowWater, ArgsFile: cfg.ArgsFile,
		})
	}
	return out
}

func configsFromPersisted(configs []persistedConfig) []Config {
	out := make([]Config, 0, len(configs))
	for _, cfg := range configs {
		out = append(out, Config{
			Name: cfg.Name, Enabled: cfg.Enabled, Remote: cfg.Remote, Mountpoint: cfg.Mountpoint,
			VFSCacheMode: cfg.VFSCacheMode, VFSCacheMaxSize: cfg.VFSCacheMaxSize,
			VFSCacheMaxAge: cfg.VFSCacheMaxAge, DirCacheTime: cfg.DirCacheTime,
			PollInterval: cfg.PollInterval, AllowOther: cfg.AllowOther, ReadOnly: cfg.ReadOnly,
			LogLevel: cfg.LogLevel, RequireNetwork: cfg.RequireNetwork, ProbeRemote: cfg.ProbeRemote,
			NetworkMode: cfg.NetworkMode, ChargingOnly: cfg.ChargingOnly, MinBattery: cfg.MinBattery,
			MinFreeCacheSpace: cfg.MinFreeCacheSpace, BootSettle: cfg.BootSettle, NetworkSettle: cfg.NetworkSettle,
			VFSProfile: cfg.VFSProfile, CacheHighWater: cfg.CacheHighWater, CacheLowWater: cfg.CacheLowWater, ArgsFile: cfg.ArgsFile,
		})
	}
	return out
}

func diffConfigs(current, next []Config) []ConfigChange {
	oldMap := map[string]Config{}
	newMap := map[string]Config{}
	for _, cfg := range current {
		oldMap[cfg.Name] = cfg
	}
	for _, cfg := range next {
		newMap[cfg.Name] = cfg
	}
	names := make([]string, 0, len(oldMap)+len(newMap))
	seen := map[string]bool{}
	for name := range oldMap {
		names = append(names, name)
		seen[name] = true
	}
	for name := range newMap {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	changes := make([]ConfigChange, 0)
	for _, name := range names {
		old, oldOK := oldMap[name]
		nextCfg, newOK := newMap[name]
		switch {
		case !oldOK && newOK:
			changes = append(changes, ConfigChange{Name: name, Kind: "create", Restart: nextCfg.Enabled, Reasons: []string{"created"}, Consequences: consequencesFor("create", []string{"created"}, nextCfg.Enabled)})
		case oldOK && !newOK:
			changes = append(changes, ConfigChange{Name: name, Kind: "delete", Restart: true, Reasons: []string{"deleted"}, Consequences: consequencesFor("delete", []string{"deleted"}, true)})
		case oldOK && newOK:
			reasons := configDiffReasons(old, nextCfg)
			if len(reasons) == 0 {
				continue
			}
			changes = append(changes, ConfigChange{Name: name, Kind: "update", Restart: restartSensitive(reasons), Reasons: reasons, Consequences: consequencesFor("update", reasons, restartSensitive(reasons))})
		}
	}
	return changes
}

func configDiffReasons(a, b Config) []string {
	var out []string
	checks := []struct{ name, av, bv string }{
		{"remote", a.Remote, b.Remote}, {"mountpoint", a.Mountpoint, b.Mountpoint},
		{"vfs_cache_mode", a.VFSCacheMode, b.VFSCacheMode}, {"vfs_cache_max_size", a.VFSCacheMaxSize, b.VFSCacheMaxSize},
		{"vfs_cache_max_age", a.VFSCacheMaxAge, b.VFSCacheMaxAge}, {"dir_cache_time", a.DirCacheTime, b.DirCacheTime},
		{"poll_interval", a.PollInterval, b.PollInterval}, {"log_level", a.LogLevel, b.LogLevel}, {"args_file", a.ArgsFile, b.ArgsFile},
		{"network_mode", a.NetworkMode, b.NetworkMode}, {"min_free_cache_space", a.MinFreeCacheSpace, b.MinFreeCacheSpace},
		{"boot_settle", a.BootSettle, b.BootSettle}, {"network_settle", a.NetworkSettle, b.NetworkSettle}, {"vfs_profile", a.VFSProfile, b.VFSProfile},
	}
	for _, check := range checks {
		if check.av != check.bv {
			out = append(out, check.name)
		}
	}
	if a.Enabled != b.Enabled {
		out = append(out, "enabled")
	}
	if a.AllowOther != b.AllowOther {
		out = append(out, "allow_other")
	}
	if a.ReadOnly != b.ReadOnly {
		out = append(out, "read_only")
	}
	if a.RequireNetwork != b.RequireNetwork {
		out = append(out, "require_network")
	}
	if a.ProbeRemote != b.ProbeRemote {
		out = append(out, "probe_remote")
	}
	if a.ChargingOnly != b.ChargingOnly {
		out = append(out, "charging_only")
	}
	if a.MinBattery != b.MinBattery {
		out = append(out, "min_battery")
	}
	if a.CacheHighWater != b.CacheHighWater {
		out = append(out, "cache_high_water")
	}
	if a.CacheLowWater != b.CacheLowWater {
		out = append(out, "cache_low_water")
	}
	return out
}

func consequencesFor(kind string, reasons []string, restart bool) []string {
	seen := map[string]bool{}
	add := func(value string) {
		if value != "" && !seen[value] {
			seen[value] = true
		}
	}
	switch kind {
	case "create":
		add("configuration_create")
		if restart {
			add("lifecycle_start")
		}
		add("namespace_requalify")
	case "delete":
		add("lifecycle_stop")
		add("namespace_release")
		add("cache_preserved")
	default:
		if restart {
			add("lifecycle_restart")
			add("namespace_requalify")
		}
	}
	for _, reason := range reasons {
		switch reason {
		case "network_mode", "require_network", "charging_only", "min_battery", "min_free_cache_space", "boot_settle", "network_settle":
			add("policy_recheck")
		case "vfs_profile", "vfs_cache_mode", "vfs_cache_max_size", "vfs_cache_max_age", "cache_high_water", "cache_low_water":
			add("cache_policy_recheck")
		case "mountpoint", "allow_other":
			add("namespace_requalify")
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func restartSensitive(reasons []string) bool {
	for _, reason := range reasons {
		switch reason {
		case "enabled", "network_mode", "require_network", "charging_only", "min_battery", "min_free_cache_space", "boot_settle", "network_settle", "cache_high_water", "cache_low_water":
			continue
		default:
			return true
		}
	}
	return false
}
