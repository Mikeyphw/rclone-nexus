package mounts

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"rclone-nexus/internal/paths"
)

func withFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func withMountLock(p paths.Paths, name string, fn func() error) error {
	p = p.Normalize()
	if !ValidName(name) {
		return fmt.Errorf("invalid mount name: %s", name)
	}
	return withFileLock(filepath.Join(p.LockDir, "mount-"+name+".lock"), fn)
}

func withConfigLock(p paths.Paths, fn func() error) error {
	p = p.Normalize()
	return withFileLock(filepath.Join(p.LockDir, "config.lock"), fn)
}
