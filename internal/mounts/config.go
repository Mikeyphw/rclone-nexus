package mounts

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"rclone-nexus/internal/paths"
)

const maxConfigLine = 256 << 10

type Config struct {
	Name         string
	Enabled      bool
	Remote       string
	Mountpoint   string
	VFSCacheMode string
	AllowOther   bool
	LogLevel     string
	ArgsFile     string
}

var allowedKeys = map[string]bool{
	"enabled": true, "remote": true, "mountpoint": true, "vfs_cache_mode": true,
	"allow_other": true, "log_level": true, "args_file": true,
}

func ValidName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func ConfigPath(p paths.Paths, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("invalid mount name: %s", name)
	}
	return filepath.Join(p.MountsDir, name+".conf"), nil
}

func Parse(p paths.Paths, name string) (Config, error) {
	path, err := ConfigPath(p, name)
	if err != nil {
		return Config{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("mount definition not found")
		}
		return Config{}, err
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxConfigLine)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" {
			return Config{}, fmt.Errorf("invalid config line %d", lineNumber)
		}
		if !allowedKeys[key] {
			return Config{}, fmt.Errorf("unsupported config key %q", key)
		}
		if _, duplicate := values[key]; duplicate {
			return Config{}, fmt.Errorf("duplicate config key %q", key)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return Config{}, err
	}
	cfg := Config{
		Name:         name,
		Enabled:      truthy(values["enabled"]),
		Remote:       values["remote"],
		Mountpoint:   values["mountpoint"],
		VFSCacheMode: defaultString(values["vfs_cache_mode"], "full"),
		AllowOther:   defaultTruthy(values, "allow_other", true),
		LogLevel:     defaultString(values["log_level"], "INFO"),
		ArgsFile:     values["args_file"],
	}
	return cfg, nil
}

func List(p paths.Paths) ([]string, error) {
	entries, err := os.ReadDir(p.MountsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".conf")
		if ValidName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func truthy(value string) bool {
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func defaultTruthy(values map[string]string, key string, fallback bool) bool {
	value, ok := values[key]
	if !ok || value == "" {
		return fallback
	}
	return truthy(value)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
