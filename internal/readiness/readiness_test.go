package readiness

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func readyPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	providerDir := filepath.Join(root, "provider")
	if err := os.MkdirAll(filepath.Join(providerDir, "conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	rclone := filepath.Join(providerDir, "rclone")
	if err := os.WriteFile(rclone, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'rclone vtest'; exit 0; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, "conf", "rclone.conf"), []byte("[x]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{StateDir: filepath.Join(root, "state"), ModuleDir: filepath.Join(root, "module"), ProviderModuleDir: providerDir, RcloneConfig: filepath.Join(providerDir, "conf", "rclone.conf"), FuseDevice: "/dev/null"}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBootAndNetworkLateReasonsAreExplicit(t *testing.T) {
	p := readyPaths(t)
	t.Setenv("RNEXUS_BOOT_COMPLETED", "0")
	t.Setenv("RNEXUS_NETWORK_STATE", "offline")
	snap := Check(context.Background(), p, Spec{Name: "x", Remote: "x:", Mountpoint: filepath.Join(filepath.Dir(p.StateDir), "mnt", "x"), BootRequired: true, RequireNetwork: true})
	if snap.Ready || snap.WaitingReason != "android_boot_incomplete" {
		t.Fatalf("unexpected readiness: %+v", snap)
	}
	t.Setenv("RNEXUS_BOOT_COMPLETED", "1")
	snap = Check(context.Background(), p, Spec{Name: "x", Remote: "x:", Mountpoint: filepath.Join(filepath.Dir(p.StateDir), "mnt", "x"), BootRequired: true, RequireNetwork: true})
	if snap.Ready || snap.WaitingReason != "network_unavailable" {
		t.Fatalf("unexpected network readiness: %+v", snap)
	}
}

func TestRemoteProbeClassifiesAuthAndOffline(t *testing.T) {
	p := readyPaths(t)
	t.Setenv("RNEXUS_BOOT_COMPLETED", "1")
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	t.Setenv("RNEXUS_REMOTE_PROBE_RESULT", "auth")
	mountpoint := filepath.Join(filepath.Dir(p.StateDir), "mnt", "x")
	snap := Check(context.Background(), p, Spec{Name: "x", Remote: "x:", Mountpoint: mountpoint, ProbeRemote: true})
	if snap.RemoteState != "auth_error" || snap.WaitingReason != "remote_auth_error" {
		t.Fatalf("unexpected auth classification: %+v", snap)
	}
	t.Setenv("RNEXUS_REMOTE_PROBE_RESULT", "offline")
	snap = Check(context.Background(), p, Spec{Name: "x", Remote: "x:", Mountpoint: mountpoint, ProbeRemote: true})
	if snap.RemoteState != "offline" || snap.WaitingReason != "remote_offline" {
		t.Fatalf("unexpected offline classification: %+v", snap)
	}
}
