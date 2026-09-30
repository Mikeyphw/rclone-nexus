package vfs

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const (
	ProfileCustom    = "custom"
	ProfileStreaming = "streaming"
	ProfileBalanced  = "balanced"
	ProfileOffline   = "offline"
	ProfileMinimal   = "minimal"
)

type Options struct {
	CacheMode    string `json:"vfs_cache_mode"`
	CacheMaxSize string `json:"vfs_cache_max_size,omitempty"`
	CacheMaxAge  string `json:"vfs_cache_max_age,omitempty"`
	DirCacheTime string `json:"dir_cache_time,omitempty"`
	PollInterval string `json:"poll_interval,omitempty"`
}

type Profile struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Options     Options `json:"options"`
}

type Recommendation struct {
	Profile        string `json:"profile"`
	CacheMaxSize   string `json:"cache_max_size"`
	MemoryBytes    uint64 `json:"memory_bytes"`
	CacheFreeBytes uint64 `json:"cache_free_bytes"`
	Reason         string `json:"reason"`
}

var profiles = []Profile{
	{Name: ProfileStreaming, Description: "media-first full VFS caching with bounded local storage", Options: Options{CacheMode: "full", CacheMaxSize: "4GiB", CacheMaxAge: "24h", DirCacheTime: "1h", PollInterval: "15s"}},
	{Name: ProfileBalanced, Description: "general-purpose full VFS caching with moderate footprint", Options: Options{CacheMode: "full", CacheMaxSize: "2GiB", CacheMaxAge: "12h", DirCacheTime: "30m", PollInterval: "30s"}},
	{Name: ProfileOffline, Description: "larger persistent cache for intermittent connectivity", Options: Options{CacheMode: "full", CacheMaxSize: "8GiB", CacheMaxAge: "168h", DirCacheTime: "24h", PollInterval: "1m"}},
	{Name: ProfileMinimal, Description: "small cache footprint for constrained devices", Options: Options{CacheMode: "minimal", CacheMaxSize: "512MiB", CacheMaxAge: "1h", DirCacheTime: "5m", PollInterval: "1m"}},
	{Name: ProfileCustom, Description: "use the mount's explicit VFS values without profile mutation", Options: Options{}},
}

func Names() []string {
	out := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, profile.Name)
	}
	return out
}

func Profiles() []Profile {
	out := make([]Profile, len(profiles))
	copy(out, profiles)
	return out
}

func ValidProfile(name string) bool {
	for _, profile := range profiles {
		if profile.Name == name {
			return true
		}
	}
	return false
}

func Expand(profile string, custom Options) (Options, error) {
	if profile == "" {
		profile = ProfileCustom
	}
	if profile == ProfileCustom {
		return custom, nil
	}
	for _, item := range profiles {
		if item.Name == profile {
			return item.Options, nil
		}
	}
	return Options{}, fmt.Errorf("unsupported vfs profile %q", profile)
}

func MemoryTotalBytes() uint64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kib, _ := strconv.ParseUint(fields[1], 10, 64)
			return kib * 1024
		}
	}
	return 0
}

func FreeBytes(path string) uint64 {
	var stat syscall.Statfs_t
	if syscall.Statfs(path, &stat) != nil {
		return 0
	}
	return stat.Bavail * uint64(stat.Bsize)
}

func Recommend(cacheRoot string) Recommendation {
	memory := MemoryTotalBytes()
	free := FreeBytes(cacheRoot)
	profile := ProfileBalanced
	reason := "balanced default"
	var target uint64 = 2 << 30
	switch {
	case memory > 0 && memory < 4<<30:
		profile, target, reason = ProfileMinimal, 512<<20, "device memory is constrained"
	case free > 0 && free < 2<<30:
		profile, target, reason = ProfileMinimal, 512<<20, "cache filesystem free space is constrained"
	case free >= 32<<30 && memory >= 8<<30:
		profile, target, reason = ProfileOffline, 8<<30, "ample memory and cache storage are available"
	case free >= 12<<30 && memory >= 6<<30:
		profile, target, reason = ProfileStreaming, 4<<30, "device resources support a larger streaming cache"
	}
	if free > 0 {
		quarter := free / 4
		if quarter < target {
			target = quarter
		}
		if target < 256<<20 {
			target = 256 << 20
		}
	}
	return Recommendation{Profile: profile, CacheMaxSize: formatBytes(target), MemoryBytes: memory, CacheFreeBytes: free, Reason: reason}
}

func formatBytes(value uint64) string {
	const gib = uint64(1 << 30)
	const mib = uint64(1 << 20)
	if value >= gib && value%gib == 0 {
		return fmt.Sprintf("%dGiB", value/gib)
	}
	if value >= mib {
		return fmt.Sprintf("%dMiB", value/mib)
	}
	return fmt.Sprintf("%dB", value)
}
