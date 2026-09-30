package diagnostics

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/redact"
)

const (
	defaultMaxLogBytes = int64(1 << 20)
	defaultBackups     = 4
)

type Event struct {
	SchemaVersion int    `json:"schema_version"`
	TimeUnixMS    int64  `json:"time_unix_ms"`
	Category      string `json:"category"`
	Name          string `json:"name"`
	State         string `json:"state,omitempty"`
	Code          string `json:"code,omitempty"`
	Data          any    `json:"data,omitempty"`
}

func envInt64(name string, fallback, min, max int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < min || parsed > max {
		return fallback
	}
	return parsed
}

func logLimits() (int64, int) {
	max := envInt64("RNEXUS_LOG_MAX_BYTES", defaultMaxLogBytes, 16<<10, 64<<20)
	backups := int(envInt64("RNEXUS_LOG_BACKUPS", defaultBackups, 1, 12))
	return max, backups
}

func securePrivateFile(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

func Rotate(path string, maxBytes int64, backups int) error {
	if maxBytes <= 0 || backups < 1 {
		return nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}
	for i := backups - 1; i >= 1; i-- {
		older := fmt.Sprintf("%s.%d", path, i)
		newer := fmt.Sprintf("%s.%d", path, i+1)
		if _, err := os.Stat(older); err == nil {
			_ = os.Rename(older, newer)
		}
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", path, backups+1))
	if err := os.Rename(path, path+".1"); err != nil {
		return err
	}
	for i := 1; i <= backups; i++ {
		backup := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Stat(backup); err == nil {
			if err := securePrivateFile(backup); err != nil {
				return err
			}
		}
	}
	return nil
}

func RotateActive(path string, maxBytes int64, backups int) error {
	if maxBytes <= 0 || backups < 1 {
		return nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}
	for i := backups - 1; i >= 1; i-- {
		older := fmt.Sprintf("%s.%d", path, i)
		newer := fmt.Sprintf("%s.%d", path, i+1)
		if _, err := os.Stat(older); err == nil {
			_ = os.Rename(older, newer)
		}
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", path, backups+1))
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	start := info.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		f.Close()
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	f.Close()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".1", data, 0o600); err != nil {
		return err
	}
	if err := securePrivateFile(path + ".1"); err != nil {
		return err
	}
	current, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := current.Close(); err != nil {
		return err
	}
	return securePrivateFile(path)
}

func RotateRuntimeLogs(p paths.Paths) {
	p = p.Normalize()
	maxBytes, backups := logLimits()
	entries, _ := os.ReadDir(p.LogDir)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "nexus.log" || name == "service.log" || name == "racd.log" || strings.HasPrefix(name, "mount-") {
			_ = RotateActive(filepath.Join(p.LogDir, name), maxBytes, backups)
		}
	}
}

func Append(p paths.Paths, category, name, state, code string, data any) error {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return err
	}
	maxBytes, backups := logLimits()
	path := filepath.Join(p.DiagnosticsDir, "events.jsonl")
	if err := Rotate(path, maxBytes, backups); err != nil {
		return err
	}
	event := Event{
		SchemaVersion: 1, TimeUnixMS: time.Now().UnixMilli(), Category: category,
		Name: redact.BoundedString(name, 256), State: redact.BoundedString(state, 128),
		Code: redact.BoundedString(code, 128), Data: redact.Value(data, 4096),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := securePrivateFile(path); err != nil {
		return err
	}
	if _, err := f.Write(append(payload, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

var secretPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential|authorization|cookie|client_secret|refresh_token|access_token|api_key|apikey)([[:space:]]*[=:][[:space:]]*)([^[:space:],;]+)`)
var bearerPattern = regexp.MustCompile(`(?i)Bearer[[:space:]]+[^[:space:],;]+`)

func SanitizeText(value string, privatePaths ...string) string {
	value = bearerPattern.ReplaceAllString(value, `Bearer <redacted>`)
	value = secretPattern.ReplaceAllString(value, `$1$2<redacted>`)
	for _, path := range privatePaths {
		clean := filepath.Clean(strings.TrimSpace(path))
		if clean == "." || clean == "/" || clean == "" {
			continue
		}
		value = strings.ReplaceAll(value, clean, "<private-path>")
	}
	return redact.BoundedString(value, 256<<10)
}
