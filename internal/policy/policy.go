package policy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/paths"
)

const (
	NetworkAny            = "any"
	NetworkWiFi           = "wifi"
	NetworkUnmetered      = "unmetered"
	NetworkOfflineAllowed = "offline-allowed"
)

type Spec struct {
	NetworkMode   string
	ChargingOnly  bool
	MinBattery    int
	MinFreeCache  uint64
	BootSettle    time.Duration
	NetworkSettle time.Duration
}

type Observation struct {
	Online          bool   `json:"online"`
	NetworkClass    string `json:"network_class"`
	MeteredKnown    bool   `json:"metered_known"`
	Metered         bool   `json:"metered,omitempty"`
	ChargingKnown   bool   `json:"charging_known"`
	Charging        bool   `json:"charging,omitempty"`
	BatteryKnown    bool   `json:"battery_known"`
	BatteryPercent  int    `json:"battery_percent,omitempty"`
	CacheFreeBytes  uint64 `json:"cache_free_bytes"`
	CacheTotalBytes uint64 `json:"cache_total_bytes"`
	UptimeSeconds   uint64 `json:"uptime_seconds"`
	ObservedUnixMS  int64  `json:"observed_unix_ms"`
}

type Decision struct {
	Allowed            bool        `json:"allowed"`
	State              string      `json:"state"`
	Reason             string      `json:"reason,omitempty"`
	Reasons            []string    `json:"reasons,omitempty"`
	Retryable          bool        `json:"retryable"`
	NetworkStableForMS int64       `json:"network_stable_for_ms,omitempty"`
	Observation        Observation `json:"observation"`
}

type networkState struct {
	SchemaVersion int    `json:"schema_version"`
	Online        bool   `json:"online"`
	Class         string `json:"class"`
	SinceUnixMS   int64  `json:"since_unix_ms"`
}

func ValidNetworkMode(mode string) bool {
	switch mode {
	case NetworkAny, NetworkWiFi, NetworkUnmetered, NetworkOfflineAllowed:
		return true
	}
	return false
}

func Evaluate(ctx context.Context, p paths.Paths, name string, spec Spec, boot bool) (Decision, error) {
	return evaluate(ctx, p, name, spec, boot, false)
}
func EvaluateAndRecord(ctx context.Context, p paths.Paths, name string, spec Spec, boot bool) (Decision, error) {
	return evaluate(ctx, p, name, spec, boot, true)
}

func evaluate(ctx context.Context, p paths.Paths, name string, spec Spec, boot bool, record bool) (Decision, error) {
	p = p.Normalize()
	obs := observe(ctx, p, spec.NetworkMode == NetworkUnmetered)
	stableFor, err := networkStableFor(p, name, obs, record)
	if err != nil {
		return Decision{}, err
	}
	return evaluateObservation(obs, spec, boot, stableFor)
}

// evaluateObservation is the pure policy decision boundary. Observation gathering
// is intentionally separate so fail-closed behavior can be tested without
// consulting the host's live Android/network state.
func evaluateObservation(obs Observation, spec Spec, boot bool, stableFor time.Duration) (Decision, error) {
	decision := Decision{Allowed: true, State: "allowed", Retryable: true, Observation: obs, NetworkStableForMS: stableFor.Milliseconds()}
	block := func(reason string) {
		decision.Allowed = false
		decision.State = "blocked"
		decision.Reasons = append(decision.Reasons, reason)
		if decision.Reason == "" {
			decision.Reason = reason
		}
	}

	mode := spec.NetworkMode
	if mode == "" {
		mode = NetworkOfflineAllowed
	}
	switch mode {
	case NetworkOfflineAllowed:
	case NetworkAny:
		if !obs.Online {
			block("network_offline")
		}
	case NetworkWiFi:
		if !obs.Online {
			block("network_offline")
		} else if obs.NetworkClass != "wifi" {
			block("wifi_required")
		}
	case NetworkUnmetered:
		if !obs.Online {
			block("network_offline")
		} else if obs.MeteredKnown && obs.Metered {
			block("unmetered_required")
		} else if !obs.MeteredKnown {
			block("metering_unknown")
		}
	default:
		return Decision{}, fmt.Errorf("unsupported network policy %q", mode)
	}
	if spec.ChargingOnly {
		if !obs.ChargingKnown {
			block("charging_unknown")
		} else if !obs.Charging {
			block("charging_required")
		}
	}
	if spec.MinBattery > 0 {
		if !obs.BatteryKnown {
			block("battery_unknown")
		} else if obs.BatteryPercent < spec.MinBattery {
			block("battery_below_minimum")
		}
	}
	if spec.MinFreeCache > 0 && obs.CacheFreeBytes < spec.MinFreeCache {
		block("cache_free_space_below_minimum")
	}
	if boot && spec.BootSettle > 0 && time.Duration(obs.UptimeSeconds)*time.Second < spec.BootSettle {
		block("boot_settle")
	}
	if obs.Online && spec.NetworkSettle > 0 && stableFor < spec.NetworkSettle {
		block("network_settle")
	}
	return decision, nil
}

func Observe(ctx context.Context, p paths.Paths) Observation { return observe(ctx, p, true) }

func observe(ctx context.Context, p paths.Paths, needMetered bool) Observation {
	online, class := networkStatus()
	meteredKnown, metered := false, false
	if needMetered && online {
		meteredKnown, metered = meteredStatus(ctx, class)
	}
	chargingKnown, charging, batteryKnown, battery := powerStatus()
	free, total := filesystemSpace(p.Normalize().CacheDir)
	return Observation{Online: online, NetworkClass: class, MeteredKnown: meteredKnown, Metered: metered, ChargingKnown: chargingKnown, Charging: charging, BatteryKnown: batteryKnown, BatteryPercent: battery, CacheFreeBytes: free, CacheTotalBytes: total, UptimeSeconds: uptimeSeconds(), ObservedUnixMS: time.Now().UnixMilli()}
}

func networkStatus() (bool, string) {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("RNEXUS_NETWORK_STATE"))); value != "" {
		if value == "offline" || value == "none" || value == "down" {
			return false, "offline"
		}
		return true, value
	}
	path := os.Getenv("RNEXUS_ROUTE_PATH")
	if path == "" {
		path = "/proc/net/route"
	}
	file, err := os.Open(path)
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
		if flags&1 == 0 {
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

func meteredStatus(ctx context.Context, class string) (bool, bool) {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("RNEXUS_NETWORK_METERED"))); value != "" {
		return true, value == "1" || value == "true" || value == "yes" || value == "metered"
	}
	if class == "ethernet" {
		return true, false
	}
	// Fail closed unless ConnectivityService identifies the active/default
	// network and its own capability block. Looking for NOT_METERED globally
	// could accidentally use a capability from an inactive network.
	binary, err := exec.LookPath("dumpsys")
	if err != nil {
		return false, false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, binary, "connectivity").Output()
	if err != nil {
		return false, false
	}
	text := string(output)
	re := regexp.MustCompile(`(?mi)(?:active\s+default\s+network|default\s+network)\s*:\s*(\d+)`)
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return false, false
	}
	marker := "network{" + match[1] + "}"
	index := strings.Index(text, marker)
	if index < 0 {
		return false, false
	}
	end := index + 4096
	if end > len(text) {
		end = len(text)
	}
	block := text[index:end]
	if next := strings.Index(block[len(marker):], "NetworkAgentInfo{"); next >= 0 {
		block = block[:len(marker)+next]
	}
	if strings.Contains(block, "NOT_METERED") || strings.Contains(block, "NET_CAPABILITY_NOT_METERED") {
		return true, false
	}
	if strings.Contains(block, "Capabilities:") || strings.Contains(block, "NET_CAPABILITY_") {
		return true, true
	}
	return false, false
}

func powerStatus() (bool, bool, bool, int) {
	if charging := strings.TrimSpace(os.Getenv("RNEXUS_CHARGING")); charging != "" {
		knownBattery := false
		battery := 0
		if value := os.Getenv("RNEXUS_BATTERY_PERCENT"); value != "" {
			if n, err := strconv.Atoi(value); err == nil {
				knownBattery, battery = true, n
			}
		}
		return true, truthy(charging), knownBattery, battery
	}
	entries, _ := filepath.Glob("/sys/class/power_supply/*")
	chargingKnown, charging, batteryKnown, battery := false, false, false, 0
	for _, entry := range entries {
		kind := strings.ToLower(readTrim(filepath.Join(entry, "type")))
		switch kind {
		case "battery":
			if capacity, err := strconv.Atoi(readTrim(filepath.Join(entry, "capacity"))); err == nil {
				batteryKnown, battery = true, capacity
			}
			status := strings.ToLower(readTrim(filepath.Join(entry, "status")))
			if status != "" {
				chargingKnown = true
				if status == "charging" || status == "full" {
					charging = true
				}
			}
		case "mains", "usb", "usb_c", "wireless":
			if online := readTrim(filepath.Join(entry, "online")); online != "" {
				chargingKnown = true
				if truthy(online) {
					charging = true
				}
			}
		}
	}
	return chargingKnown, charging, batteryKnown, battery
}

func filesystemSpace(path string) (uint64, uint64) {
	var stat syscall.Statfs_t
	if syscall.Statfs(path, &stat) != nil {
		return 0, 0
	}
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize)
}

func uptimeSeconds() uint64 {
	file, err := os.Open("/proc/uptime")
	if err != nil {
		return 0
	}
	defer file.Close()
	var seconds float64
	_, _ = fmt.Fscan(file, &seconds)
	if seconds < 0 {
		return 0
	}
	return uint64(seconds)
}

func readTrim(path string) string {
	data, _ := os.ReadFile(path)
	return strings.TrimSpace(string(data))
}
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func networkStableFor(p paths.Paths, name string, obs Observation, record bool) (time.Duration, error) {
	if err := p.EnsureState(); err != nil {
		return 0, err
	}
	path := filepath.Join(p.PolicyDir, name+".json")
	now := time.Now().UnixMilli()
	state := networkState{SchemaVersion: 1, Online: obs.Online, Class: obs.NetworkClass, SinceUnixMS: now}
	unchanged := false
	if data, err := os.ReadFile(path); err == nil {
		var previous networkState
		if json.Unmarshal(data, &previous) == nil && previous.SchemaVersion == 1 && previous.Online == obs.Online && previous.Class == obs.NetworkClass && previous.SinceUnixMS > 0 {
			state.SinceUnixMS = previous.SinceUnixMS
			unchanged = true
		}
	}
	if !unchanged && record {
		payload, _ := json.MarshalIndent(state, "", "  ")
		payload = append(payload, '\n')
		if err := writeAtomic(path, payload); err != nil {
			return 0, err
		}
	}
	if !unchanged && !record {
		return 0, nil
	}
	stable := time.Duration(now-state.SinceUnixMS) * time.Millisecond
	if stable < 0 {
		stable = 0
	}
	return stable, nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".policy-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
