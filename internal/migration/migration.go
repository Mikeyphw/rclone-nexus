package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/jobs"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimeauth"
)

const SchemaVersion = 1

type Phase string

const (
	PhasePrepared                Phase = "PREPARED"
	PhaseQuiescing               Phase = "QUIESCING_PROVIDER"
	PhaseApplying                Phase = "APPLYING_IMPORT"
	PhaseAwaitingProviderDisable Phase = "AWAITING_PROVIDER_DISABLE"
	PhaseFinalizing              Phase = "FINALIZING_AUTHORITY"
	PhaseCompleted               Phase = "COMPLETED"
	PhaseRolledBack              Phase = "ROLLED_BACK"
	PhaseConflict                Phase = "CONFLICT"
)

type ProviderProcess struct {
	PID        int    `json:"pid"`
	Kind       string `json:"kind"`
	Remote     string `json:"remote,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`
}

type MountCandidate struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Remote     string `json:"remote"`
	Mountpoint string `json:"mountpoint"`
	PID        int    `json:"pid,omitempty"`
}

type JobCandidate struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Options     []string `json:"options,omitempty"`
	Importable  bool     `json:"importable"`
	Reason      string   `json:"reason,omitempty"`
	SourceFile  string   `json:"source_file"`
	Line        int      `json:"line"`
}

type Detection struct {
	SchemaVersion         int               `json:"schema_version"`
	ProviderPresent       bool              `json:"provider_present"`
	ProviderEnabled       bool              `json:"provider_enabled"`
	ProviderModuleDir     string            `json:"provider_module_dir"`
	ProviderModuleVersion string            `json:"provider_module_version,omitempty"`
	ProviderBinary        string            `json:"provider_binary,omitempty"`
	ProviderRcloneVersion string            `json:"provider_rclone_version,omitempty"`
	ProviderConfig        string            `json:"provider_config,omitempty"`
	ProviderConfigSHA256  string            `json:"provider_config_sha256,omitempty"`
	Processes             []ProviderProcess `json:"processes"`
	Mounts                []MountCandidate  `json:"mounts"`
	Jobs                  []JobCandidate    `json:"jobs"`
	ServiceActive         bool              `json:"service_active"`
	WebUIActive           bool              `json:"webui_active"`
	SchedulerActive       bool              `json:"scheduler_active"`
	InspectedUnixMS       int64             `json:"inspected_unix_ms"`
}

type PreviewRequest struct {
	SelectedMounts []string `json:"selected_mounts,omitempty"`
	SelectedJobs   []string `json:"selected_jobs,omitempty"`
	JobEvery       string   `json:"job_every,omitempty"`
}

type Preview struct {
	SchemaVersion           int                   `json:"schema_version"`
	Generation              uint64                `json:"generation"`
	CandidateDigest         string                `json:"candidate_digest"`
	Detection               Detection             `json:"detection"`
	SelectedMounts          []MountCandidate      `json:"selected_mounts"`
	SelectedJobs            []jobs.Config         `json:"selected_jobs"`
	MountRegistryBefore     mounts.PublicRegistry `json:"mount_registry_before"`
	JobRegistryBefore       jobs.Registry         `json:"job_registry_before"`
	Conflicts               []string              `json:"conflicts,omitempty"`
	CanApply                bool                  `json:"can_apply"`
	RequiresProviderDisable bool                  `json:"requires_provider_disable"`
}

type FinalizePreview struct {
	SchemaVersion   int       `json:"schema_version"`
	Generation      uint64    `json:"generation"`
	CandidateDigest string    `json:"candidate_digest"`
	State           State     `json:"state"`
	Detection       Detection `json:"detection"`
	Conflicts       []string  `json:"conflicts,omitempty"`
	CanFinalize     bool      `json:"can_finalize"`
}

type State struct {
	SchemaVersion            int      `json:"schema_version"`
	Generation               uint64   `json:"generation"`
	Phase                    Phase    `json:"phase"`
	TransactionID            string   `json:"transaction_id"`
	CandidateDigest          string   `json:"candidate_digest"`
	ProviderConfigSHA256     string   `json:"provider_config_sha256,omitempty"`
	BackupDir                string   `json:"backup_dir,omitempty"`
	ImportedMounts           []string `json:"imported_mounts,omitempty"`
	ImportedJobs             []string `json:"imported_jobs,omitempty"`
	SelectedMounts           []string `json:"selected_mounts,omitempty"`
	SelectedJobs             []string `json:"selected_jobs,omitempty"`
	ProviderQuiesced         bool     `json:"provider_quiesced"`
	ProviderProcessesStopped int      `json:"provider_processes_stopped"`
	ProviderDisableRequired  bool     `json:"provider_disable_required"`
	ActiveRuntimeID          string   `json:"active_runtime_id,omitempty"`
	ActiveRuntimeSHA256      string   `json:"active_runtime_sha256,omitempty"`
	ConfigSHA256             string   `json:"config_sha256,omitempty"`
	EvidencePath             string   `json:"evidence_path,omitempty"`
	LastError                string   `json:"last_error,omitempty"`
	Recovery                 string   `json:"recovery,omitempty"`
	UpdatedUnixMS            int64    `json:"updated_unix_ms"`
}

type Evidence struct {
	SchemaVersion            int      `json:"schema_version"`
	TransactionID            string   `json:"transaction_id"`
	ProviderConfigSHA256     string   `json:"provider_config_sha256"`
	ManagedConfigSHA256      string   `json:"managed_config_sha256"`
	ActiveRuntimeID          string   `json:"active_runtime_id"`
	ActiveRuntimeSHA256      string   `json:"active_runtime_sha256"`
	ImportedMounts           []string `json:"imported_mounts"`
	ImportedJobs             []string `json:"imported_jobs"`
	ProviderProcessesStopped int      `json:"provider_processes_stopped"`
	ProviderPresentAfter     bool     `json:"provider_present_after"`
	ProviderEnabledAfter     bool     `json:"provider_enabled_after"`
	CompletedUnixMS          int64    `json:"completed_unix_ms"`
}

func nowMS() int64 { return time.Now().UnixMilli() }

func hashBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isRegular(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}
func isExecutable(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0o111 != 0
}

func providerEnabled(dir string) bool {
	if !isDir(dir) {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "disable")); err == nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "remove")); err == nil {
		return false
	}
	return true
}
func isDir(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }

func moduleVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "module.prop"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "version" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func providerBinary(dir string) string {
	for _, rel := range []string{"system/vendor/bin/rclone", "vendor/bin/rclone", "system/bin/rclone", "bin/rclone", "rclone"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if isExecutable(p) {
			return p
		}
	}
	return ""
}

func binaryVersion(path string) string {
	if path == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if len(line) > 512 {
		line = line[:512]
	}
	return line
}

func procRoot() string {
	if v := strings.TrimSpace(os.Getenv("RNEXUS_MIGRATION_PROC_ROOT")); v != "" {
		return filepath.Clean(v)
	}
	return "/proc"
}

func splitNUL(b []byte) []string {
	parts := strings.Split(string(b), "\x00")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
func within(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	p := filepath.Clean(path)
	r := filepath.Clean(root)
	rel, err := filepath.Rel(r, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func classifyProcess(providerDir string, pid int, args []string, exe, cwd string) (ProviderProcess, bool) {
	owned := within(exe, providerDir) || within(cwd, providerDir)
	for _, arg := range args {
		if within(arg, providerDir) || strings.Contains(arg, providerDir+string(filepath.Separator)) {
			owned = true
		}
	}
	if !owned {
		return ProviderProcess{}, false
	}
	p := ProviderProcess{PID: pid, Kind: "service"}
	joined := strings.ToLower(strings.Join(args, " "))
	if strings.Contains(joined, "rclone-web") || strings.Contains(joined, "rc-web-gui") || strings.Contains(joined, "--rc-addr") || strings.Contains(joined, " rcd ") {
		p.Kind = "webui"
	}
	if strings.Contains(joined, "rclone-sync") || strings.Contains(joined, "/conf/sync") || strings.Contains(joined, "/conf/copy") {
		p.Kind = "scheduler"
	}
	for i, arg := range args {
		if arg == "mount" && i+2 < len(args) && !strings.HasPrefix(args[i+1], "-") && filepath.IsAbs(args[i+2]) {
			p.Kind = "mount"
			p.Remote = args[i+1]
			p.Mountpoint = filepath.Clean(args[i+2])
			break
		}
	}
	return p, true
}

func scanProcesses(providerDir string) []ProviderProcess {
	root := procRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return []ProviderProcess{}
	}
	out := []ProviderProcess{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		base := filepath.Join(root, entry.Name())
		cmdline, _ := os.ReadFile(filepath.Join(base, "cmdline"))
		args := splitNUL(cmdline)
		exe, _ := os.Readlink(filepath.Join(base, "exe"))
		cwd, _ := os.Readlink(filepath.Join(base, "cwd"))
		if p, ok := classifyProcess(providerDir, pid, args, exe, cwd); ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

func sanitizeName(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-._")
	if s == "" {
		s = "item"
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

func mountCandidates(processes []ProviderProcess) []MountCandidate {
	seen := map[string]bool{}
	out := []MountCandidate{}
	for _, p := range processes {
		if p.Kind != "mount" || p.Remote == "" || p.Mountpoint == "" {
			continue
		}
		key := p.Remote + "\x00" + p.Mountpoint
		if seen[key] {
			continue
		}
		seen[key] = true
		seed := sanitizeName(filepath.Base(p.Mountpoint))
		sum := sha256.Sum256([]byte(key))
		id := "mount:" + hex.EncodeToString(sum[:8])
		name := "migrated-" + seed + "-" + hex.EncodeToString(sum[:3])
		out = append(out, MountCandidate{ID: id, Name: name, Remote: p.Remote, Mountpoint: p.Mountpoint, PID: p.PID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func splitFields(line string) ([]string, error) {
	var out []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range line {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated escape/quote")
	}
	flush()
	return out, nil
}

func parseJobFile(path, typ string) []JobCandidate {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(b), "\n")
	out := []JobCandidate{}
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields, err := splitFields(line)
		id := fmt.Sprintf("%s:%d", filepath.Base(path), i+1)
		name := fmt.Sprintf("migrated-%s-%03d", typ, i+1)
		c := JobCandidate{ID: id, Name: name, Type: typ, SourceFile: path, Line: i + 1}
		if err != nil {
			c.Reason = err.Error()
			out = append(out, c)
			continue
		}
		if len(fields) < 2 {
			c.Reason = "job requires source and destination"
			out = append(out, c)
			continue
		}
		c.Source, c.Destination = fields[0], fields[1]
		if len(fields) > 2 {
			c.Options = append([]string(nil), fields[2:]...)
			c.Reason = "provider job has options not representable by Nexus typed job model"
		} else {
			c.Importable = true
		}
		out = append(out, c)
	}
	return out
}

func Detect(p paths.Paths) (Detection, error) {
	p = p.Normalize()
	d := Detection{SchemaVersion: SchemaVersion, ProviderModuleDir: p.ProviderModuleDir, InspectedUnixMS: nowMS(), Processes: []ProviderProcess{}, Mounts: []MountCandidate{}, Jobs: []JobCandidate{}}
	info, err := os.Stat(p.ProviderModuleDir)
	if err != nil {
		if os.IsNotExist(err) {
			return d, nil
		}
		return d, err
	}
	if !info.IsDir() {
		return d, errors.New("legacy provider path is not a directory")
	}
	d.ProviderPresent = true
	d.ProviderEnabled = providerEnabled(p.ProviderModuleDir)
	d.ProviderModuleVersion = moduleVersion(p.ProviderModuleDir)
	d.ProviderBinary = providerBinary(p.ProviderModuleDir)
	d.ProviderRcloneVersion = binaryVersion(d.ProviderBinary)
	config := filepath.Join(p.ProviderModuleDir, "conf", "rclone.conf")
	if isRegular(config) {
		d.ProviderConfig = config
		d.ProviderConfigSHA256, _ = hashFile(config)
	}
	d.Processes = scanProcesses(p.ProviderModuleDir)
	d.Mounts = mountCandidates(d.Processes)
	for _, pr := range d.Processes {
		switch pr.Kind {
		case "webui":
			d.WebUIActive = true
		case "scheduler":
			d.SchedulerActive = true
		}
		if pr.Kind == "service" || pr.Kind == "mount" {
			d.ServiceActive = true
		}
	}
	d.Jobs = append(d.Jobs, parseJobFile(filepath.Join(p.ProviderModuleDir, "conf", "sync"), jobs.TypeSync)...)
	d.Jobs = append(d.Jobs, parseJobFile(filepath.Join(p.ProviderModuleDir, "conf", "copy"), jobs.TypeCopy)...)
	return d, nil
}

func candidateFromConfig(c mounts.Config) mounts.CandidateConfig {
	return mounts.CandidateConfig{Name: c.Name, Enabled: c.Enabled, Remote: c.Remote, Mountpoint: c.Mountpoint, VFSCacheMode: c.VFSCacheMode, VFSCacheMaxSize: c.VFSCacheMaxSize, VFSCacheMaxAge: c.VFSCacheMaxAge, DirCacheTime: c.DirCacheTime, PollInterval: c.PollInterval, AllowOther: c.AllowOther, ReadOnly: c.ReadOnly, LogLevel: c.LogLevel, RequireNetwork: c.RequireNetwork, ProbeRemote: c.ProbeRemote, NetworkMode: c.NetworkMode, ChargingOnly: c.ChargingOnly, MinBattery: c.MinBattery, MinFreeCacheSpace: c.MinFreeCacheSpace, BootSettle: c.BootSettle, NetworkSettle: c.NetworkSettle, VFSProfile: c.VFSProfile, CacheHighWater: c.CacheHighWater, CacheLowWater: c.CacheLowWater}
}

func selectedSet(requested []string, all []string, defaultAll bool) map[string]bool {
	s := map[string]bool{}
	if len(requested) == 0 && defaultAll {
		for _, v := range all {
			s[v] = true
		}
		return s
	}
	for _, v := range requested {
		s[v] = true
	}
	return s
}

func loadState(p paths.Paths) (State, bool, error) {
	p = p.Normalize()
	b, err := os.ReadFile(p.MigrationState)
	if os.IsNotExist(err) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err = json.Unmarshal(b, &s); err != nil {
		return State{}, false, err
	}
	if s.SchemaVersion != SchemaVersion {
		return State{}, false, errors.New("unsupported migration state schema")
	}
	return s, true, nil
}
func StateSnapshot(p paths.Paths) (State, bool, error) { return loadState(p) }
func generation(p paths.Paths) uint64 {
	s, ok, _ := loadState(p)
	if !ok {
		return 0
	}
	return s.Generation
}

func PreviewMigration(p paths.Paths, req PreviewRequest) (Preview, error) {
	p = p.Normalize()
	d, err := Detect(p)
	if err != nil {
		return Preview{}, err
	}
	mountsBefore, err := mounts.PublicSnapshot(p)
	if err != nil {
		return Preview{}, err
	}
	jobsBefore, err := jobs.Snapshot(p)
	if err != nil {
		return Preview{}, err
	}
	out := Preview{SchemaVersion: SchemaVersion, Generation: generation(p), Detection: d, SelectedMounts: []MountCandidate{}, SelectedJobs: []jobs.Config{}, MountRegistryBefore: mountsBefore, JobRegistryBefore: jobsBefore, Conflicts: []string{}, RequiresProviderDisable: d.ProviderPresent && d.ProviderEnabled}
	if !d.ProviderPresent {
		out.Conflicts = append(out.Conflicts, "legacy provider is not present")
	}
	if d.ProviderConfig == "" {
		out.Conflicts = append(out.Conflicts, "provider rclone.conf is missing")
	}
	mountIDs := make([]string, 0, len(d.Mounts))
	for _, m := range d.Mounts {
		mountIDs = append(mountIDs, m.ID)
	}
	ms := selectedSet(req.SelectedMounts, mountIDs, false)
	nameUsed := map[string]bool{}
	pointUsed := map[string]bool{}
	for _, m := range mountsBefore.Mounts {
		nameUsed[m.Name] = true
		pointUsed[filepath.Clean(m.Mountpoint)] = true
	}
	for _, m := range d.Mounts {
		if !ms[m.ID] {
			continue
		}
		if nameUsed[m.Name] {
			out.Conflicts = append(out.Conflicts, "mount name conflict: "+m.Name)
			continue
		}
		if pointUsed[filepath.Clean(m.Mountpoint)] {
			out.Conflicts = append(out.Conflicts, "duplicate destination path: "+m.Mountpoint)
			continue
		}
		nameUsed[m.Name] = true
		pointUsed[filepath.Clean(m.Mountpoint)] = true
		out.SelectedMounts = append(out.SelectedMounts, m)
	}
	jobIDs := make([]string, 0, len(d.Jobs))
	for _, j := range d.Jobs {
		if j.Importable {
			jobIDs = append(jobIDs, j.ID)
		}
	}
	js := selectedSet(req.SelectedJobs, jobIDs, false)
	existingJobs := map[string]bool{}
	for _, j := range jobsBefore.Jobs {
		existingJobs[j.Name] = true
	}
	for _, j := range d.Jobs {
		if !js[j.ID] {
			continue
		}
		if !j.Importable {
			out.Conflicts = append(out.Conflicts, "job not importable: "+j.ID+": "+j.Reason)
			continue
		}
		if req.JobEvery == "" {
			out.Conflicts = append(out.Conflicts, "selected provider jobs require explicit job_every")
			continue
		}
		if existingJobs[j.Name] {
			out.Conflicts = append(out.Conflicts, "imported job conflicts with existing Nexus job: "+j.Name)
			continue
		}
		c := jobs.Config{Name: j.Name, Enabled: false, Type: j.Type, Source: j.Source, Destination: j.Destination, Every: req.JobEvery, NetworkMode: "any", ConfirmDestructive: j.Type == jobs.TypeSync}
		if _, e := jobs.PreviewCandidate(p, append(append([]jobs.Config{}, jobsBefore.Jobs...), c)); e != nil {
			out.Conflicts = append(out.Conflicts, "job "+j.Name+": "+e.Error())
			continue
		}
		existingJobs[j.Name] = true
		out.SelectedJobs = append(out.SelectedJobs, c)
	}
	canonical := struct {
		ConfigSHA       string           `json:"config_sha256"`
		MountRev        uint64           `json:"mount_revision"`
		MountDigest     string           `json:"mount_digest"`
		JobRev          uint64           `json:"job_revision"`
		JobDigest       string           `json:"job_digest"`
		Mounts          []MountCandidate `json:"mounts"`
		Jobs            []jobs.Config    `json:"jobs"`
		ProviderEnabled bool             `json:"provider_enabled"`
	}{d.ProviderConfigSHA256, mountsBefore.Revision, mountsBefore.Digest, jobsBefore.Revision, jobsBefore.Digest, out.SelectedMounts, out.SelectedJobs, d.ProviderEnabled}
	b, _ := json.Marshal(canonical)
	out.CandidateDigest = hashBytes(b)
	out.CanApply = len(out.Conflicts) == 0
	return out, nil
}

func PreviewFinalize(p paths.Paths) (FinalizePreview, error) {
	p = p.Normalize()
	s, ok, err := loadState(p)
	if err != nil {
		return FinalizePreview{}, err
	}
	if !ok {
		return FinalizePreview{}, errors.New("no migration state")
	}
	d, err := Detect(p)
	if err != nil {
		return FinalizePreview{}, err
	}
	out := FinalizePreview{SchemaVersion: SchemaVersion, Generation: s.Generation, State: s, Detection: d}
	if s.Phase != PhaseAwaitingProviderDisable {
		out.Conflicts = append(out.Conflicts, fmt.Sprintf("migration cannot finalize from phase %s", s.Phase))
	}
	if d.ProviderEnabled {
		out.Conflicts = append(out.Conflicts, "legacy provider module is still enabled")
	}
	if len(d.Processes) > 0 {
		out.Conflicts = append(out.Conflicts, "legacy provider lifecycle still has active processes")
	}
	canonical := struct {
		Generation        uint64            `json:"generation"`
		Phase             Phase             `json:"phase"`
		TransactionID     string            `json:"transaction_id"`
		CandidateDigest   string            `json:"candidate_digest"`
		ProviderEnabled   bool              `json:"provider_enabled"`
		ProviderProcesses []ProviderProcess `json:"provider_processes"`
		ImportedMounts    []string          `json:"imported_mounts"`
		ImportedJobs      []string          `json:"imported_jobs"`
		ConfigSHA256      string            `json:"config_sha256"`
	}{s.Generation, s.Phase, s.TransactionID, s.CandidateDigest, d.ProviderEnabled, d.Processes, s.ImportedMounts, s.ImportedJobs, s.ConfigSHA256}
	b, _ := json.Marshal(canonical)
	out.CandidateDigest = hashBytes(b)
	out.CanFinalize = len(out.Conflicts) == 0
	return out, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".migration-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
func saveState(p paths.Paths, s State) error {
	p = p.Normalize()
	s.SchemaVersion = SchemaVersion
	s.UpdatedUnixMS = nowMS()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p.MigrationState, append(b, '\n'), 0o600)
}

func activeRuntime(p paths.Paths) (id, digest, binary string, err error) {
	binary, err = runtimeauth.Executable(p)
	if err != nil {
		return "", "", "", err
	}
	digest, err = hashFile(binary)
	if err != nil {
		return "", "", "", err
	}
	return "bundled", digest, binary, nil
}

func validateConfig(ctx context.Context, binary, config string) error {
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, binary, "listremotes", "--config", config, "--ask-password=false")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 2048 {
			detail = detail[len(detail)-2048:]
		}
		return fmt.Errorf("selected managed runtime rejected provider config: %w: %s", err, detail)
	}
	return nil
}

func writeBackups(p paths.Paths, tx string, m mounts.Registry, j jobs.Registry) (string, error) {
	dir := filepath.Join(p.Normalize().MigrationEvidenceDir, "tx-"+tx)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	type mb struct {
		Configs []mounts.CandidateConfig `json:"configs"`
	}
	ms := mb{Configs: make([]mounts.CandidateConfig, 0, len(m.Mounts))}
	for _, c := range m.Mounts {
		ms.Configs = append(ms.Configs, candidateFromConfig(c))
	}
	b, _ := json.MarshalIndent(ms, "", "  ")
	if err := writeAtomic(filepath.Join(dir, "mounts-before.json"), append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	b, _ = json.MarshalIndent(j, "", "  ")
	if err := writeAtomic(filepath.Join(dir, "jobs-before.json"), append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	cfg := p.Normalize().ManagedRcloneConfig
	if data, err := os.ReadFile(cfg); err == nil {
		if err := writeAtomic(filepath.Join(dir, "config-before.bin"), data, 0o600); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	} else {
		if err := writeAtomic(filepath.Join(dir, "config-before.absent"), []byte("absent\n"), 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

var afterProviderQuiesce = func() {}

func quiesceTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("RNEXUS_MIGRATION_QUIESCE_TIMEOUT_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms >= 50 && ms <= 30000 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 4 * time.Second
}

func quiesceProvider(ctx context.Context, d Detection) (int, error) {
	stopped := 0
	for _, pr := range d.Processes {
		if pr.PID <= 1 || pr.PID == os.Getpid() {
			continue
		}
		if err := syscall.Kill(pr.PID, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return stopped, fmt.Errorf("stop provider process %d: %w", pr.PID, err)
		}
	}
	deadline := time.Now().Add(quiesceTimeout())
	for time.Now().Before(deadline) {
		left := scanProcesses(d.ProviderModuleDir)
		if len(left) == 0 {
			return len(d.Processes), nil
		}
		select {
		case <-ctx.Done():
			return stopped, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	left := scanProcesses(d.ProviderModuleDir)
	if len(left) > 0 {
		return stopped, fmt.Errorf("provider lifecycle refused stop: %d process(es) remain", len(left))
	}
	return len(d.Processes), nil
}

func mountCandidateList(current mounts.Registry, imported []MountCandidate, enable bool) []mounts.CandidateConfig {
	out := make([]mounts.CandidateConfig, 0, len(current.Mounts)+len(imported))
	for _, c := range current.Mounts {
		out = append(out, candidateFromConfig(c))
	}
	for _, m := range imported {
		out = append(out, mounts.CandidateConfig{Name: m.Name, Enabled: enable, Remote: m.Remote, Mountpoint: m.Mountpoint, VFSCacheMode: "full", AllowOther: true, LogLevel: "INFO", NetworkMode: "any", ProbeRemote: true, VFSProfile: "balanced"})
	}
	return out
}
func jobCandidateList(current jobs.Registry, imported []jobs.Config, enable bool) []jobs.Config {
	out := append([]jobs.Config{}, current.Jobs...)
	for _, j := range imported {
		j.Enabled = enable
		out = append(out, j)
	}
	return out
}

func applyMounts(ctx context.Context, p paths.Paths, current mounts.Registry, candidates []mounts.CandidateConfig) (mounts.ConfigApplyReport, error) {
	pr, err := mounts.PreviewCandidate(p, candidates)
	if err != nil {
		return mounts.ConfigApplyReport{}, err
	}
	return mounts.ApplyCandidate(ctx, p, current.Revision, pr.CandidateDigest, candidates, nil)
}
func applyJobs(p paths.Paths, current jobs.Registry, candidates []jobs.Config) (jobs.Registry, error) {
	pr, err := jobs.PreviewCandidate(p, candidates)
	if err != nil {
		return jobs.Registry{}, err
	}
	return jobs.ApplyCandidate(p, current.Revision, pr.CandidateDigest, candidates)
}

func copyExact(src, dst string) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if err := writeAtomic(dst, b, 0o600); err != nil {
		return "", err
	}
	srcHash := hashBytes(b)
	dstHash, err := hashFile(dst)
	if err != nil {
		return "", err
	}
	if srcHash != dstHash {
		return "", errors.New("managed config copy hash mismatch")
	}
	return dstHash, nil
}

func withMigrationLock(p paths.Paths, fn func() error) error {
	p = p.Normalize()
	if err := os.MkdirAll(filepath.Dir(p.MigrationLock), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p.MigrationLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock migration authority: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func Apply(ctx context.Context, p paths.Paths, preview Preview) (State, error) {
	var out State
	err := withMigrationLock(p, func() error {
		var err error
		out, err = applyUnlocked(ctx, p, preview)
		return err
	})
	return out, err
}

func applyUnlocked(ctx context.Context, p paths.Paths, preview Preview) (State, error) {
	p = p.Normalize()
	if !preview.CanApply {
		return State{}, errors.New("migration preview contains conflicts")
	}
	if err := p.EnsureState(); err != nil {
		return State{}, err
	}
	fresh, err := PreviewMigration(p, PreviewRequest{SelectedMounts: idsMount(preview.SelectedMounts), SelectedJobs: idsJobsFromConfigs(preview.SelectedJobs), JobEvery: jobEvery(preview.SelectedJobs)})
	if err != nil {
		return State{}, err
	}
	if fresh.CandidateDigest != preview.CandidateDigest {
		return State{}, errors.New("migration preview is stale")
	}
	tx := fmt.Sprintf("mig-%d-%s", time.Now().UnixMilli(), preview.CandidateDigest[:10])
	mcur, err := mounts.LoadRegistry(p)
	if err != nil {
		return State{}, err
	}
	jcur, err := jobs.Load(p)
	if err != nil {
		return State{}, err
	}
	backup, err := writeBackups(p, tx, mcur, jcur)
	if err != nil {
		return State{}, err
	}
	s := State{SchemaVersion: SchemaVersion, Generation: preview.Generation + 1, Phase: PhasePrepared, TransactionID: tx, CandidateDigest: preview.CandidateDigest, ProviderConfigSHA256: preview.Detection.ProviderConfigSHA256, BackupDir: backup, SelectedMounts: namesMount(preview.SelectedMounts), SelectedJobs: namesJob(preview.SelectedJobs), ProviderDisableRequired: preview.Detection.ProviderEnabled}
	if err := saveState(p, s); err != nil {
		return State{}, err
	}
	fail := func(e error) (State, error) {
		s.LastError = e.Error()
		s.Phase = PhaseRolledBack
		s.Recovery = "automatic rollback after failed migration apply"
		_ = rollbackFromBackup(context.Background(), p, &s)
		_ = saveState(p, s)
		return s, e
	}
	if !preview.Detection.ProviderPresent {
		return fail(errors.New("provider disappeared before migration"))
	}
	s.Phase = PhaseQuiescing
	_ = saveState(p, s)
	stopped, err := quiesceProvider(ctx, preview.Detection)
	if err != nil {
		return fail(err)
	}
	s.ProviderQuiesced = true
	s.ProviderProcessesStopped = stopped
	afterProviderQuiesce()
	after, err := Detect(p)
	if err != nil {
		return fail(err)
	}
	if !after.ProviderPresent {
		return fail(errors.New("provider disappeared mid-migration"))
	}
	if len(after.Processes) != 0 {
		return fail(errors.New("provider lifecycle remains active after quiesce"))
	}
	id, digest, binary, err := activeRuntime(p)
	if err != nil {
		return fail(err)
	}
	if err := validateConfig(ctx, binary, preview.Detection.ProviderConfig); err != nil {
		return fail(err)
	}
	s.Phase = PhaseApplying
	_ = saveState(p, s)
	cfgHash, err := copyExact(preview.Detection.ProviderConfig, p.ManagedRcloneConfig)
	if err != nil {
		return fail(err)
	}
	if cfgHash != preview.Detection.ProviderConfigSHA256 {
		return fail(errors.New("provider config changed during migration"))
	}
	if err := validateConfig(ctx, binary, p.ManagedRcloneConfig); err != nil {
		return fail(err)
	}
	mrep, err := applyMounts(ctx, p, mcur, mountCandidateList(mcur, preview.SelectedMounts, false))
	if err != nil {
		return fail(err)
	}
	if len(mrep.LifecycleFailures) > 0 {
		return fail(fmt.Errorf("unexpected lifecycle failure while importing disabled mounts: %v", mrep.LifecycleFailures))
	}
	jrep, err := applyJobs(p, jcur, jobCandidateList(jcur, preview.SelectedJobs, false))
	if err != nil {
		return fail(err)
	}
	s.ActiveRuntimeID = id
	s.ActiveRuntimeSHA256 = digest
	s.ConfigSHA256 = cfgHash
	s.ImportedMounts = namesMount(preview.SelectedMounts)
	s.ImportedJobs = namesJob(preview.SelectedJobs)
	_ = jrep
	s.Phase = PhaseAwaitingProviderDisable
	s.LastError = ""
	s.Recovery = "provider lifecycle quiesced; disable/remove legacy module explicitly, then finalize migration"
	if err := saveState(p, s); err != nil {
		return fail(err)
	}
	return s, nil
}

func idsMount(ms []MountCandidate) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}
func namesMount(ms []MountCandidate) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}
func namesJob(js []jobs.Config) []string {
	out := make([]string, len(js))
	for i, j := range js {
		out[i] = j.Name
	}
	return out
}
func idsJobsFromConfigs(js []jobs.Config) []string {
	out := make([]string, len(js))
	for i, j := range js {
		if strings.HasPrefix(j.Name, "migrated-sync-") {
			n := strings.TrimPrefix(j.Name, "migrated-sync-")
			out[i] = "sync:" + strings.TrimLeft(n, "0")
		} else if strings.HasPrefix(j.Name, "migrated-copy-") {
			n := strings.TrimPrefix(j.Name, "migrated-copy-")
			out[i] = "copy:" + strings.TrimLeft(n, "0")
		} else {
			out[i] = j.Name
		}
	}
	return out
}
func jobEvery(js []jobs.Config) string {
	if len(js) > 0 {
		return js[0].Every
	}
	return ""
}

func readMountBackup(dir string) ([]mounts.CandidateConfig, error) {
	b, err := os.ReadFile(filepath.Join(dir, "mounts-before.json"))
	if err != nil {
		return nil, err
	}
	var v struct {
		Configs []mounts.CandidateConfig `json:"configs"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v.Configs, nil
}
func readJobsBackup(dir string) (jobs.Registry, error) {
	b, err := os.ReadFile(filepath.Join(dir, "jobs-before.json"))
	if err != nil {
		return jobs.Registry{}, err
	}
	var v jobs.Registry
	if err = json.Unmarshal(b, &v); err != nil {
		return jobs.Registry{}, err
	}
	return v, nil
}
func restoreConfig(p paths.Paths, dir string) error {
	src := filepath.Join(dir, "config-before.bin")
	if b, err := os.ReadFile(src); err == nil {
		return writeAtomic(p.Normalize().ManagedRcloneConfig, b, 0o600)
	}
	if _, err := os.Stat(filepath.Join(dir, "config-before.absent")); err == nil {
		e := os.Remove(p.Normalize().ManagedRcloneConfig)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	return errors.New("migration config backup is missing")
}
func rollbackFromBackup(ctx context.Context, p paths.Paths, s *State) error {
	if s.BackupDir == "" {
		return nil
	}
	var errs []string
	if err := restoreConfig(p, s.BackupDir); err != nil {
		errs = append(errs, "config: "+err.Error())
	}
	if c, err := readMountBackup(s.BackupDir); err == nil {
		cur, e := mounts.LoadRegistry(p)
		if e == nil {
			if rep, e := applyMounts(ctx, p, cur, c); e != nil {
				errs = append(errs, "mounts: "+e.Error())
			} else if len(rep.LifecycleFailures) > 0 {
				errs = append(errs, "mount lifecycle rollback incomplete")
			}
		} else {
			errs = append(errs, "mounts: "+e.Error())
		}
	} else {
		errs = append(errs, "mounts backup: "+err.Error())
	}
	if orig, err := readJobsBackup(s.BackupDir); err == nil {
		cur, e := jobs.Load(p)
		if e == nil {
			if _, e := applyJobs(p, cur, orig.Jobs); e != nil {
				errs = append(errs, "jobs: "+e.Error())
			}
		} else {
			errs = append(errs, "jobs: "+e.Error())
		}
	} else {
		errs = append(errs, "jobs backup: "+err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func Finalize(ctx context.Context, p paths.Paths) (State, error) {
	var out State
	err := withMigrationLock(p, func() error {
		var err error
		out, err = finalizeUnlocked(ctx, p)
		return err
	})
	return out, err
}

func finalizeUnlocked(ctx context.Context, p paths.Paths) (State, error) {
	p = p.Normalize()
	s, ok, err := loadState(p)
	if err != nil {
		return State{}, err
	}
	if !ok {
		return State{}, errors.New("no migration state")
	}
	if s.Phase != PhaseAwaitingProviderDisable {
		return s, fmt.Errorf("migration cannot finalize from phase %s", s.Phase)
	}
	d, err := Detect(p)
	if err != nil {
		return s, err
	}
	if d.ProviderEnabled || len(d.Processes) > 0 {
		return s, errors.New("legacy provider lifecycle is still active; disable/remove it explicitly before finalizing")
	}
	s.Phase = PhaseFinalizing
	s.LastError = ""
	_ = saveState(p, s)
	fail := func(e error) (State, error) {
		s.LastError = e.Error()
		s.Phase = PhaseRolledBack
		s.Recovery = "authority switch failed; restored pre-migration Nexus state"
		_ = rollbackFromBackup(context.Background(), p, &s)
		_ = saveState(p, s)
		return s, e
	}
	id, digest, binary, err := activeRuntime(p)
	if err != nil {
		return fail(err)
	}
	if err := validateConfig(ctx, binary, p.ManagedRcloneConfig); err != nil {
		return fail(err)
	}
	curMounts, err := mounts.LoadRegistry(p)
	if err != nil {
		return fail(err)
	}
	mset := map[string]bool{}
	for _, n := range s.ImportedMounts {
		mset[n] = true
	}
	mc := make([]mounts.CandidateConfig, 0, len(curMounts.Mounts))
	for _, c := range curMounts.Mounts {
		x := candidateFromConfig(c)
		if mset[x.Name] {
			x.Enabled = true
		}
		mc = append(mc, x)
	}
	mrep, err := applyMounts(ctx, p, curMounts, mc)
	if err != nil {
		return fail(err)
	}
	if len(mrep.LifecycleFailures) > 0 {
		return fail(fmt.Errorf("selected Nexus mount failed during authority switch: %s", mrep.LifecycleFailures[0].Error))
	}
	curJobs, err := jobs.Load(p)
	if err != nil {
		return fail(err)
	}
	jset := map[string]bool{}
	for _, n := range s.ImportedJobs {
		jset[n] = true
	}
	jc := append([]jobs.Config{}, curJobs.Jobs...)
	for i := range jc {
		if jset[jc[i].Name] {
			jc[i].Enabled = true
		}
	}
	if _, err := applyJobs(p, curJobs, jc); err != nil {
		return fail(err)
	}
	cfgHash, err := hashFile(p.ManagedRcloneConfig)
	if err != nil {
		return fail(err)
	}
	if cfgHash != s.ConfigSHA256 {
		return fail(errors.New("managed config changed before authority switch"))
	}
	ev := Evidence{SchemaVersion: SchemaVersion, TransactionID: s.TransactionID, ProviderConfigSHA256: s.ProviderConfigSHA256, ManagedConfigSHA256: cfgHash, ActiveRuntimeID: id, ActiveRuntimeSHA256: digest, ImportedMounts: append([]string{}, s.ImportedMounts...), ImportedJobs: append([]string{}, s.ImportedJobs...), ProviderProcessesStopped: s.ProviderProcessesStopped, ProviderPresentAfter: d.ProviderPresent, ProviderEnabledAfter: d.ProviderEnabled, CompletedUnixMS: nowMS()}
	eb, _ := json.MarshalIndent(ev, "", "  ")
	ep := filepath.Join(p.MigrationEvidenceDir, s.TransactionID+".json")
	if err := writeAtomic(ep, append(eb, '\n'), 0o600); err != nil {
		return fail(err)
	}
	s.Phase = PhaseCompleted
	s.ActiveRuntimeID = id
	s.ActiveRuntimeSHA256 = digest
	s.EvidencePath = ep
	s.Recovery = "standalone authority active; legacy provider is no longer required and was not uninstalled by Nexus"
	s.LastError = ""
	if err := saveState(p, s); err != nil {
		return fail(err)
	}
	return s, nil
}

func Recover(ctx context.Context, p paths.Paths) (State, bool, error) {
	var out State
	var ok bool
	err := withMigrationLock(p, func() error {
		var err error
		out, ok, err = recoverUnlocked(ctx, p)
		return err
	})
	return out, ok, err
}

func recoverUnlocked(ctx context.Context, p paths.Paths) (State, bool, error) {
	p = p.Normalize()
	s, ok, err := loadState(p)
	if err != nil || !ok {
		return s, ok, err
	}
	switch s.Phase {
	case PhasePrepared, PhaseQuiescing, PhaseApplying, PhaseFinalizing:
		e := rollbackFromBackup(ctx, p, &s)
		s.Phase = PhaseRolledBack
		s.Recovery = "recovered interrupted migration by restoring pre-migration Nexus state"
		if e != nil {
			s.LastError = e.Error()
		}
		_ = saveState(p, s)
		if e != nil {
			return s, true, e
		}
		return s, true, nil
	case PhaseAwaitingProviderDisable:
		return s, true, errors.New("migration is awaiting explicit provider disable/remove and finalization")
	case PhaseCompleted:
		d, e := Detect(p)
		if e != nil {
			return s, true, e
		}
		if d.ProviderEnabled || len(d.Processes) > 0 {
			s.Phase = PhaseConflict
			s.LastError = "legacy provider lifecycle became active after standalone migration"
			s.Recovery = "disable the competing provider lifecycle; Nexus remains fail-closed"
			_ = saveState(p, s)
			return s, true, errors.New(s.LastError)
		}
		return s, true, nil
	case PhaseConflict:
		return s, true, errors.New("migration authority conflict: " + s.LastError)
	default:
		return s, true, nil
	}
}

func Rollback(ctx context.Context, p paths.Paths) (State, error) {
	var out State
	err := withMigrationLock(p, func() error {
		var err error
		out, err = rollbackUnlocked(ctx, p)
		return err
	})
	return out, err
}

func rollbackUnlocked(ctx context.Context, p paths.Paths) (State, error) {
	s, ok, err := loadState(p)
	if err != nil {
		return State{}, err
	}
	if !ok {
		return State{}, errors.New("no migration state")
	}
	if s.Phase == PhaseCompleted {
		return s, errors.New("completed standalone migration cannot be silently reverted to provider authority")
	}
	if err := rollbackFromBackup(ctx, p, &s); err != nil {
		return s, err
	}
	s.Phase = PhaseRolledBack
	s.Recovery = "manual rollback restored pre-migration Nexus state"
	s.LastError = ""
	if err := saveState(p, s); err != nil {
		return s, err
	}
	return s, nil
}
