package runtimeauth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rclone-nexus/internal/paths"
)

type Mode string

const ModeManaged Mode = "managed"

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
	ActiveRuntimeID       string   `json:"active_runtime_id,omitempty"`
	ActivationPhase       string   `json:"activation_phase,omitempty"`
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

func bundledExecutable(p paths.Paths) string {
	candidate := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
	if executable(candidate) {
		return candidate
	}
	return ""
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

func explicitMode() (Mode, error) {
	value := strings.TrimSpace(os.Getenv("RNEXUS_RUNTIME_MODE"))
	if value == "" || value == string(ModeManaged) {
		return ModeManaged, nil
	}
	return "", fmt.Errorf("static runtime build supports only %q mode; %s is no longer supported", ModeManaged, value)
}

func Resolve(p paths.Paths) (Resolution, error) {
	p = p.Normalize()
	present, enabled := legacyProviderState(p)
	mode, err := explicitMode()
	if err != nil {
		return Resolution{}, err
	}
	result := Resolution{
		Mode:                  mode,
		Canonical:             true,
		RuntimeRoot:           p.RuntimeDir,
		ConfigRoot:            p.ManagedConfigDir,
		Source:                "nexus-bundled-static",
		Config:                p.ManagedRcloneConfig,
		LegacyProviderPresent: present,
		LegacyProviderEnabled: enabled,
	}
	if mode != ModeManaged {
		return Resolution{}, errors.New("static runtime resolver reached unsupported mode")
	}
	result.Binary = bundledExecutable(p)
	if result.Binary == "" {
		result.Binary = filepath.Join(p.ModuleDir, "system", "bin", "rclone")
		result.Issues = append(result.Issues, "bundled_runtime_missing")
	}
	if present {
		// Provider modules may be installed on the device, or may have been used as
		// a source to package the ZIP. They are not runtime authority in the static
		// build and must not make the managed resolver ambiguous.
		result.Issues = append(result.Issues, "legacy_provider_observed_ignored")
	}
	if !executable(result.Binary) {
		result.Issues = append(result.Issues, "bundled_runtime_missing")
	}
	if !regular(result.Config) {
		result.Issues = append(result.Issues, "managed_config_missing")
	}
	result.Operational = executable(result.Binary) && regular(result.Config)
	return result, nil
}

func executableResolved(p paths.Paths, _ bool) (string, error) {
	resolution, err := Resolve(p)
	if err != nil {
		return "", err
	}
	if resolution.Binary == "" || !executable(resolution.Binary) {
		return "", fmt.Errorf("bundled rclone runtime not found: %s", resolution.Binary)
	}
	return resolution.Binary, nil
}

func Executable(p paths.Paths) (string, error) {
	return executableResolved(p, false)
}

func ConfigPath(p paths.Paths) (string, error) {
	resolution, err := Resolve(p)
	if err != nil {
		return "", err
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
