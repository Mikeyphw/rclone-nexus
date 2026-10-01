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
)

type Status struct {
	ModuleID        string   `json:"module_id"`
	ModuleVersion   string   `json:"module_version,omitempty"`
	ModuleReady     bool     `json:"module_ready"`
	BinaryReady     bool     `json:"binary_ready"`
	RcloneVersion   string   `json:"rclone_version,omitempty"`
	FuseDeviceReady bool     `json:"fuse_device_ready"`
	FuseHelperReady bool     `json:"fuse_helper_ready"`
	ConfigReady     bool     `json:"config_ready"`
	Ready           bool     `json:"ready"`
	Issues          []string `json:"issues,omitempty"`
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

func FindRclone(p paths.Paths) (string, error) {
	if candidate := os.Getenv("RNEXUS_RCLONE_BIN"); candidate != "" {
		if executable(candidate) {
			return candidate, nil
		}
	}
	if candidate, err := exec.LookPath("rclone"); err == nil && executable(candidate) {
		return candidate, nil
	}
	for _, candidate := range []string{
		filepath.Join(p.ProviderModuleDir, "system", "vendor", "bin", "rclone"),
		filepath.Join(p.ProviderModuleDir, "vendor", "bin", "rclone"),
		filepath.Join(p.ProviderModuleDir, "system", "bin", "rclone"),
		filepath.Join(p.ProviderModuleDir, "bin", "rclone"),
		filepath.Join(p.ProviderModuleDir, "rclone"),
	} {
		if executable(candidate) {
			return candidate, nil
		}
	}
	return "", errors.New("rclone binary not found")
}

func FindFuseHelper(p paths.Paths) (string, error) {
	if candidate := os.Getenv("RNEXUS_FUSERMOUNT_BIN"); candidate != "" && executable(candidate) {
		return candidate, nil
	}
	if candidate, err := exec.LookPath("fusermount3"); err == nil && executable(candidate) {
		return candidate, nil
	}
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
	return "", errors.New("fusermount3 not found")
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
	result := Status{ModuleID: "rclone"}
	if info, err := os.Stat(p.ProviderModuleDir); err == nil && info.IsDir() {
		result.ModuleReady = true
		result.ModuleVersion = providerModuleVersion(p.ProviderModuleDir)
	} else {
		result.Issues = append(result.Issues, "provider_module_missing")
	}
	if binary, err := FindRclone(p); err == nil {
		result.BinaryReady = true
		result.RcloneVersion = statusVersion(binary)
	} else {
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
	if info, err := os.Stat(p.RcloneConfig); err == nil && info.Mode().IsRegular() {
		result.ConfigReady = true
	} else {
		result.Issues = append(result.Issues, "rclone_config_missing")
	}
	result.Ready = result.ModuleReady && result.BinaryReady && result.FuseDeviceReady && result.ConfigReady
	return result
}
