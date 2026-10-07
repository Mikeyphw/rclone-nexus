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
	t.Setenv("RNEXUS_RUNTIME_MODE", "managed")
	root := t.TempDir()
	managedBin := filepath.Join(root, "module", "system", "bin", "rclone")
	managedConfig := filepath.Join(root, "state", "config", "rclone", "rclone.conf")
	if err := os.MkdirAll(filepath.Dir(managedBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedBin, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'rclone vtest'; exit 0; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(managedConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedConfig, []byte("[x]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{
		StateDir:            filepath.Join(root, "state"),
		ModuleDir:           filepath.Join(root, "module"),
		ProviderModuleDir:   filepath.Join(root, "provider-removed"),
		ManagedRcloneConfig: managedConfig,
		FuseDevice:          "/dev/null",
	}.Normalize()
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

func TestOfflineAllowedProbeDoesNotBlockColdStartWithoutNetwork(t *testing.T) {
	p := readyPaths(t)
	t.Setenv("RNEXUS_BOOT_COMPLETED", "1")
	t.Setenv("RNEXUS_NETWORK_STATE", "offline")
	mountpoint := filepath.Join(filepath.Dir(p.StateDir), "mnt", "x")
	snap := Check(context.Background(), p, Spec{Name: "x", Remote: "x:", Mountpoint: mountpoint, RequireNetwork: false, ProbeRemote: true})
	if !snap.Ready || snap.WaitingReason != "" || snap.RemoteState != "offline" {
		t.Fatalf("offline-allowed probe should be advisory while device is offline: %+v", snap)
	}
}

func TestNetworkStatusRecognizesIPv6DefaultRoute(t *testing.T) {
	dir := t.TempDir()
	v4 := filepath.Join(dir, "route")
	v6 := filepath.Join(dir, "ipv6_route")
	if err := os.WriteFile(v4, []byte("Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	line := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 00000000 00000000 00000000 00000001 wlan0\n"
	if err := os.WriteFile(v6, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "")
	t.Setenv("RNEXUS_ROUTE_PATH", v4)
	t.Setenv("RNEXUS_IPV6_ROUTE_PATH", v6)
	t.Setenv("RNEXUS_NET_CLASS_PATH", filepath.Join(dir, "missing"))
	ready, class := networkStatus()
	if !ready || class != "wifi" {
		t.Fatalf("ready=%v class=%q", ready, class)
	}
}

func TestNetworkStatusFallsBackToActiveVPNInterface(t *testing.T) {
	dir := t.TempDir()
	v4 := filepath.Join(dir, "route")
	v6 := filepath.Join(dir, "ipv6_route")
	netRoot := filepath.Join(dir, "net")
	if err := os.WriteFile(v4, []byte("Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v6, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(netRoot, "tun0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netRoot, "tun0", "operstate"), []byte("up\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "")
	t.Setenv("RNEXUS_ROUTE_PATH", v4)
	t.Setenv("RNEXUS_IPV6_ROUTE_PATH", v6)
	t.Setenv("RNEXUS_NET_CLASS_PATH", netRoot)
	ready, class := networkStatus()
	if !ready || class != "vpn" {
		t.Fatalf("ready=%v class=%q", ready, class)
	}
}
