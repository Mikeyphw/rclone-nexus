package runtimeauth

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"rclone-nexus/internal/paths"
)

type Mode string

const (
	ModeManaged           Mode = "managed"
	ModeExternal          Mode = "external"
	ModeMigrationRequired Mode = "migration-required"
)

type Resolution struct {
	Mode                  Mode     `json:"mode"`
	Canonical             bool     `json:"canonical"`
	Operational           bool     `json:"operational"`
	Binary                string   `json:"binary,omitempty"`
	Config                string   `json:"config,omitempty"`
	RuntimeRoot           string   `json:"runtime_root"`
	ConfigRoot            string   `json:"config_root"`
	Source                string   `json:"source"`
	LegacyProviderPresent bool     `json:"legacy_provider_present"`
	LegacyProviderEnabled bool     `json:"legacy_provider_enabled"`
	AmbiguousAuthority    bool     `json:"ambiguous_authority"`
	Issues                []string `json:"issues,omitempty"`
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func regular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func legacyProviderState(p paths.Paths) (present, enabled bool) {
	info, err := os.Stat(p.ProviderModuleDir)
	if err != nil || !info.IsDir() {
		return false, false
	}
	present = true
	if _, err := os.Stat(filepath.Join(p.ProviderModuleDir, "disable")); err == nil {
		return true, false
	}
	if _, err := os.Stat(filepath.Join(p.ProviderModuleDir, "remove")); err == nil {
		return true, false
	}
	return true, true
}

func externalExecutable(p paths.Paths) string {
	if candidate := strings.TrimSpace(os.Getenv("RNEXUS_EXTERNAL_RCLONE_BIN")); candidate != "" {
		return candidate
	}
	if candidate := strings.TrimSpace(os.Getenv("RNEXUS_RCLONE_BIN")); candidate != "" {
		return candidate
	}
	for _, candidate := range []string{
		filepath.Join(p.ProviderModuleDir, "system", "vendor", "bin", "rclone"),
		filepath.Join(p.ProviderModuleDir, "vendor", "bin", "rclone"),
		filepath.Join(p.ProviderModuleDir, "system", "bin", "rclone"),
		filepath.Join(p.ProviderModuleDir, "bin", "rclone"),
		filepath.Join(p.ProviderModuleDir, "rclone"),
	} {
		if executable(candidate) {
			return candidate
		}
	}
	if candidate, err := exec.LookPath("rclone"); err == nil && executable(candidate) {
		return candidate
	}
	return ""
}

func explicitMode() (Mode, error) {
	value := strings.TrimSpace(os.Getenv("RNEXUS_RUNTIME_MODE"))
	if value == "" {
		return "", nil
	}
	switch Mode(value) {
	case ModeManaged, ModeExternal, ModeMigrationRequired:
		return Mode(value), nil
	default:
		return "", fmt.Errorf("invalid RNEXUS_RUNTIME_MODE %q", value)
	}
}

func Resolve(p paths.Paths) (Resolution, error) {
	p = p.Normalize()
	present, enabled := legacyProviderState(p)
	mode, err := explicitMode()
	if err != nil {
		return Resolution{}, err
	}
	if mode == "" {
		switch {
		case strings.TrimSpace(os.Getenv("RNEXUS_EXTERNAL_RCLONE_BIN")) != "", strings.TrimSpace(os.Getenv("RNEXUS_RCLONE_BIN")) != "":
			mode = ModeExternal
		case executable(p.ManagedRcloneBin):
			mode = ModeManaged
		case present:
			mode = ModeMigrationRequired
		default:
			mode = ModeManaged
		}
	}

	result := Resolution{
		Mode:                  mode,
		RuntimeRoot:           p.RuntimeDir,
		ConfigRoot:            p.ManagedConfigDir,
		LegacyProviderPresent: present,
		LegacyProviderEnabled: enabled,
	}

	switch mode {
	case ModeManaged:
		result.Canonical = true
		result.Source = "nexus-managed"
		result.Binary = p.ManagedRcloneBin
		// Managed mode has exactly one config authority. Callers that need an
		// explicit supported override must set ManagedRcloneConfig (or the
		// RNEXUS_MANAGED_RCLONE_CONFIG production setting); the legacy
		// RcloneConfig field cannot redirect managed execution.
		result.Config = p.ManagedRcloneConfig
		if enabled {
			result.AmbiguousAuthority = true
			result.Issues = append(result.Issues, "legacy_provider_lifecycle_enabled")
		}
		if !executable(result.Binary) {
			result.Issues = append(result.Issues, "managed_runtime_missing")
		}
		if !regular(result.Config) {
			result.Issues = append(result.Issues, "managed_config_missing")
		}
		result.Operational = !result.AmbiguousAuthority && executable(result.Binary) && regular(result.Config)
	case ModeExternal:
		result.Canonical = false
		result.Source = "external-compatibility"
		result.Binary = externalExecutable(p)
		result.Config = strings.TrimSpace(os.Getenv("RNEXUS_EXTERNAL_RCLONE_CONFIG"))
		if result.Config == "" {
			result.Config = strings.TrimSpace(os.Getenv("RCLONE_CONFIG"))
		}
		if result.Config == "" {
			legacyConfig := filepath.Join(p.ProviderModuleDir, "conf", "rclone.conf")
			if regular(legacyConfig) {
				result.Config = legacyConfig
			} else {
				result.Config = p.RcloneConfig
			}
		}
		if result.Binary == "" || !executable(result.Binary) {
			result.Issues = append(result.Issues, "external_runtime_missing")
		}
		if !regular(result.Config) {
			result.Issues = append(result.Issues, "external_config_missing")
		}
		result.Operational = result.Binary != "" && executable(result.Binary) && regular(result.Config)
	case ModeMigrationRequired:
		result.Canonical = false
		result.Source = "legacy-provider-detected"
		result.Config = filepath.Join(p.ProviderModuleDir, "conf", "rclone.conf")
		result.Issues = append(result.Issues, "migration_required")
		result.Operational = false
	default:
		return Resolution{}, errors.New("runtime authority resolved unknown mode")
	}
	return result, nil
}

func Executable(p paths.Paths) (string, error) {
	resolution, err := Resolve(p)
	if err != nil {
		return "", err
	}
	switch {
	case resolution.Mode == ModeMigrationRequired:
		return "", errors.New("runtime migration required: legacy provider is not a managed Nexus runtime")
	case resolution.AmbiguousAuthority:
		return "", errors.New("runtime authority ambiguous: legacy provider lifecycle remains enabled alongside managed Nexus runtime")
	case resolution.Binary == "" || !executable(resolution.Binary):
		if resolution.Mode == ModeManaged {
			return "", fmt.Errorf("managed rclone runtime not found: %s", resolution.Binary)
		}
		return "", errors.New("external runtime mode requires an explicit executable")
	}
	return resolution.Binary, nil
}

func ConfigPath(p paths.Paths) (string, error) {
	resolution, err := Resolve(p)
	if err != nil {
		return "", err
	}
	if resolution.Mode == ModeMigrationRequired {
		return "", errors.New("runtime migration required: legacy provider config is not authoritative in managed mode")
	}
	if resolution.Config == "" {
		return "", errors.New("rclone config path is not configured")
	}
	return resolution.Config, nil
}

func RequireOperational(p paths.Paths) (Resolution, error) {
	resolution, err := Resolve(p)
	if err != nil {
		return Resolution{}, err
	}
	if _, err := Executable(p); err != nil {
		return resolution, err
	}
	if _, err := ConfigPath(p); err != nil {
		return resolution, err
	}
	if !regular(resolution.Config) {
		return resolution, fmt.Errorf("rclone config not found: %s", resolution.Config)
	}
	return resolution, nil
}
