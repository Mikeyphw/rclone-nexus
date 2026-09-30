package doctor

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"rclone-nexus/internal/diagnostics"
	"rclone-nexus/internal/integrity"
	"rclone-nexus/internal/jobs"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/policy"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/rootmgr"
	"rclone-nexus/internal/supervisor"
)

const (
	Pass = "PASS"
	Warn = "WARN"
	Fail = "FAIL"
)

type Check struct {
	Code     string `json:"code"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
	Guidance string `json:"guidance,omitempty"`
}

type Report struct {
	SchemaVersion   int     `json:"schema_version"`
	GeneratedUnixMS int64   `json:"generated_unix_ms"`
	Overall         string  `json:"overall"`
	Checks          []Check `json:"checks"`
}

type BundleResult struct {
	BundleID string `json:"bundle_id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

func add(checks *[]Check, code, status, summary, detail, guidance string) {
	*checks = append(*checks, Check{Code: code, Status: status, Summary: summary, Detail: detail, Guidance: guidance})
}

func Run(ctx context.Context, p paths.Paths) Report {
	p = p.Normalize()
	_ = p.EnsureState()
	checks := []Check{}
	ps := provider.Discover(p)
	if ps.ModuleReady {
		add(&checks, "provider.module", Pass, "Provider module detected", "", "")
	} else {
		add(&checks, "provider.module", Fail, "Provider module missing", "module id rclone not detected", "Install/enable NewFuture rclone-fuse3-magisk")
	}
	if ps.BinaryReady {
		add(&checks, "provider.binary", Pass, "rclone binary ready", ps.RcloneVersion, "")
	} else {
		add(&checks, "provider.binary", Fail, "rclone binary unavailable", "", "Verify provider module installation")
	}
	if ps.ConfigReady {
		add(&checks, "provider.config", Pass, "rclone configuration ready", "", "")
	} else {
		add(&checks, "provider.config", Fail, "rclone configuration unavailable", "", "Run rclone config through the provider module")
	}
	if ps.FuseDeviceReady {
		add(&checks, "fuse.device", Pass, "FUSE device available", "", "")
	} else {
		add(&checks, "fuse.device", Fail, "FUSE device unavailable", "", "Verify kernel/root environment exposes /dev/fuse")
	}

	filesystems := os.Getenv("RNEXUS_PROC_FILESYSTEMS")
	if filesystems == "" {
		filesystems = "/proc/filesystems"
	}
	if data, err := os.ReadFile(filesystems); err == nil && (bytes.Contains(data, []byte("fuse")) || ps.FuseDeviceReady) {
		add(&checks, "kernel.fuse", Pass, "Kernel FUSE support observable", "", "")
	} else if ps.FuseDeviceReady {
		add(&checks, "kernel.fuse", Warn, "Kernel FUSE filesystem list unavailable", "device exists", "Verify mount capability if rclone mount fails")
	} else {
		add(&checks, "kernel.fuse", Fail, "Kernel FUSE support unavailable", "", "Use a kernel/root environment with FUSE support")
	}
	if ps.FuseHelperReady {
		add(&checks, "fuse.helper", Pass, "FUSE helper available", "", "")
	} else {
		add(&checks, "fuse.helper", Warn, "fusermount3 helper unavailable", "", "Provider may still work on builds that do not require the helper")
	}

	rm := rootmgr.Detect()
	if rm.Compatible {
		add(&checks, "root.manager", Pass, "Root module manager compatible", rm.Name, "")
	} else {
		add(&checks, "root.manager", Warn, "Root module manager not identified", "", "Nexus will use only conservative common module capabilities")
	}

	if info, err := os.Stat(p.StateDir); err == nil && info.IsDir() && info.Mode().Perm() == 0o700 {
		if os.Geteuid() != 0 || info.Sys() == nil {
			add(&checks, "state.permissions", Pass, "Persistent state mode is private", "0700", "")
		} else {
			add(&checks, "state.permissions", Pass, "Persistent state mode is private", "0700", "")
		}
	} else {
		add(&checks, "state.permissions", Fail, "Persistent state permissions are unsafe", "expected mode 0700", "Run Nexus as root and repair state permissions")
	}

	obs := policy.Observe(ctx, p)
	netStatus := Warn
	netSummary := "Network offline or unknown"
	if obs.Online {
		netStatus, netSummary = Pass, "Network available"
	}
	add(&checks, "network", netStatus, netSummary, obs.NetworkClass, "")
	if obs.CacheFreeBytes > 0 {
		add(&checks, "storage.cache", Pass, "Cache filesystem available", fmt.Sprintf("free_bytes=%d", obs.CacheFreeBytes), "")
	} else {
		add(&checks, "storage.cache", Warn, "Cache filesystem capacity unavailable", "", "Verify persistent-state filesystem")
	}

	if _, err := os.Stat("/proc/self/ns/mnt"); err == nil {
		add(&checks, "namespace.self", Pass, "Mount namespace introspection available", "", "")
	} else {
		add(&checks, "namespace.self", Warn, "Mount namespace introspection unavailable", "", "Android app visibility qualification will be unavailable")
	}

	registry, err := mounts.LoadRegistry(p)
	if err == nil {
		add(&checks, "mounts.registry", Pass, "Mount registry readable", fmt.Sprintf("mounts=%d", len(registry.Mounts)), "")
	} else {
		add(&checks, "mounts.registry", Fail, "Mount registry invalid", err.Error(), "Fix or rollback Nexus mount configuration")
	}
	health, err := supervisor.InspectAll(ctx, p, false)
	if err == nil {
		add(&checks, "mounts.health", Pass, "Mount health readable", fmt.Sprintf("mounts=%d", len(health)), "")
	} else {
		add(&checks, "mounts.health", Warn, "Mount health inspection incomplete", err.Error(), "Inspect individual mount health")
	}
	jr, err := jobs.Snapshot(p)
	if err == nil {
		add(&checks, "jobs.registry", Pass, "Job registry readable", fmt.Sprintf("jobs=%d", len(jr.Jobs)), "")
	} else {
		add(&checks, "jobs.registry", Fail, "Job registry invalid", err.Error(), "Fix scheduled-job configuration")
	}

	ir := integrity.Verify(p.ModuleDir)
	switch {
	case ir.OK:
		add(&checks, "module.integrity", Pass, "Module integrity manifest verified", fmt.Sprintf("files=%d", ir.Checked), "")
	case ir.Available:
		add(&checks, "module.integrity", Fail, "Module integrity verification failed", strings.Join(ir.Issues, ","), "Reinstall the exact Rclone Nexus release")
	default:
		add(&checks, "module.integrity", Warn, "Module integrity manifest unavailable", "", "Development/source trees may not contain the packaged manifest")
	}

	private := []string{p.StateDir, p.ModuleDir, p.ProviderModuleDir, p.RcloneConfig}
	for i := range checks {
		checks[i].Detail = diagnostics.SanitizeText(checks[i].Detail, private...)
		checks[i].Guidance = diagnostics.SanitizeText(checks[i].Guidance, private...)
	}
	overall := Pass
	for _, c := range checks {
		if c.Status == Fail {
			overall = Fail
			break
		}
		if c.Status == Warn && overall == Pass {
			overall = Warn
		}
	}
	return Report{SchemaVersion: 1, GeneratedUnixMS: nowUnixMS(), Overall: overall, Checks: checks}
}

func nowUnixMS() int64 {
	if value := strings.TrimSpace(os.Getenv("RNEXUS_TEST_NOW_MS")); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return parsed
		}
	}
	return time.Now().UnixMilli()
}

func deterministicTime() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

type bundleEntry struct {
	name string
	data []byte
}

func BuildBundle(ctx context.Context, p paths.Paths) (BundleResult, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return BundleResult{}, err
	}
	report := Run(ctx, p)
	for i := range report.Checks {
		if report.Checks[i].Code == "storage.cache" {
			report.Checks[i].Detail = ""
		}
	}
	private := []string{p.StateDir, p.ModuleDir, p.ProviderModuleDir, p.RcloneConfig}
	entries := []bundleEntry{}
	addJSON := func(name string, v any) error {
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			return e
		}
		b = append(b, '\n')
		b = []byte(diagnostics.SanitizeText(string(b), private...))
		entries = append(entries, bundleEntry{name, b})
		return nil
	}
	if err := addJSON("doctor.json", report); err != nil {
		return BundleResult{}, err
	}
	if err := addJSON("root-manager.json", rootmgr.Detect()); err != nil {
		return BundleResult{}, err
	}
	if err := addJSON("provider.json", provider.Discover(p)); err != nil {
		return BundleResult{}, err
	}
	if snap, err := mounts.PublicSnapshot(p); err == nil {
		_ = addJSON("mounts.json", snap)
	}
	if js, err := jobs.Snapshot(p); err == nil {
		_ = addJSON("jobs.json", js)
	}
	for _, dir := range []string{p.DiagnosticsDir, p.LogDir} {
		files, _ := os.ReadDir(dir)
		for _, item := range files {
			if item.IsDir() {
				continue
			}
			name := item.Name()
			if strings.HasSuffix(name, ".zip") {
				continue
			}
			data, err := tailFile(filepath.Join(dir, name), 64<<10)
			if err != nil {
				continue
			}
			clean := diagnostics.SanitizeText(string(data), private...)
			entries = append(entries, bundleEntry{"logs/" + name, []byte(clean)})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	manifest := map[string]string{}
	for _, e := range entries {
		sum := sha256.Sum256(e.data)
		manifest[e.name] = hex.EncodeToString(sum[:])
	}
	mb, _ := json.MarshalIndent(map[string]any{"schema_version": 1, "entries": manifest}, "", "  ")
	entries = append(entries, bundleEntry{"manifest.json", append(mb, '\n')})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetModTime(deterministicTime())
		h.SetMode(0o600)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return BundleResult{}, err
		}
		if _, err = w.Write(e.data); err != nil {
			return BundleResult{}, err
		}
	}
	if err := zw.Close(); err != nil {
		return BundleResult{}, err
	}
	sum := sha256.Sum256(buf.Bytes())
	id := hex.EncodeToString(sum[:8])
	filename := "rclone-nexus-support-" + id + ".zip"
	path := filepath.Join(p.SupportDir, filename)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return BundleResult{}, err
	}
	_ = os.Chmod(path, 0o600)
	return BundleResult{BundleID: id, Filename: filename, Size: int64(buf.Len()), SHA256: hex.EncodeToString(sum[:])}, nil
}

func tailFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := st.Size() - max
	if start < 0 {
		start = 0
	}
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, max))
}
