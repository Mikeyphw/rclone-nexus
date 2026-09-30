package readiness

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/redact"
)

type Spec struct {
	Name           string
	Remote         string
	Mountpoint     string
	BootRequired   bool
	RequireNetwork bool
	ProbeRemote    bool
}

type Condition struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason,omitempty"`
}

type Snapshot struct {
	Ready         bool        `json:"ready"`
	WaitingReason string      `json:"waiting_reason,omitempty"`
	NetworkClass  string      `json:"network_class,omitempty"`
	RemoteState   string      `json:"remote_state,omitempty"`
	Conditions    []Condition `json:"conditions"`
}

type remoteProbeResult struct {
	State string
	Ready bool
}

func Check(ctx context.Context, p paths.Paths, spec Spec) Snapshot {
	p = p.Normalize()
	result := Snapshot{Ready: true, NetworkClass: "unknown", RemoteState: "not_requested"}
	add := func(name string, required, ready bool, reason string) {
		result.Conditions = append(result.Conditions, Condition{Name: name, Required: required, Ready: ready, Reason: reason})
		if required && !ready && result.WaitingReason == "" {
			result.Ready = false
			result.WaitingReason = reason
		}
	}

	if err := p.EnsureState(); err != nil {
		add("persistent_state", true, false, "persistent_state_unavailable")
	} else {
		add("persistent_state", true, true, "")
	}

	if spec.BootRequired {
		ready := bootCompleted(ctx)
		add("android_boot", true, ready, chooseReason(ready, "android_boot_incomplete"))
	} else {
		add("android_boot", false, true, "not_required")
	}

	providerStatus := provider.Discover(p)
	add("provider_module", true, providerStatus.ModuleReady, chooseReason(providerStatus.ModuleReady, "provider_module_missing"))
	add("provider_binary", true, providerStatus.BinaryReady, chooseReason(providerStatus.BinaryReady, "rclone_binary_missing"))
	add("fuse_device", true, providerStatus.FuseDeviceReady, chooseReason(providerStatus.FuseDeviceReady, "fuse_device_unavailable"))
	add("provider_config", true, providerStatus.ConfigReady, chooseReason(providerStatus.ConfigReady, "rclone_config_missing"))

	storageReady := targetStorageReady(spec.Mountpoint)
	add("target_storage", true, storageReady, chooseReason(storageReady, "target_storage_unavailable"))

	networkRequired := spec.RequireNetwork || spec.ProbeRemote
	networkReady, networkClass := networkStatus()
	result.NetworkClass = networkClass
	add("network", networkRequired, !networkRequired || networkReady, chooseReason(!networkRequired || networkReady, "network_unavailable"))

	if spec.ProbeRemote && networkReady {
		probe := probeRemote(ctx, p, spec.Remote)
		result.RemoteState = probe.State
		add("remote_probe", true, probe.Ready, probeReason(probe.State))
	} else if spec.ProbeRemote {
		result.RemoteState = "offline"
		add("remote_probe", true, false, "remote_offline")
	} else {
		add("remote_probe", false, true, "not_requested")
	}
	return result
}

func chooseReason(ready bool, reason string) string {
	if ready {
		return ""
	}
	return reason
}

func bootCompleted(ctx context.Context) bool {
	if value := os.Getenv("RNEXUS_BOOT_COMPLETED"); value != "" {
		return truthy(value)
	}
	binary := os.Getenv("RNEXUS_GETPROP_BIN")
	if binary == "" {
		if candidate, err := exec.LookPath("getprop"); err == nil {
			binary = candidate
		}
	}
	if binary == "" {
		return true
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, binary, "sys.boot_completed").Output()
	return err == nil && strings.TrimSpace(string(output)) == "1"
}

func targetStorageReady(mountpoint string) bool {
	if value := os.Getenv("RNEXUS_STORAGE_READY"); value != "" {
		return truthy(value)
	}
	clean := filepath.Clean(mountpoint)
	parts := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
	if len(parts) >= 3 && parts[0] == "storage" && parts[1] == "emulated" {
		_, err := os.Stat(filepath.Join(string(filepath.Separator), parts[0], parts[1], parts[2]))
		return err == nil
	}
	if len(parts) >= 5 && parts[0] == "mnt" && parts[1] == "runtime" && parts[3] == "emulated" {
		_, err := os.Stat(filepath.Join(string(filepath.Separator), parts[0], parts[1], parts[2], parts[3], parts[4]))
		return err == nil
	}
	parent := filepath.Dir(clean)
	for {
		info, err := os.Stat(parent)
		if err == nil {
			return info.IsDir()
		}
		next := filepath.Dir(parent)
		if next == parent {
			return false
		}
		parent = next
	}
}

func networkStatus() (bool, string) {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("RNEXUS_NETWORK_STATE"))); value != "" {
		if value == "offline" || value == "none" || value == "down" {
			return false, "offline"
		}
		return true, value
	}
	routePath := os.Getenv("RNEXUS_ROUTE_PATH")
	if routePath == "" {
		routePath = "/proc/net/route"
	}
	file, err := os.Open(routePath)
	if err != nil {
		return false, "offline"
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, _ := strconv.ParseUint(fields[3], 16, 64)
		if flags&0x1 == 0 {
			continue
		}
		return true, classifyInterface(fields[0])
	}
	return false, "offline"
}

func classifyInterface(iface string) string {
	lower := strings.ToLower(iface)
	switch {
	case strings.HasPrefix(lower, "wlan") || strings.HasPrefix(lower, "wifi"):
		return "wifi"
	case strings.HasPrefix(lower, "rmnet") || strings.HasPrefix(lower, "ccmni") || strings.HasPrefix(lower, "pdp"):
		return "cellular"
	case strings.HasPrefix(lower, "tun") || strings.HasPrefix(lower, "wg") || strings.HasPrefix(lower, "ppp"):
		return "vpn"
	case strings.HasPrefix(lower, "eth"):
		return "ethernet"
	default:
		return "other"
	}
}

func probeRemote(ctx context.Context, p paths.Paths, remote string) remoteProbeResult {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("RNEXUS_REMOTE_PROBE_RESULT"))); value != "" {
		switch value {
		case "ok", "ready", "online":
			return remoteProbeResult{State: "online", Ready: true}
		case "auth", "auth_error":
			return remoteProbeResult{State: "auth_error"}
		case "offline", "network":
			return remoteProbeResult{State: "offline"}
		default:
			return remoteProbeResult{State: "error"}
		}
	}
	binary, err := provider.FindRclone(p)
	if err != nil {
		return remoteProbeResult{State: "error"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, remoteProbeTimeout())
	defer cancel()
	cmd := exec.CommandContext(probeCtx, binary, "lsf", remote, "--max-depth", "1", "--config", p.RcloneConfig, "--contimeout", "3s", "--timeout", "5s")
	cmd.Stdout = io.Discard
	stderr := &limitedBuffer{remaining: 16 << 10}
	cmd.Stderr = stderr
	if err := cmd.Run(); err == nil {
		return remoteProbeResult{State: "online", Ready: true}
	}
	if errorsIsDeadline(probeCtx.Err()) {
		return remoteProbeResult{State: "offline"}
	}
	text := strings.ToLower(stderr.String())
	for _, marker := range []string{"unauthorized", "invalid_grant", "authentication", "token expired", "401", "403", "access denied"} {
		if strings.Contains(text, marker) {
			return remoteProbeResult{State: "auth_error"}
		}
	}
	for _, marker := range []string{"network is unreachable", "no route to host", "connection refused", "timeout", "timed out", "temporary failure", "name resolution"} {
		if strings.Contains(text, marker) {
			return remoteProbeResult{State: "offline"}
		}
	}
	return remoteProbeResult{State: "error"}
}

func remoteProbeTimeout() time.Duration {
	if value := os.Getenv("RNEXUS_REMOTE_PROBE_TIMEOUT_MS"); value != "" {
		if ms, err := strconv.Atoi(value); err == nil && ms >= 100 && ms <= 30000 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 8 * time.Second
}

func probeReason(state string) string {
	switch state {
	case "online":
		return ""
	case "auth_error":
		return "remote_auth_error"
	case "offline":
		return "remote_offline"
	default:
		return "remote_probe_failed"
	}
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func errorsIsDeadline(err error) bool { return err == context.DeadlineExceeded }

type limitedBuffer struct {
	buf       bytes.Buffer
	remaining int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if b.remaining > 0 {
		chunk := p
		if len(chunk) > b.remaining {
			chunk = chunk[:b.remaining]
		}
		_, _ = b.buf.Write(chunk)
		b.remaining -= len(chunk)
	}
	return original, nil
}
func (b *limitedBuffer) String() string { return redact.BoundedString(b.buf.String(), 16<<10) }

func DebugString(s Snapshot) string {
	return fmt.Sprintf("ready=%t waiting=%s network=%s remote=%s", s.Ready, s.WaitingReason, s.NetworkClass, s.RemoteState)
}
