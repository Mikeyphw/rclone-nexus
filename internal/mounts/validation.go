package mounts

import (
	"fmt"
	"path/filepath"
	"strings"

	cachegov "rclone-nexus/internal/cache"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/policy"
	"rclone-nexus/internal/vfs"
)

// ValidationIssue is a credential-free, field-addressable explanation suitable
// for interactive clients. Detail intentionally mirrors the authoritative
// validator's wording so CLI and WebUI diagnosis stay aligned.
type ValidationIssue struct {
	Code         string `json:"code"`
	Category     string `json:"category"`
	Mount        string `json:"mount,omitempty"`
	Field        string `json:"field,omitempty"`
	Severity     string `json:"severity"`
	Message      string `json:"message"`
	Detail       string `json:"detail,omitempty"`
	Suggestion   string `json:"suggestion,omitempty"`
	RelatedMount string `json:"related_mount,omitempty"`
}

type ValidationReport struct {
	Valid           bool              `json:"valid"`
	CurrentRevision uint64            `json:"current_revision"`
	Issues          []ValidationIssue `json:"issues"`
}

func issue(code, category, field, message, detail, suggestion string) ValidationIssue {
	return ValidationIssue{
		Code: code, Category: category, Field: field, Severity: "error",
		Message: message, Detail: detail, Suggestion: suggestion,
	}
}

func validationIssuesForConfig(p paths.Paths, cfg Config) []ValidationIssue {
	p = p.Normalize()
	out := []ValidationIssue{}
	if !ValidName(cfg.Name) {
		out = append(out, issue("invalid_mount_name", "input", "name", "Mount name is not valid", fmt.Sprintf("invalid mount name: %s", cfg.Name), "Use letters, numbers, dot, underscore, or dash."))
	}
	if cfg.Remote == "" {
		out = append(out, issue("remote_required", "remote", "remote", "Choose an rclone remote", fmt.Sprintf("%s: missing remote", cfg.Name), "Select a configured remote and folder."))
	} else if strings.ContainsAny(cfg.Remote, "\x00\r\n") {
		out = append(out, issue("invalid_remote", "remote", "remote", "Remote contains unsupported characters", fmt.Sprintf("%s: remote contains control characters", cfg.Name), "Choose the remote again or enter a clean remote:path value."))
	}
	if cfg.Mountpoint == "" || cfg.Mountpoint == "." || !filepath.IsAbs(cfg.Mountpoint) {
		out = append(out, issue("mountpoint_absolute_required", "destination", "mountpoint", "Mount location must be an absolute path", fmt.Sprintf("%s: mountpoint must be absolute", cfg.Name), "/storage/emulated/0/Rclone/<name>"))
	} else {
		clean := filepath.Clean(cfg.Mountpoint)
		if clean == string(filepath.Separator) {
			out = append(out, issue("mountpoint_root_forbidden", "destination", "mountpoint", "Filesystem root cannot be used as a mount location", fmt.Sprintf("%s: mountpoint cannot be filesystem root", cfg.Name), "/storage/emulated/0/Rclone/<name>"))
		}
		for _, protected := range []string{p.StateDir, p.ModuleDir, p.ProviderModuleDir} {
			if protected == "" {
				continue
			}
			protected = filepath.Clean(protected)
			if sameOrNested(clean, protected) || sameOrNested(protected, clean) {
				out = append(out, issue("mountpoint_protected", "security", "mountpoint", "Mount location overlaps Nexus or provider state", fmt.Sprintf("%s: mountpoint overlaps protected Nexus/provider state", cfg.Name), "Choose a folder under shared storage instead."))
				break
			}
		}
	}
	switch cfg.VFSCacheMode {
	case "off", "minimal", "writes", "full":
	default:
		out = append(out, issue("invalid_vfs_cache_mode", "vfs", "vfs_cache_mode", "VFS cache mode is not supported", fmt.Sprintf("%s: unsupported vfs_cache_mode %q", cfg.Name, cfg.VFSCacheMode), "Choose off, minimal, writes, or full."))
	}
	if cfg.VFSCacheMaxSize != "" && !validSize(cfg.VFSCacheMaxSize) {
		out = append(out, issue("invalid_cache_size", "vfs", "vfs_cache_max_size", "Cache size is not valid", fmt.Sprintf("%s: invalid vfs_cache_max_size %q", cfg.Name, cfg.VFSCacheMaxSize), "Use a value such as 512MiB, 2GiB, or 8GiB."))
	}
	for _, item := range []struct{ field, value, label string }{
		{"vfs_cache_max_age", cfg.VFSCacheMaxAge, "Cache maximum age"},
		{"dir_cache_time", cfg.DirCacheTime, "Directory cache duration"},
		{"poll_interval", cfg.PollInterval, "Poll interval"},
	} {
		if item.value != "" && !validDuration(item.value) {
			out = append(out, issue("invalid_duration", "vfs", item.field, item.label+" is not valid", fmt.Sprintf("%s: invalid %s %q", cfg.Name, item.field, item.value), "Use values such as 30s, 15m, 24h, or 7d."))
		}
	}
	switch cfg.LogLevel {
	case "DEBUG", "INFO", "NOTICE", "ERROR":
	default:
		out = append(out, issue("invalid_log_level", "advanced", "log_level", "Log level is not supported", fmt.Sprintf("%s: unsupported log_level %q", cfg.Name, cfg.LogLevel), "Choose DEBUG, INFO, NOTICE, or ERROR."))
	}
	if !policy.ValidNetworkMode(cfg.NetworkMode) {
		out = append(out, issue("invalid_network_policy", "policy", "network_mode", "Network policy is not supported", fmt.Sprintf("%s: unsupported network_mode %q", cfg.Name, cfg.NetworkMode), "Choose Any network, Wi-Fi, Unmetered, or Offline allowed."))
	}
	if cfg.MinBattery < 0 || cfg.MinBattery > 100 {
		out = append(out, issue("invalid_min_battery", "policy", "min_battery", "Minimum battery must be between 0 and 100", fmt.Sprintf("%s: min_battery must be 0..100", cfg.Name), "Use 0 to disable the battery threshold."))
	}
	if cfg.MinFreeCacheSpace != "" {
		if _, err := cachegov.ParseSize(cfg.MinFreeCacheSpace); err != nil {
			out = append(out, issue("invalid_min_cache_free", "vfs", "min_free_cache_space", "Minimum free cache space is not valid", fmt.Sprintf("%s: invalid min_free_cache_space %q", cfg.Name, cfg.MinFreeCacheSpace), "Use a value such as 512MiB or 2GiB."))
		}
	}
	for _, item := range []struct{ field, value, label string }{
		{"boot_settle", cfg.BootSettle, "Boot settle delay"},
		{"network_settle", cfg.NetworkSettle, "Network settle delay"},
	} {
		if item.value != "" && !validDuration(item.value) {
			out = append(out, issue("invalid_duration", "advanced", item.field, item.label+" is not valid", fmt.Sprintf("%s: invalid %s %q", cfg.Name, item.field, item.value), "Use values such as 5s, 30s, or 2m."))
		}
	}
	if !vfs.ValidProfile(cfg.VFSProfile) {
		out = append(out, issue("invalid_vfs_profile", "vfs", "vfs_profile", "Performance profile is not supported", fmt.Sprintf("%s: unsupported vfs_profile %q", cfg.Name, cfg.VFSProfile), "Choose one of the profiles offered by Nexus."))
	}
	if cfg.CacheLowWater < 1 || cfg.CacheLowWater > 99 || cfg.CacheHighWater < 1 || cfg.CacheHighWater > 100 || cfg.CacheLowWater >= cfg.CacheHighWater {
		out = append(out, issue("invalid_cache_watermarks", "vfs", "cache_low_water", "Cache watermarks are inconsistent", fmt.Sprintf("%s: cache water marks must satisfy 1 <= low < high <= 100", cfg.Name), "Keep low-water below high-water, for example 75% and 90%."))
	}
	if cfg.ArgsFile != "" {
		if strings.ContainsAny(cfg.ArgsFile, "\x00\r\n") {
			out = append(out, issue("invalid_private_args", "security", "", "Root-local advanced arguments are invalid", fmt.Sprintf("%s: invalid args_file path", cfg.Name), "Repair the root-local mount definition outside the WebUI."))
		} else if !filepath.IsAbs(cfg.ArgsFile) {
			cleanArgs := filepath.Clean(cfg.ArgsFile)
			if cleanArgs == ".." || strings.HasPrefix(cleanArgs, ".."+string(filepath.Separator)) {
				out = append(out, issue("invalid_private_args", "security", "", "Root-local advanced arguments escape Nexus state", fmt.Sprintf("%s: relative args_file escapes state directory", cfg.Name), "Repair the root-local mount definition outside the WebUI."))
			}
		}
	}
	return out
}

func validationIssuesForConfigs(p paths.Paths, configs []Config) []ValidationIssue {
	out := []ValidationIssue{}
	seenNames := map[string]struct{}{}
	for i := range configs {
		configs[i] = normalizeConfig(configs[i])
		items := validationIssuesForConfig(p, configs[i])
		for j := range items {
			items[j].Mount = configs[i].Name
		}
		out = append(out, items...)
		if _, exists := seenNames[configs[i].Name]; exists && configs[i].Name != "" {
			item := issue("duplicate_mount_name", "input", "name", "Another mount already uses this name", fmt.Sprintf("duplicate mount name %q", configs[i].Name), "Choose a unique mount name.")
			item.Mount = configs[i].Name
			out = append(out, item)
		}
		seenNames[configs[i].Name] = struct{}{}
	}
	for i := 0; i < len(configs); i++ {
		for j := i + 1; j < len(configs); j++ {
			a := filepath.Clean(configs[i].Mountpoint)
			b := filepath.Clean(configs[j].Mountpoint)
			if a == "." || b == "." || !filepath.IsAbs(a) || !filepath.IsAbs(b) {
				continue
			}
			if sameOrNested(a, b) || sameOrNested(b, a) {
				item := issue("mountpoint_overlap", "destination", "mountpoint", "Mount location overlaps another mount", fmt.Sprintf("mountpoints overlap: %s=%s and %s=%s", configs[i].Name, a, configs[j].Name, b), "Choose a separate mount location.")
				item.Mount = configs[i].Name
				item.RelatedMount = configs[j].Name
				out = append(out, item)
			}
		}
	}
	return out
}

func materializeCandidateUnchecked(current Registry, candidate []CandidateConfig) []Config {
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
		next = append(next, normalizeConfig(cfg))
	}
	normalizeSortConfigs(next)
	return next
}

// ValidateCandidate reports every currently detectable registry issue without
// issuing a preview proof or mutating state.
func ValidateCandidate(p paths.Paths, candidate []CandidateConfig) (ValidationReport, error) {
	current, err := LoadRegistry(p)
	if err != nil {
		return ValidationReport{}, err
	}
	next := materializeCandidateUnchecked(current, candidate)
	issues := validationIssuesForConfigs(p, next)
	return ValidationReport{Valid: len(issues) == 0, CurrentRevision: current.Revision, Issues: issues}, nil
}
