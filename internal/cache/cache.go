package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
)

type Limits struct {
	MaxBytes    uint64
	HighPercent int
	LowPercent  int
	MinFree     uint64
}

type Status struct {
	Name         string `json:"name"`
	Bytes        uint64 `json:"bytes"`
	Files        int    `json:"files"`
	FreeBytes    uint64 `json:"free_bytes"`
	TotalBytes   uint64 `json:"total_bytes"`
	MaxBytes     uint64 `json:"max_bytes,omitempty"`
	HighBytes    uint64 `json:"high_bytes,omitempty"`
	LowBytes     uint64 `json:"low_bytes,omitempty"`
	MinFreeBytes uint64 `json:"min_free_bytes,omitempty"`
	AboveHigh    bool   `json:"above_high"`
	BelowMinFree bool   `json:"below_min_free"`
	PrunePending bool   `json:"prune_pending"`
}

type Preview struct {
	Name            string `json:"name"`
	Action          string `json:"action"`
	CurrentBytes    uint64 `json:"current_bytes"`
	DeleteBytes     uint64 `json:"delete_bytes"`
	DeleteFiles     int    `json:"delete_files"`
	TargetBytes     uint64 `json:"target_bytes,omitempty"`
	RequiresStopped bool   `json:"requires_stopped,omitempty"`
}

type fileEntry struct {
	path string
	size uint64
	mod  time.Time
}

func ParseSize(value string) (uint64, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "off" {
		return 0, nil
	}
	suffixes := []struct {
		name string
		mul  float64
	}{{"pib", 1 << 50}, {"pb", 1e15}, {"tib", 1 << 40}, {"tb", 1e12}, {"gib", 1 << 30}, {"gb", 1e9}, {"mib", 1 << 20}, {"mb", 1e6}, {"kib", 1 << 10}, {"kb", 1e3}, {"b", 1}}
	multiplier := float64(1)
	number := value
	for _, item := range suffixes {
		if strings.HasSuffix(value, item.name) {
			multiplier = item.mul
			number = strings.TrimSpace(strings.TrimSuffix(value, item.name))
			break
		}
	}
	f, err := strconv.ParseFloat(number, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid size %q", value)
	}
	return uint64(f * multiplier), nil
}

func OwnedDir(p paths.Paths, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\\x00\r\n") || name == "." || name == ".." {
		return "", fmt.Errorf("invalid cache owner name")
	}
	p = p.Normalize()
	root := filepath.Clean(p.CacheDir)
	target := filepath.Clean(filepath.Join(root, name))
	if target == root || !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("cache path escapes Nexus cache root")
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("owned cache root is a symlink")
	}
	return target, nil
}

func Inspect(p paths.Paths, name string, limits Limits) (Status, error) {
	dir, err := OwnedDir(p, name)
	if err != nil {
		return Status{}, err
	}
	_ = os.MkdirAll(dir, 0o700)
	entries, bytes, err := scan(dir)
	if err != nil {
		return Status{}, err
	}
	free, total := fsSpace(dir)
	status := Status{Name: name, Bytes: bytes, Files: len(entries), FreeBytes: free, TotalBytes: total, MaxBytes: limits.MaxBytes, MinFreeBytes: limits.MinFree}
	if limits.HighPercent <= 0 {
		limits.HighPercent = 90
	}
	if limits.LowPercent <= 0 {
		limits.LowPercent = 75
	}
	if limits.MaxBytes > 0 {
		status.HighBytes = limits.MaxBytes * uint64(limits.HighPercent) / 100
		status.LowBytes = limits.MaxBytes * uint64(limits.LowPercent) / 100
		status.AboveHigh = bytes > status.HighBytes
	}
	status.BelowMinFree = limits.MinFree > 0 && free < limits.MinFree
	status.PrunePending = status.AboveHigh || status.BelowMinFree
	return status, nil
}

func PrunePreview(p paths.Paths, name string, limits Limits) (Preview, error) {
	return preview(p, name, limits, "prune")
}
func ClearPreview(p paths.Paths, name string) (Preview, error) {
	return preview(p, name, Limits{}, "clear")
}
func ForgetPreview(p paths.Paths, name string) (Preview, error) {
	result, err := preview(p, name, Limits{}, "forget")
	result.RequiresStopped = true
	return result, err
}

func preview(p paths.Paths, name string, limits Limits, action string) (Preview, error) {
	dir, err := OwnedDir(p, name)
	if err != nil {
		return Preview{}, err
	}
	_ = os.MkdirAll(dir, 0o700)
	entries, bytes, err := scan(dir)
	if err != nil {
		return Preview{}, err
	}
	result := Preview{Name: name, Action: action, CurrentBytes: bytes}
	if action == "clear" || action == "forget" {
		result.DeleteBytes = bytes
		result.DeleteFiles = len(entries)
		return result, nil
	}
	status, err := Inspect(p, name, limits)
	if err != nil {
		return Preview{}, err
	}
	if !status.PrunePending {
		return result, nil
	}
	target := status.LowBytes
	if limits.MaxBytes == 0 {
		target = bytes
	}
	freeNeeded := uint64(0)
	if limits.MinFree > status.FreeBytes {
		freeNeeded = limits.MinFree - status.FreeBytes
	}
	var deleted uint64
	var count int
	for _, entry := range entries {
		if bytes-deleted <= target && deleted >= freeNeeded {
			break
		}
		deleted += entry.size
		count++
	}
	result.DeleteBytes = deleted
	result.DeleteFiles = count
	result.TargetBytes = target
	return result, nil
}

func Prune(ctx context.Context, p paths.Paths, name string, limits Limits) (Preview, error) {
	plan, err := PrunePreview(p, name, limits)
	if err != nil || plan.DeleteFiles == 0 {
		return plan, err
	}
	dir, _ := OwnedDir(p, name)
	entries, _, err := scan(dir)
	if err != nil {
		return Preview{}, err
	}
	deleted := 0
	var bytes uint64
	for _, entry := range entries {
		if deleted >= plan.DeleteFiles {
			break
		}
		select {
		case <-ctx.Done():
			return Preview{}, ctx.Err()
		default:
		}
		if err := removeOwnedFile(dir, entry.path); err != nil {
			return Preview{}, err
		}
		deleted++
		bytes += entry.size
	}
	plan.DeleteFiles, plan.DeleteBytes = deleted, bytes
	return plan, nil
}
func Clear(ctx context.Context, p paths.Paths, name string) (Preview, error) {
	plan, err := ClearPreview(p, name)
	if err != nil {
		return Preview{}, err
	}
	dir, _ := OwnedDir(p, name)
	entries, _, err := scan(dir)
	if err != nil {
		return Preview{}, err
	}
	deleted := 0
	var bytes uint64
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return Preview{}, ctx.Err()
		default:
		}
		if err := removeOwnedFile(dir, entry.path); err != nil {
			return Preview{}, err
		}
		deleted++
		bytes += entry.size
	}
	plan.DeleteFiles, plan.DeleteBytes = deleted, bytes
	return plan, nil
}
func Forget(ctx context.Context, p paths.Paths, name string) (Preview, error) {
	plan, err := ForgetPreview(p, name)
	if err != nil {
		return Preview{}, err
	}
	dir, _ := OwnedDir(p, name)
	select {
	case <-ctx.Done():
		return Preview{}, ctx.Err()
	default:
	}
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return Preview{}, fmt.Errorf("owned cache root is a symlink")
	}
	if err := os.RemoveAll(dir); err != nil {
		return Preview{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Preview{}, err
	}
	return plan, nil
}

func scan(dir string) ([]fileEntry, uint64, error) {
	entries := []fileEntry{}
	var total uint64
	count := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		count++
		if count > 20000 {
			return fmt.Errorf("cache scan exceeds bounded entry limit")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		size := uint64(info.Size())
		entries = append(entries, fileEntry{path: path, size: size, mod: info.ModTime()})
		total += size
		return nil
	})
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].mod.Equal(entries[j].mod) {
			return entries[i].path < entries[j].path
		}
		return entries[i].mod.Before(entries[j].mod)
	})
	return entries, total, err
}
func removeOwnedFile(root, path string) error {
	cleanRoot := filepath.Clean(root)
	clean := filepath.Clean(path)
	if !strings.HasPrefix(clean, cleanRoot+string(filepath.Separator)) {
		return fmt.Errorf("cache deletion escaped ownership root")
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular cache deletion")
	}
	return os.Remove(clean)
}
func fsSpace(path string) (uint64, uint64) {
	var stat syscall.Statfs_t
	if syscall.Statfs(path, &stat) != nil {
		return 0, 0
	}
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize)
}
