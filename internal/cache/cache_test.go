package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rclone-nexus/internal/paths"
)

func cachePaths(t *testing.T) paths.Paths {
	t.Helper()
	p := paths.Paths{StateDir: filepath.Join(t.TempDir(), "state")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	return p
}
func writeSized(t *testing.T, path string, size int, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestPruneHighLowWaterOldestFirst(t *testing.T) {
	p := cachePaths(t)
	dir, err := OwnedDir(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	writeSized(t, filepath.Join(dir, "old"), 40, 3*time.Hour)
	writeSized(t, filepath.Join(dir, "mid"), 40, 2*time.Hour)
	writeSized(t, filepath.Join(dir, "new"), 40, time.Hour)
	limits := Limits{MaxBytes: 100, HighPercent: 90, LowPercent: 50}
	before, err := Inspect(p, "drive", limits)
	if err != nil {
		t.Fatal(err)
	}
	if !before.AboveHigh || before.Bytes != 120 {
		t.Fatalf("unexpected pressure: %+v", before)
	}
	preview, err := PrunePreview(p, "drive", limits)
	if err != nil {
		t.Fatal(err)
	}
	if preview.DeleteFiles != 2 || preview.DeleteBytes != 80 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if _, err := Prune(context.Background(), p, "drive", limits); err != nil {
		t.Fatal(err)
	}
	after, err := Inspect(p, "drive", limits)
	if err != nil {
		t.Fatal(err)
	}
	if after.Bytes != 40 || after.Files != 1 {
		t.Fatalf("prune did not reach low water: %+v", after)
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); err != nil {
		t.Fatalf("newest file should remain: %v", err)
	}
}

func TestCacheOwnershipRejectsSymlinkRoot(t *testing.T) {
	p := cachePaths(t)
	dir, _ := OwnedDir(p, "drive")
	external := t.TempDir()
	_ = os.RemoveAll(dir)
	if err := os.Symlink(external, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(p, "drive", Limits{}); err == nil {
		t.Fatal("expected symlink ownership refusal")
	}
}
func TestForgetResetsOnlyOwnedTree(t *testing.T) {
	p := cachePaths(t)
	dir, _ := OwnedDir(p, "drive")
	other := filepath.Join(p.CacheDir, "other")
	writeSized(t, filepath.Join(dir, "a"), 10, time.Hour)
	writeSized(t, filepath.Join(other, "keep"), 10, time.Hour)
	if _, err := Forget(context.Background(), p, "drive"); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("owned tree not reset: entries=%v err=%v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(other, "keep")); err != nil {
		t.Fatalf("unrelated cache changed: %v", err)
	}
}
func TestCancelledPruneDoesNotDelete(t *testing.T) {
	p := cachePaths(t)
	dir, _ := OwnedDir(p, "drive")
	writeSized(t, filepath.Join(dir, "a"), 100, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Prune(ctx, p, "drive", Limits{MaxBytes: 50, HighPercent: 90, LowPercent: 50}); err == nil {
		t.Fatal("expected cancellation")
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); err != nil {
		t.Fatalf("cancelled prune deleted file: %v", err)
	}
}
