package mounts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyMigrationPreviewApplyPreservesPrivateArgsFile(t *testing.T) {
	p := lifecycleTestPaths(t)
	mountpoint := filepath.Join(t.TempDir(), "drive")
	legacy := "enabled=true\nremote=fake:\nmountpoint=" + mountpoint + "\nvfs_cache_mode=full\nallow_other=false\nargs_file=private/secret.args\n"
	if err := os.WriteFile(filepath.Join(p.MountsDir, "drive.conf"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := PublicSnapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source != "legacy-v0.1" || snapshot.Revision != 0 || len(snapshot.Mounts) != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if !snapshot.Mounts[0].HasArgsFile {
		t.Fatal("args_file presence was not retained")
	}
	publicJSON, _ := json.Marshal(snapshot)
	if strings.Contains(string(publicJSON), "private/secret.args") {
		t.Fatalf("private args_file path leaked: %s", publicJSON)
	}

	candidate := []CandidateConfig{snapshot.Mounts[0].CandidateConfig}
	preview, err := PreviewCandidate(p, candidate)
	if err != nil {
		t.Fatal(err)
	}
	report, err := ApplyCandidate(context.Background(), p, preview.CurrentRevision, preview.CandidateDigest, candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied || report.Registry.Revision != 1 || report.Registry.Source != "registry-v2" {
		t.Fatalf("report=%+v", report)
	}
	cfg, err := Parse(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ArgsFile != "private/secret.args" {
		t.Fatalf("args_file not preserved: %q", cfg.ArgsFile)
	}
	if _, err := os.Stat(p.ConfigPrevious); err != nil {
		t.Fatalf("previous-known-good not written: %v", err)
	}
}

func TestCandidateValidationRejectsOverlapsAndBadVFSValues(t *testing.T) {
	p := lifecycleTestPaths(t)
	base := filepath.Join(t.TempDir(), "mounts")
	_, err := PreviewCandidate(p, []CandidateConfig{
		{Name: "one", Enabled: true, Remote: "fake:", Mountpoint: filepath.Join(base, "one"), VFSCacheMode: "full", LogLevel: "INFO"},
		{Name: "two", Enabled: true, Remote: "fake:", Mountpoint: filepath.Join(base, "one", "nested"), VFSCacheMode: "full", LogLevel: "INFO"},
	})
	if err == nil || !strings.Contains(err.Error(), "mountpoints overlap") {
		t.Fatalf("expected overlap error, got %v", err)
	}
	_, err = PreviewCandidate(p, []CandidateConfig{{Name: "one", Enabled: true, Remote: "fake:", Mountpoint: filepath.Join(base, "one"), VFSCacheMode: "banana", LogLevel: "INFO"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported vfs_cache_mode") {
		t.Fatalf("expected vfs error, got %v", err)
	}
}

func TestApplyRejectsStaleRevisionAndDigestMismatch(t *testing.T) {
	p := lifecycleTestPaths(t)
	candidate := []CandidateConfig{{Name: "drive", Enabled: false, Remote: "fake:", Mountpoint: filepath.Join(t.TempDir(), "drive"), VFSCacheMode: "full", LogLevel: "INFO"}}
	preview, err := PreviewCandidate(p, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyCandidate(context.Background(), p, 0, "wrong", candidate, nil); !IsCandidateDigestMismatch(err) {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
	if _, err := ApplyCandidate(context.Background(), p, 0, preview.CandidateDigest, candidate, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyCandidate(context.Background(), p, 0, preview.CandidateDigest, candidate, nil); !IsStaleRevision(err) {
		t.Fatalf("expected stale revision, got %v", err)
	}
}

func TestAtomicRegistryFailureKeepsPublishedFileIntact(t *testing.T) {
	p := lifecycleTestPaths(t)
	first := Registry{SchemaVersion: RegistrySchemaVersion, Revision: 1, Mounts: []Config{{Name: "one", Enabled: false, Remote: "fake:", Mountpoint: filepath.Join(t.TempDir(), "one"), VFSCacheMode: "full", LogLevel: "INFO"}}}
	first.Digest, _ = digestConfigs(first.Mounts)
	if err := writeRegistryAtomic(p.ConfigRegistry, first); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p.ConfigRegistry)
	if err != nil {
		t.Fatal(err)
	}

	second := Registry{SchemaVersion: RegistrySchemaVersion, Revision: 2, Mounts: []Config{{Name: "two", Enabled: false, Remote: "fake:", Mountpoint: filepath.Join(t.TempDir(), "two"), VFSCacheMode: "full", LogLevel: "INFO"}}}
	second.Digest, _ = digestConfigs(second.Mounts)
	injected := errors.New("injected-before-rename")
	if err := writeRegistryAtomicWithHook(p.ConfigRegistry, second, func() error { return injected }); !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}
	after, err := os.ReadFile(p.ConfigRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("published registry changed before atomic rename")
	}
	matches, _ := filepath.Glob(filepath.Join(p.ConfigDir, ".registry-*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("temporary files leaked: %v", matches)
	}
}

func TestApplyRestartsOnlyAffectedRunningMount(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "a", "fake:", filepath.Join(t.TempDir(), "a"), true)
	writeLegacyMount(t, p, "b", "fake:", filepath.Join(t.TempDir(), "b"), true)
	if _, err := Start(context.Background(), p, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), p, "b"); err != nil {
		t.Fatal(err)
	}
	defer cleanupMount(t, p, "a")
	defer cleanupMount(t, p, "b")
	beforeA := StatusOne(p, "a").PID
	beforeB := StatusOne(p, "b").PID

	snapshot, err := PublicSnapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	candidate := make([]CandidateConfig, 0, len(snapshot.Mounts))
	for _, item := range snapshot.Mounts {
		cfg := item.CandidateConfig
		if cfg.Name == "a" {
			cfg.PollInterval = "15s"
		}
		candidate = append(candidate, cfg)
	}
	preview, err := PreviewCandidate(p, candidate)
	if err != nil {
		t.Fatal(err)
	}
	report, err := ApplyCandidate(context.Background(), p, preview.CurrentRevision, preview.CandidateDigest, candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.LifecycleFailures) != 0 {
		t.Fatalf("failures=%+v", report.LifecycleFailures)
	}
	if len(report.LifecycleActions) != 1 || report.LifecycleActions[0].Name != "a" {
		t.Fatalf("actions=%+v", report.LifecycleActions)
	}
	afterA := StatusOne(p, "a").PID
	afterB := StatusOne(p, "b").PID
	if afterA == beforeA {
		t.Fatalf("affected mount was not restarted: %d", beforeA)
	}
	if afterB != beforeB {
		t.Fatalf("unaffected mount restarted: before=%d after=%d", beforeB, afterB)
	}
}

func TestRollbackUsesPreviousKnownGoodAtNewRevision(t *testing.T) {
	p := lifecycleTestPaths(t)
	first := []CandidateConfig{{Name: "drive", Enabled: false, Remote: "fake:", Mountpoint: filepath.Join(t.TempDir(), "drive"), VFSCacheMode: "full", LogLevel: "INFO"}}
	preview, _ := PreviewCandidate(p, first)
	report, err := ApplyCandidate(context.Background(), p, preview.CurrentRevision, preview.CandidateDigest, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second[0].PollInterval = "10s"
	preview2, _ := PreviewCandidate(p, second)
	report2, err := ApplyCandidate(context.Background(), p, preview2.CurrentRevision, preview2.CandidateDigest, second, nil)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := LoadPreviousRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	if previous.Digest != report.Registry.Digest {
		t.Fatalf("previous digest=%s first=%s", previous.Digest, report.Registry.Digest)
	}
	rolled, err := RollbackPrevious(context.Background(), p, report2.Registry.Revision, previous.Digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Registry.Revision != report2.Registry.Revision+1 {
		t.Fatalf("rollback revision=%d", rolled.Registry.Revision)
	}
	cfg, _ := Parse(p, "drive")
	if cfg.PollInterval != "" {
		t.Fatalf("rollback did not restore prior config: %+v", cfg)
	}
}

func TestLegacyMigrationCorpus(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
		check   func(t *testing.T, cfg Config)
	}{
		{
			name: "minimal-defaults",
			body: "enabled=true\nremote=fake:\nmountpoint={MOUNT}\n",
			check: func(t *testing.T, cfg Config) {
				if cfg.VFSCacheMode != "full" || !cfg.AllowOther || cfg.LogLevel != "INFO" {
					t.Fatalf("defaults not normalized: %+v", cfg)
				}
			},
		},
		{
			name: "advanced-fields",
			body: "enabled=false\nremote=fake:path\nmountpoint={MOUNT}\nvfs_cache_mode=writes\nvfs_cache_max_size=8GiB\nvfs_cache_max_age=24h\ndir_cache_time=1h\npoll_interval=15s\nallow_other=false\nread_only=true\nlog_level=debug\nargs_file=extra.args\n",
			check: func(t *testing.T, cfg Config) {
				if cfg.VFSCacheMode != "writes" || cfg.VFSCacheMaxSize != "8GiB" || cfg.LogLevel != "DEBUG" || !cfg.ReadOnly || cfg.ArgsFile != "extra.args" {
					t.Fatalf("advanced fields not normalized: %+v", cfg)
				}
			},
		},
		{
			name:    "duplicate-key",
			body:    "enabled=true\nremote=fake:\nremote=other:\nmountpoint={MOUNT}\n",
			wantErr: "duplicate config key",
		},
		{
			name:    "unknown-key",
			body:    "enabled=true\nremote=fake:\nmountpoint={MOUNT}\nshell_command=id\n",
			wantErr: "unsupported config key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := lifecycleTestPaths(t)
			mountpoint := filepath.Join(t.TempDir(), "mnt")
			body := strings.ReplaceAll(tc.body, "{MOUNT}", mountpoint)
			if err := os.WriteFile(filepath.Join(p.MountsDir, "drive.conf"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			registry, err := LoadRegistry(p)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if registry.Source != "legacy-v0.1" || len(registry.Mounts) != 1 {
				t.Fatalf("registry=%+v", registry)
			}
			if tc.check != nil {
				tc.check(t, registry.Mounts[0])
			}
		})
	}
}

func TestPreviewExplainsLifecycleNamespacePolicyAndCacheConsequences(t *testing.T) {
	p := lifecycleTestPaths(t)
	writeLegacyMount(t, p, "drive", "fake:", filepath.Join(t.TempDir(), "drive"), true)
	snapshot, err := PublicSnapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	candidate := []CandidateConfig{snapshot.Mounts[0].CandidateConfig}
	candidate[0].Mountpoint = filepath.Join(t.TempDir(), "new-drive")
	candidate[0].NetworkMode = "wifi"
	candidate[0].VFSProfile = "balanced"
	preview, err := PreviewCandidate(p, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Changes) != 1 {
		t.Fatalf("changes=%+v", preview.Changes)
	}
	change := preview.Changes[0]
	if !change.Restart {
		t.Fatalf("restart not reported: %+v", change)
	}
	joined := strings.Join(change.Consequences, ",")
	for _, want := range []string{"lifecycle_restart", "namespace_requalify", "policy_recheck", "cache_policy_recheck"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %+v", want, change.Consequences)
		}
	}
}

func TestValidateCandidateAggregatesFieldAddressableIssues(t *testing.T) {
	p := lifecycleTestPaths(t)
	candidate := []CandidateConfig{
		{Name: "bad name", Enabled: true, Remote: "", Mountpoint: "relative", VFSCacheMode: "full", VFSCacheMaxSize: "2GBB", LogLevel: "INFO", NetworkMode: "any", VFSProfile: "custom", CacheLowWater: 95, CacheHighWater: 90},
	}
	report, err := ValidateCandidate(p, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || len(report.Issues) < 5 {
		t.Fatalf("expected aggregated validation issues, got %+v", report)
	}
	fields := map[string]bool{}
	for _, issue := range report.Issues {
		fields[issue.Field] = true
		if issue.Category == "" || issue.Code == "" || issue.Message == "" || issue.Severity == "" || issue.Mount != "bad name" {
			t.Fatalf("issue is not structured/attributed: %+v", issue)
		}
	}
	for _, field := range []string{"name", "remote", "mountpoint", "vfs_cache_max_size", "cache_low_water"} {
		if !fields[field] {
			t.Fatalf("missing issue for %s: %+v", field, report.Issues)
		}
	}
}

func TestValidateCandidateReportsMountpointConflictWithRelatedMount(t *testing.T) {
	p := lifecycleTestPaths(t)
	base := filepath.Join(t.TempDir(), "Rclone")
	candidate := []CandidateConfig{
		{Name: "one", Remote: "fake:a", Mountpoint: base, VFSCacheMode: "full", LogLevel: "INFO", NetworkMode: "any", VFSProfile: "custom", CacheLowWater: 75, CacheHighWater: 90},
		{Name: "two", Remote: "fake:b", Mountpoint: filepath.Join(base, "nested"), VFSCacheMode: "full", LogLevel: "INFO", NetworkMode: "any", VFSProfile: "custom", CacheLowWater: 75, CacheHighWater: 90},
	}
	report, err := ValidateCandidate(p, candidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range report.Issues {
		if issue.Code == "mountpoint_overlap" && issue.Field == "mountpoint" && issue.Mount == "one" && issue.RelatedMount == "two" {
			return
		}
	}
	t.Fatalf("structured overlap issue missing: %+v", report.Issues)
}
