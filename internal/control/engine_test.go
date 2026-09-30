package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
	"rclone-nexus/internal/rc"
)

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	base := t.TempDir()
	provider := filepath.Join(base, "provider")
	if err := os.MkdirAll(filepath.Join(provider, "conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provider, "conf", "rclone.conf"), []byte("[fake]\ntype=local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{
		ModuleDir: filepath.Join(base, "module"), ProviderModuleDir: provider,
		StateDir: filepath.Join(base, "state"), MountsDir: filepath.Join(base, "state", "mounts.d"),
		RunDir: filepath.Join(base, "state", "run"), LogDir: filepath.Join(base, "state", "logs"), CacheDir: filepath.Join(base, "state", "cache"),
		RcloneConfig: filepath.Join(provider, "conf", "rclone.conf"), FuseDevice: "/dev/null",
	}
	p.Socket = filepath.Join(p.RunDir, "racd.sock")
	p.DaemonLock = filepath.Join(p.RunDir, "racd.lock")
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRegistryIsTypedAndContainsNoArbitraryExec(t *testing.T) {
	engine := New(testPaths(t))
	caps := engine.Capabilities()
	classes := map[string]bool{}
	for _, class := range caps.Classes {
		classes[class] = true
	}
	for _, expected := range []string{protocol.ClassQuery, protocol.ClassPreview, protocol.ClassRun, protocol.ClassCancel, protocol.ClassReconcile} {
		if !classes[expected] {
			t.Fatalf("missing operation class %s", expected)
		}
	}
	for _, op := range caps.Operations {
		name := strings.ToLower(op.Name)
		if strings.Contains(name, "exec") || strings.Contains(name, "shell") || strings.Contains(name, "argv") {
			t.Fatalf("unsafe operation exposed: %+v", op)
		}
	}
}

func TestProviderStatusDoesNotExposePrivatePaths(t *testing.T) {
	p := testPaths(t)
	engine := New(p)
	request := protocol.NewRequest("provider-1", "provider.status", protocol.ClassQuery, struct{}{})
	response := engine.Execute(context.Background(), request, nil)
	if !response.OK {
		t.Fatalf("response failed: %+v", response.Error)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, private := range []string{p.ProviderModuleDir, p.RcloneConfig, p.StateDir} {
		if strings.Contains(text, private) {
			t.Fatalf("private path leaked: %s", private)
		}
	}
}

func TestUnknownOperationFailsWithStableCode(t *testing.T) {
	engine := New(testPaths(t))
	request := protocol.NewRequest("bad-op", "system.exec", protocol.ClassRun, map[string]any{"argv": []string{"id"}})
	response := engine.Execute(context.Background(), request, nil)
	if response.OK || response.Error == nil || response.Error.Code != "unknown_operation" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestOperationClassMismatchFails(t *testing.T) {
	engine := New(testPaths(t))
	request := protocol.NewRequest("bad-class", "provider.status", protocol.ClassRun, struct{}{})
	response := engine.Execute(context.Background(), request, nil)
	if response.OK || response.Error == nil || response.Error.Code != "operation_class_mismatch" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestConfigMutationSurfaceRejectsArgsFile(t *testing.T) {
	p := testPaths(t)
	engine := New(p)
	mountpoint := filepath.Join(t.TempDir(), "drive")
	raw := json.RawMessage(`{"mounts":[{"name":"drive","enabled":true,"remote":"fake:","mountpoint":"` + mountpoint + `","vfs_cache_mode":"full","allow_other":false,"log_level":"INFO","args_file":"/data/local/tmp/unsafe.args"}]}`)
	request := protocol.NewRequest("config-args-file", "config.preview", protocol.ClassPreview, struct{}{})
	request.Operation.Args = raw
	response := engine.Execute(context.Background(), request, nil)
	if response.OK || response.Error == nil || response.Error.Code != "invalid_argument" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestCapabilitiesExposeSafeCancellationOnly(t *testing.T) {
	engine := New(testPaths(t))
	caps := engine.Capabilities()
	seen := map[string]bool{}
	for _, op := range caps.Operations {
		seen[op.Name] = op.Cancellable
	}
	for _, name := range []string{"mount.start", "mount.stop", "mount.restart", "mount.reconcile"} {
		if !seen[name] {
			t.Fatalf("expected %s to be cancellable", name)
		}
	}
	for _, name := range []string{"config.apply", "config.rollback"} {
		if seen[name] {
			t.Fatalf("unsafe cancellation exposed for %s", name)
		}
	}
}

func TestRCMetricsTypedResponseNeverContainsCredentials(t *testing.T) {
	p := testPaths(t)
	rec, err := rc.Prepare(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", rec.Address)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/core/stats", func(w http.ResponseWriter, r *http.Request) {
		u, pw, ok := r.BasicAuth()
		if !ok || u != rec.Username || pw != rec.Password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"bytes":42,"speed":2}`))
	})
	mux.HandleFunc("/vfs/stats", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"openFiles":1}`)) })
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	engine := New(p)
	request := protocol.NewRequest("rc-safe", "rc.metrics", protocol.ClassQuery, map[string]any{"name": "drive"})
	response := engine.Execute(context.Background(), request, nil)
	if !response.OK {
		t.Fatalf("metrics response failed: %+v", response.Error)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if strings.Contains(text, rec.Username) || strings.Contains(text, rec.Password) {
		t.Fatalf("RC credential crossed typed API: %s", text)
	}
}

func TestConfigApplyRequiresFreshPreviewProof(t *testing.T) {
	p := testPaths(t)
	engine := New(p)
	mountpoint := filepath.Join(t.TempDir(), "drive")
	candidate := []map[string]any{{"name": "drive", "enabled": false, "remote": "fake:", "mountpoint": mountpoint, "vfs_cache_mode": "full", "allow_other": false, "log_level": "INFO"}}
	previewReq := protocol.NewRequest("preview-proof", "config.preview", protocol.ClassPreview, map[string]any{"mounts": candidate})
	previewResp := engine.Execute(context.Background(), previewReq, nil)
	if !previewResp.OK {
		t.Fatalf("preview failed: %+v", previewResp.Error)
	}
	payload, _ := json.Marshal(previewResp.Result)
	var preview struct {
		CurrentRevision uint64 `json:"current_revision"`
		CandidateDigest string `json:"candidate_digest"`
		PreviewProof    string `json:"preview_proof"`
	}
	if err := json.Unmarshal(payload, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.PreviewProof == "" {
		t.Fatal("preview token missing")
	}

	without := protocol.NewRequest("apply-no-proof", "config.apply", protocol.ClassRun, map[string]any{"expected_revision": preview.CurrentRevision, "candidate_digest": preview.CandidateDigest, "mounts": candidate})
	withoutResp := engine.Execute(context.Background(), without, nil)
	if withoutResp.OK || withoutResp.Error == nil || withoutResp.Error.Code != "preview_required" {
		t.Fatalf("unexpected missing-proof response: %+v", withoutResp)
	}

	with := protocol.NewRequest("apply-with-proof", "config.apply", protocol.ClassRun, map[string]any{"expected_revision": preview.CurrentRevision, "candidate_digest": preview.CandidateDigest, "preview_proof": preview.PreviewProof, "mounts": candidate})
	withResp := engine.Execute(context.Background(), with, nil)
	if !withResp.OK {
		t.Fatalf("apply failed: %+v", withResp.Error)
	}

	replay := protocol.NewRequest("apply-replay", "config.apply", protocol.ClassRun, map[string]any{"expected_revision": preview.CurrentRevision, "candidate_digest": preview.CandidateDigest, "preview_proof": preview.PreviewProof, "mounts": candidate})
	replayResp := engine.Execute(context.Background(), replay, nil)
	if replayResp.OK || replayResp.Error == nil || replayResp.Error.Code != "preview_required" {
		t.Fatalf("preview proof replay accepted: %+v", replayResp)
	}
}

func TestConfigRollbackRequiresFreshPreviewProof(t *testing.T) {
	p := testPaths(t)
	engine := New(p)
	mountpoint := filepath.Join(t.TempDir(), "drive")
	applyCandidate := func(requestPrefix, poll string) {
		candidate := []map[string]any{{"name": "drive", "enabled": false, "remote": "fake:", "mountpoint": mountpoint, "vfs_cache_mode": "full", "allow_other": false, "log_level": "INFO", "poll_interval": poll}}
		previewResp := engine.Execute(context.Background(), protocol.NewRequest(requestPrefix+"-preview", "config.preview", protocol.ClassPreview, map[string]any{"mounts": candidate}), nil)
		if !previewResp.OK {
			t.Fatalf("preview failed: %+v", previewResp.Error)
		}
		payload, _ := json.Marshal(previewResp.Result)
		var preview struct {
			CurrentRevision uint64 `json:"current_revision"`
			CandidateDigest string `json:"candidate_digest"`
			PreviewProof    string `json:"preview_proof"`
		}
		if err := json.Unmarshal(payload, &preview); err != nil {
			t.Fatal(err)
		}
		applyResp := engine.Execute(context.Background(), protocol.NewRequest(requestPrefix+"-apply", "config.apply", protocol.ClassRun, map[string]any{"expected_revision": preview.CurrentRevision, "candidate_digest": preview.CandidateDigest, "preview_proof": preview.PreviewProof, "mounts": candidate}), nil)
		if !applyResp.OK {
			t.Fatalf("apply failed: %+v", applyResp.Error)
		}
	}
	applyCandidate("first", "")
	applyCandidate("second", "10s")

	previewResp := engine.Execute(context.Background(), protocol.NewRequest("rollback-preview", "config.rollback.preview", protocol.ClassPreview, map[string]any{}), nil)
	if !previewResp.OK {
		t.Fatalf("rollback preview failed: %+v", previewResp.Error)
	}
	payload, _ := json.Marshal(previewResp.Result)
	var preview struct {
		CurrentRevision uint64 `json:"current_revision"`
		CandidateDigest string `json:"candidate_digest"`
		PreviewProof    string `json:"preview_proof"`
	}
	if err := json.Unmarshal(payload, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.PreviewProof == "" {
		t.Fatal("rollback preview proof missing")
	}
	missing := engine.Execute(context.Background(), protocol.NewRequest("rollback-missing", "config.rollback", protocol.ClassRun, map[string]any{"expected_revision": preview.CurrentRevision, "previous_digest": preview.CandidateDigest}), nil)
	if missing.OK || missing.Error == nil || missing.Error.Code != "preview_required" {
		t.Fatalf("missing rollback proof accepted: %+v", missing)
	}
	applied := engine.Execute(context.Background(), protocol.NewRequest("rollback-apply", "config.rollback", protocol.ClassRun, map[string]any{"expected_revision": preview.CurrentRevision, "previous_digest": preview.CandidateDigest, "preview_proof": preview.PreviewProof}), nil)
	if !applied.OK {
		t.Fatalf("rollback failed: %+v", applied.Error)
	}
	cfg, err := mounts.Parse(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval != "" {
		t.Fatalf("rollback did not restore previous config: %+v", cfg)
	}
}
