package websettings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"rclone-nexus/internal/paths"
	"sort"
	"strings"
	"syscall"
)

type Settings struct {
	RefreshSeconds   int    `json:"refresh_seconds"`
	LogFollow        bool   `json:"log_follow"`
	LogLimit         int    `json:"log_limit"`
	LogMaxBytes      int64  `json:"log_max_bytes"`
	LogBackups       int    `json:"log_backups"`
	DenseMode        bool   `json:"dense_mode"`
	ReducedMotion    bool   `json:"reduced_motion"`
	DefaultView      string `json:"default_view"`
	DefaultMountView string `json:"default_mount_view"`
	WebUIIdleSeconds int    `json:"webui_idle_seconds"`
}
type Snapshot struct {
	SchemaVersion int      `json:"schema_version"`
	Revision      uint64   `json:"revision"`
	Digest        string   `json:"digest"`
	Settings      Settings `json:"settings"`
}
type Preview struct {
	CurrentRevision uint64   `json:"current_revision"`
	CandidateDigest string   `json:"candidate_digest"`
	Settings        Settings `json:"settings"`
}

func defaults() Settings {
	return Settings{RefreshSeconds: 5, LogFollow: true, LogLimit: 100, LogMaxBytes: 1 << 20, LogBackups: 4, DefaultView: "home", DefaultMountView: "cards", WebUIIdleSeconds: 600}
}
func normalize(s Settings) (Settings, error) {
	if s.RefreshSeconds == 0 {
		s.RefreshSeconds = 5
	}
	if s.LogLimit == 0 {
		s.LogLimit = 100
	}
	if s.LogMaxBytes == 0 {
		s.LogMaxBytes = 1 << 20
	}
	if s.LogBackups == 0 {
		s.LogBackups = 4
	}
	if s.WebUIIdleSeconds == 0 {
		s.WebUIIdleSeconds = 600
	}
	if s.DefaultMountView == "" {
		s.DefaultMountView = "cards"
	}
	if s.DefaultView == "" {
		s.DefaultView = "home"
	}
	if s.RefreshSeconds < 2 || s.RefreshSeconds > 60 {
		return s, fmt.Errorf("refresh_seconds must be 2..60")
	}
	if s.LogLimit < 20 || s.LogLimit > 500 {
		return s, fmt.Errorf("log_limit must be 20..500")
	}
	if s.LogMaxBytes < 16<<10 || s.LogMaxBytes > 64<<20 {
		return s, fmt.Errorf("log_max_bytes must be 16384..67108864")
	}
	if s.LogBackups < 1 || s.LogBackups > 12 {
		return s, fmt.Errorf("log_backups must be 1..12")
	}
	if s.WebUIIdleSeconds < 30 || s.WebUIIdleSeconds > 3600 {
		return s, fmt.Errorf("webui_idle_seconds must be 30..3600")
	}
	if s.DefaultMountView != "cards" && s.DefaultMountView != "dense" {
		return s, fmt.Errorf("invalid default_mount_view")
	}
	if s.DefaultMountView == "dense" {
		s.DenseMode = true
	}
	allowed := map[string]bool{"home": true, "mounts": true, "jobs": true, "runtime": true, "logs": true, "settings": true}
	if !allowed[s.DefaultView] {
		return s, fmt.Errorf("invalid default_view")
	}
	return s, nil
}
func digest(s Settings) string {
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func path(p paths.Paths) string {
	return filepath.Join(p.Normalize().StateDir, "webui", "settings-v1.json")
}
func Load(p paths.Paths) (Snapshot, error) {
	s := defaults()
	file := path(p)
	b, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return Snapshot{SchemaVersion: 1, Revision: 0, Digest: digest(s), Settings: s}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	if json.Unmarshal(b, &snap) != nil || snap.SchemaVersion != 1 {
		return Snapshot{}, fmt.Errorf("invalid WebUI settings")
	}
	normalized, err := normalize(snap.Settings)
	if err != nil {
		return Snapshot{}, err
	}
	if snap.Digest != digest(normalized) {
		return Snapshot{}, fmt.Errorf("WebUI settings digest mismatch")
	}
	snap.Settings = normalized
	return snap, nil
}
func PreviewCandidate(p paths.Paths, s Settings) (Preview, error) {
	cur, err := Load(p)
	if err != nil {
		return Preview{}, err
	}
	n, err := normalize(s)
	if err != nil {
		return Preview{}, err
	}
	return Preview{CurrentRevision: cur.Revision, CandidateDigest: digest(n), Settings: n}, nil
}
func Apply(p paths.Paths, expected uint64, expectedDigest string, s Settings) (Snapshot, error) {
	var out Snapshot
	dir := filepath.Dir(path(p))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return out, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return out, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "settings.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return out, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return out, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	prev, err := PreviewCandidate(p, s)
	if err != nil {
		return out, err
	}
	if prev.CurrentRevision != expected {
		return out, fmt.Errorf("stale WebUI settings revision")
	}
	if prev.CandidateDigest != expectedDigest {
		return out, fmt.Errorf("WebUI settings digest mismatch")
	}
	out = Snapshot{SchemaVersion: 1, Revision: expected + 1, Digest: expectedDigest, Settings: prev.Settings}
	payload, _ := json.MarshalIndent(out, "", "  ")
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(dir, ".settings-*")
	if err != nil {
		return Snapshot{}, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	_ = tmp.Chmod(0o600)
	if _, err = tmp.Write(payload); err != nil {
		tmp.Close()
		return Snapshot{}, err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return Snapshot{}, err
	}
	if err = tmp.Close(); err != nil {
		return Snapshot{}, err
	}
	if err = os.Rename(name, path(p)); err != nil {
		return Snapshot{}, err
	}
	if err = os.Chmod(path(p), 0o600); err != nil {
		return Snapshot{}, err
	}
	dirHandle, err := os.Open(dir)
	if err != nil {
		return Snapshot{}, err
	}
	if err = dirHandle.Sync(); err != nil {
		dirHandle.Close()
		return Snapshot{}, err
	}
	if err = dirHandle.Close(); err != nil {
		return Snapshot{}, err
	}
	return out, nil
}
func Keys() []string {
	v := []string{"default_mount_view", "default_view", "dense_mode", "log_backups", "log_follow", "log_limit", "log_max_bytes", "reduced_motion", "refresh_seconds", "webui_idle_seconds"}
	sort.Strings(v)
	return v
}
func SafeDescription(s Settings) string {
	return strings.TrimSpace(fmt.Sprintf("refresh=%ds logs=%d retention=%d/%d follow=%t view=%s mount_view=%s idle=%ds", s.RefreshSeconds, s.LogLimit, s.LogMaxBytes, s.LogBackups, s.LogFollow, s.DefaultView, s.DefaultMountView, s.WebUIIdleSeconds))
}
