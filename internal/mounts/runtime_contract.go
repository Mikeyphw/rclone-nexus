package mounts

import (
	"path/filepath"
)

// RuntimeQualificationArgv is the canonical superset of command/global flags
// Nexus may generate for a managed rclone mount. Runtime qualification probes
// the candidate's command and global help against this exact contract before
// any candidate can become qualified.
func RuntimeQualificationArgv(configPath, workDir string) []string {
	return []string{
		"mount", "nexus_qual:" + filepath.Join(workDir, "source"), filepath.Join(workDir, "mount"),
		"--config", configPath,
		"--vfs-cache-mode", "full",
		"--cache-dir", filepath.Join(workDir, "cache"),
		"--log-file", filepath.Join(workDir, "mount.log"),
		"--log-level", "INFO",
		"--vfs-cache-max-size", "64MiB",
		"--vfs-cache-max-age", "1m",
		"--dir-cache-time", "1m",
		"--poll-interval", "1m",
		"--allow-other",
		"--read-only",
		"--rc",
		"--rc-addr", "127.0.0.1:0",
		"--rc-user", "nexus-qual",
		"--rc-pass", "qualification-secret",
	}
}

// RuntimeSmokeArgv uses the same production-required mount/global/RC surface
// but avoids optional policy flags that can change Android mount semantics.
// It is intentionally suitable for a short-lived local FUSE qualification.
func RuntimeSmokeArgv(configPath, sourceDir, mountpoint, cacheDir, logPath, rcAddr, rcUser, rcPass string) []string {
	return []string{
		"mount", "nexus_qual:" + sourceDir, mountpoint,
		"--config", configPath,
		"--vfs-cache-mode", "off",
		"--cache-dir", cacheDir,
		"--log-file", logPath,
		"--log-level", "INFO",
		"--rc",
		"--rc-addr", rcAddr,
		"--rc-user", rcUser,
		"--rc-pass", rcPass,
	}
}
