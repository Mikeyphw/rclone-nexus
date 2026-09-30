package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
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
