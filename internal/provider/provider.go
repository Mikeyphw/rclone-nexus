package provider

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/redact"
	"rclone-nexus/internal/runtimeauth"
)

type Status struct {
	ModuleID              string           `json:"module_id"`
	ModuleVersion         string           `json:"module_version,omitempty"`
	ModuleReady           bool             `json:"module_ready"`
	BinaryReady           bool             `json:"binary_ready"`
	RcloneVersion         string           `json:"rclone_version,omitempty"`
	FuseDeviceReady       bool             `json:"fuse_device_ready"`
	FuseHelperReady       bool             `json:"fuse_helper_ready"`
	ConfigReady           bool             `json:"config_ready"`
	Ready                 bool             `json:"ready"`
	Issues                []string         `json:"issues,omitempty"`
	RuntimeMode           runtimeauth.Mode `json:"runtime_mode"`
	RuntimeSource         string           `json:"runtime_source"`
	RuntimeCanonical      bool             `json:"runtime_canonical"`
	RuntimeOperational    bool             `json:"runtime_operational"`
	RuntimeAuthorityReady bool             `json:"runtime_authority_ready"`
	MigrationRequired     bool             `json:"migration_required"`
	AmbiguousAuthority    bool             `json:"ambiguous_authority"`
	LegacyProviderFound   bool             `json:"legacy_provider_found"`
}

type limitedBuffer struct {
	buf       bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if b.remaining > 0 {
		chunk := p
		if len(chunk) > b.remaining {
			chunk = chunk[:b.remaining]
		}
		_, _ = b.buf.Write(chunk)
		b.remaining -= len(chunk)
	}
	return original, nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }

// FindRclone is retained as the compatibility-facing API, but it no longer
// performs provider/PATH discovery itself. All production callers converge on
// the canonical runtime authority in runtimeauth.
func FindRclone(p paths.Paths) (string, error) {
	return runtimeauth.Executable(p)
}

// ConfigPath resolves the same canonical static authority as FindRclone.
// RCLONE_CONFIG and provider-owned configuration cannot redirect managed execution.
func ConfigPath(p paths.Paths) (string, error) {
	return runtimeauth.ConfigPath(p)
}

func FindFuseHelper(p paths.Paths) (string, error) {
	p = p.Normalize()
	// RNEXUS_FUSERMOUNT_BIN is retained only as an explicit test/diagnostic
	// override. Production managed mode must use the Nexus-owned helper whose
	// bytes are acquired from NewFuture independently of the selected rclone
	// runtime source.
	if candidate := os.Getenv("RNEXUS_FUSERMOUNT_BIN"); candidate != "" && executable(candidate) {
		return candidate, nil
	}
	if executable(p.ManagedFuseHelperBin) {
		return p.ManagedFuseHelperBin, nil
	}
	if authority, err := runtimeauth.Resolve(p); err == nil && authority.Mode == runtimeauth.ModeManaged {
		return "", errors.New("Nexus-managed NewFuture fusermount3 helper is unavailable")
	}
	// Compatibility/migration mode may still need to inspect or stop a legacy
	// provider. It may use that provider's helper, but managed Nexus mounts never
	// depend on it.
	for _, candidate := range []string{
		filepath.Join(p.ProviderModuleDir, "system", "vendor", "bin", "fusermount3"),
		filepath.Join(p.ProviderModuleDir, "vendor", "bin", "fusermount3"),
		filepath.Join(p.ProviderModuleDir, "system", "bin", "fusermount3"),
		filepath.Join(p.ProviderModuleDir, "bin", "fusermount3"),
	} {
		if executable(candidate) {
			return candidate, nil
		}
	}
	if candidate, err := exec.LookPath("fusermount3"); err == nil && executable(candidate) {
		return candidate, nil
	}
	return "", errors.New("fusermount3 not found")
}

// RuntimeEnv injects the canonical fusermount3 directory into PATH for rclone
// processes. This is deliberately independent of the selected rclone provider:
// bclone, official rclone, NewFuture and source builds all consume the same
// Nexus-owned NewFuture helper authority.
func RuntimeEnv(p paths.Paths) []string {
	env := os.Environ()
	helper, err := FindFuseHelper(p)
	if err != nil {
		return env
	}
	dir := filepath.Dir(helper)
	found := false
	for i, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			value := strings.TrimPrefix(entry, "PATH=")
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + value
			found = true
			break
		}
	}
	if !found {
		env = append(env, "PATH="+dir)
	}
	return env
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func statusVersion(binary string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "version")
	stdout := &limitedBuffer{remaining: 32 << 10}
	stderr := &limitedBuffer{remaining: 8 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return ""
	}
	line := strings.Split(strings.TrimSpace(stdout.String()), "\n")[0]
	return redact.BoundedString(line, 1024)
}

func providerModuleVersion(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "module.prop"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "version" {
			return redact.BoundedString(strings.TrimSpace(value), 256)
		}
	}
	return ""
}

func Discover(p paths.Paths) Status {
	p = p.Normalize()
	resolution, resolveErr := runtimeauth.Resolve(p)
	result := Status{
		ModuleID:            "rclone",
		RuntimeMode:         resolution.Mode,
		RuntimeSource:       resolution.Source,
		RuntimeCanonical:    resolution.Canonical,
		RuntimeOperational:  resolution.Operational,
		MigrationRequired:   false,
		AmbiguousAuthority:  resolution.AmbiguousAuthority,
		LegacyProviderFound: resolution.LegacyProviderPresent,
	}
	if info, err := os.Stat(p.ProviderModuleDir); err == nil && info.IsDir() {
		result.ModuleReady = true
		result.ModuleVersion = providerModuleVersion(p.ProviderModuleDir)
	}
	if resolveErr != nil {
		result.Issues = append(result.Issues, "runtime_authority_invalid")
	} else {
		result.Issues = append(result.Issues, resolution.Issues...)
	}
	if binary, err := FindRclone(p); err == nil {
		result.BinaryReady = true
		result.RuntimeAuthorityReady = !result.MigrationRequired && !result.AmbiguousAuthority
		result.RcloneVersion = statusVersion(binary)
	} else if !result.MigrationRequired {
		result.Issues = append(result.Issues, "rclone_binary_missing")
	}
	if info, err := os.Stat(p.FuseDevice); err == nil && info.Mode()&os.ModeDevice != 0 {
		result.FuseDeviceReady = true
	} else {
		result.Issues = append(result.Issues, "fuse_device_unavailable")
	}
	if _, err := FindFuseHelper(p); err == nil {
		result.FuseHelperReady = true
	} else {
		result.Issues = append(result.Issues, "fuse_helper_unavailable")
	}
	if config, err := ConfigPath(p); err == nil {
		if info, err := os.Stat(config); err == nil && info.Mode().IsRegular() {
			result.ConfigReady = true
		} else {
			result.Issues = append(result.Issues, "rclone_config_missing")
		}
	} else if !result.MigrationRequired {
		result.Issues = append(result.Issues, "rclone_config_unresolved")
	}
	result.Ready = result.RuntimeOperational && result.BinaryReady && result.FuseDeviceReady && result.ConfigReady
	return result
}
