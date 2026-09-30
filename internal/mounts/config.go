package mounts

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
)

const maxConfigLine = 256 << 10

// Config is the normalized internal mount model. ArgsFile is intentionally an
// internal/root-local field and is never serialized through the public typed
// protocol. The v2 registry has a private persistence representation for it.
type Config struct {
	Name            string
	Enabled         bool
	Remote          string
	Mountpoint      string
	VFSCacheMode    string
	VFSCacheMaxSize string
	VFSCacheMaxAge  string
	DirCacheTime    string
	PollInterval    string
	AllowOther      bool
	ReadOnly        bool
	LogLevel        string
	ArgsFile        string
}

// CandidateConfig is the typed mutation surface. It deliberately has no
// arbitrary argv/args_file field. Existing root-local args_file values are
// preserved server-side by mount name during v2 mutations.
type CandidateConfig struct {
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	Remote          string `json:"remote"`
	Mountpoint      string `json:"mountpoint"`
	VFSCacheMode    string `json:"vfs_cache_mode,omitempty"`
	VFSCacheMaxSize string `json:"vfs_cache_max_size,omitempty"`
	VFSCacheMaxAge  string `json:"vfs_cache_max_age,omitempty"`
	DirCacheTime    string `json:"dir_cache_time,omitempty"`
	PollInterval    string `json:"poll_interval,omitempty"`
	AllowOther      bool   `json:"allow_other"`
	ReadOnly        bool   `json:"read_only,omitempty"`
	LogLevel        string `json:"log_level,omitempty"`
}

type PublicConfig struct {
	CandidateConfig
	HasArgsFile bool `json:"has_args_file,omitempty"`
}

var allowedLegacyKeys = map[string]bool{
	"enabled": true, "remote": true, "mountpoint": true, "vfs_cache_mode": true,
	"vfs_cache_max_size": true, "vfs_cache_max_age": true, "dir_cache_time": true,
	"poll_interval": true, "allow_other": true, "read_only": true,
	"log_level": true, "args_file": true,
}

var sizePattern = regexp.MustCompile(`(?i)^[0-9]+(?:\.[0-9]+)?(?:b|k|kb|kib|m|mb|mib|g|gb|gib|t|tb|tib|p|pb|pib)?$`)

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
	p = p.Normalize()
	if !ValidName(name) {
		return "", fmt.Errorf("invalid mount name: %s", name)
	}
	return filepath.Join(p.MountsDir, name+".conf"), nil
}

func parseLegacyFile(p paths.Paths, name string) (Config, error) {
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
			return Config{}, fmt.Errorf("%s: invalid config line %d", name, lineNumber)
		}
		if !allowedLegacyKeys[key] {
			return Config{}, fmt.Errorf("%s: unsupported config key %q", name, key)
		}
		if _, duplicate := values[key]; duplicate {
			return Config{}, fmt.Errorf("%s: duplicate config key %q", name, key)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return Config{}, err
	}
	cfg := Config{
		Name:            name,
		Enabled:         truthy(values["enabled"]),
		Remote:          values["remote"],
		Mountpoint:      values["mountpoint"],
		VFSCacheMode:    defaultString(values["vfs_cache_mode"], "full"),
		VFSCacheMaxSize: values["vfs_cache_max_size"],
		VFSCacheMaxAge:  values["vfs_cache_max_age"],
		DirCacheTime:    values["dir_cache_time"],
		PollInterval:    values["poll_interval"],
		AllowOther:      defaultTruthy(values, "allow_other", true),
		ReadOnly:        truthy(values["read_only"]),
		LogLevel:        strings.ToUpper(defaultString(values["log_level"], "INFO")),
		ArgsFile:        values["args_file"],
	}
	cfg = normalizeConfig(cfg)
	if err := validateConfig(p, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Parse(p paths.Paths, name string) (Config, error) {
	if !ValidName(name) {
		return Config{}, fmt.Errorf("invalid mount name: %s", name)
	}
	registry, err := LoadRegistry(p)
	if err != nil {
		return Config{}, err
	}
	for _, cfg := range registry.Mounts {
		if cfg.Name == name {
			return cfg, nil
		}
	}
	return Config{}, fmt.Errorf("mount definition not found")
}

func List(p paths.Paths) ([]string, error) {
	registry, err := LoadRegistry(p)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(registry.Mounts))
	for _, cfg := range registry.Mounts {
		names = append(names, cfg.Name)
	}
	sort.Strings(names)
	return names, nil
}

func normalizeConfig(cfg Config) Config {
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Remote = strings.TrimSpace(cfg.Remote)
	cfg.Mountpoint = filepath.Clean(strings.TrimSpace(cfg.Mountpoint))
	cfg.VFSCacheMode = strings.ToLower(strings.TrimSpace(defaultString(cfg.VFSCacheMode, "full")))
	cfg.VFSCacheMaxSize = strings.TrimSpace(cfg.VFSCacheMaxSize)
	cfg.VFSCacheMaxAge = strings.TrimSpace(cfg.VFSCacheMaxAge)
	cfg.DirCacheTime = strings.TrimSpace(cfg.DirCacheTime)
	cfg.PollInterval = strings.TrimSpace(cfg.PollInterval)
	cfg.LogLevel = strings.ToUpper(strings.TrimSpace(defaultString(cfg.LogLevel, "INFO")))
	cfg.ArgsFile = strings.TrimSpace(cfg.ArgsFile)
	return cfg
}

func validateConfig(p paths.Paths, cfg Config) error {
	p = p.Normalize()
	if !ValidName(cfg.Name) {
		return fmt.Errorf("invalid mount name: %s", cfg.Name)
	}
	if cfg.Remote == "" {
		return fmt.Errorf("%s: missing remote", cfg.Name)
	}
	if strings.ContainsAny(cfg.Remote, "\x00\r\n") {
		return fmt.Errorf("%s: remote contains control characters", cfg.Name)
	}
	if cfg.Mountpoint == "" || cfg.Mountpoint == "." || !filepath.IsAbs(cfg.Mountpoint) {
		return fmt.Errorf("%s: mountpoint must be absolute", cfg.Name)
	}
	clean := filepath.Clean(cfg.Mountpoint)
	if clean == string(filepath.Separator) {
		return fmt.Errorf("%s: mountpoint cannot be filesystem root", cfg.Name)
	}
	for _, protected := range []string{p.StateDir, p.ModuleDir, p.ProviderModuleDir} {
		if protected == "" {
			continue
		}
		protected = filepath.Clean(protected)
		if sameOrNested(clean, protected) || sameOrNested(protected, clean) {
			return fmt.Errorf("%s: mountpoint overlaps protected Nexus/provider state", cfg.Name)
		}
	}
	switch cfg.VFSCacheMode {
	case "off", "minimal", "writes", "full":
	default:
		return fmt.Errorf("%s: unsupported vfs_cache_mode %q", cfg.Name, cfg.VFSCacheMode)
	}
	if cfg.VFSCacheMaxSize != "" && !validSize(cfg.VFSCacheMaxSize) {
		return fmt.Errorf("%s: invalid vfs_cache_max_size %q", cfg.Name, cfg.VFSCacheMaxSize)
	}
	for key, value := range map[string]string{
		"vfs_cache_max_age": cfg.VFSCacheMaxAge,
		"dir_cache_time":    cfg.DirCacheTime,
		"poll_interval":     cfg.PollInterval,
	} {
		if value != "" && !validDuration(value) {
			return fmt.Errorf("%s: invalid %s %q", cfg.Name, key, value)
		}
	}
	switch cfg.LogLevel {
	case "DEBUG", "INFO", "NOTICE", "ERROR":
	default:
		return fmt.Errorf("%s: unsupported log_level %q", cfg.Name, cfg.LogLevel)
	}
	if cfg.ArgsFile != "" {
		if strings.ContainsAny(cfg.ArgsFile, "\x00\r\n") {
			return fmt.Errorf("%s: invalid args_file path", cfg.Name)
		}
		if !filepath.IsAbs(cfg.ArgsFile) {
			cleanArgs := filepath.Clean(cfg.ArgsFile)
			if cleanArgs == ".." || strings.HasPrefix(cleanArgs, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%s: relative args_file escapes state directory", cfg.Name)
			}
		}
	}
	return nil
}

func validateConfigs(p paths.Paths, configs []Config) error {
	seenNames := map[string]struct{}{}
	for i := range configs {
		configs[i] = normalizeConfig(configs[i])
		if err := validateConfig(p, configs[i]); err != nil {
			return err
		}
		if _, exists := seenNames[configs[i].Name]; exists {
			return fmt.Errorf("duplicate mount name %q", configs[i].Name)
		}
		seenNames[configs[i].Name] = struct{}{}
	}
	for i := 0; i < len(configs); i++ {
		for j := i + 1; j < len(configs); j++ {
			a := filepath.Clean(configs[i].Mountpoint)
			b := filepath.Clean(configs[j].Mountpoint)
			if sameOrNested(a, b) || sameOrNested(b, a) {
				return fmt.Errorf("mountpoints overlap: %s=%s and %s=%s", configs[i].Name, a, configs[j].Name, b)
			}
		}
	}
	return nil
}

func sameOrNested(path, parent string) bool {
	path = filepath.Clean(path)
	parent = filepath.Clean(parent)
	if path == parent {
		return true
	}
	if parent == string(filepath.Separator) {
		return true
	}
	return strings.HasPrefix(path, parent+string(filepath.Separator))
}

func validSize(value string) bool {
	if strings.EqualFold(value, "off") {
		return true
	}
	return sizePattern.MatchString(value)
}

func validDuration(value string) bool {
	if value == "0" || strings.EqualFold(value, "off") {
		return true
	}
	// rclone accepts d/w suffixes in addition to Go's duration units.
	lower := strings.ToLower(value)
	multiplier := time.Duration(0)
	if strings.HasSuffix(lower, "d") {
		multiplier = 24 * time.Hour
	} else if strings.HasSuffix(lower, "w") {
		multiplier = 7 * 24 * time.Hour
	}
	if multiplier != 0 {
		number := strings.TrimSpace(lower[:len(lower)-1])
		n, err := strconv.ParseFloat(number, 64)
		return err == nil && n >= 0
	}
	duration, err := time.ParseDuration(value)
	return err == nil && duration >= 0
}

func candidateFromConfig(cfg Config) CandidateConfig {
	return CandidateConfig{
		Name: cfg.Name, Enabled: cfg.Enabled, Remote: cfg.Remote, Mountpoint: cfg.Mountpoint,
		VFSCacheMode: cfg.VFSCacheMode, VFSCacheMaxSize: cfg.VFSCacheMaxSize,
		VFSCacheMaxAge: cfg.VFSCacheMaxAge, DirCacheTime: cfg.DirCacheTime,
		PollInterval: cfg.PollInterval, AllowOther: cfg.AllowOther, ReadOnly: cfg.ReadOnly,
		LogLevel: cfg.LogLevel,
	}
}

func publicFromConfig(cfg Config) PublicConfig {
	return PublicConfig{CandidateConfig: candidateFromConfig(cfg), HasArgsFile: cfg.ArgsFile != ""}
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
