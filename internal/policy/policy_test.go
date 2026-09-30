package policy

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rclone-nexus/internal/paths"
)

func policyPaths(t *testing.T) paths.Paths {
	t.Helper()
	root := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(root, "state")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	return p
}
func hasReason(d Decision, reason string) bool {
	for _, item := range d.Reasons {
		if item == reason {
			return true
		}
	}
	return false
}

func TestPolicyTransitionMatrix(t *testing.T) {
	p := policyPaths(t)
	ctx := context.Background()
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	t.Setenv("RNEXUS_NETWORK_METERED", "false")
	t.Setenv("RNEXUS_CHARGING", "true")
	t.Setenv("RNEXUS_BATTERY_PERCENT", "80")
	tests := []struct {
		name    string
		spec    Spec
		allowed bool
		reason  string
	}{
		{"any", Spec{NetworkMode: NetworkAny}, true, ""},
		{"wifi", Spec{NetworkMode: NetworkWiFi}, true, ""},
		{"unmetered", Spec{NetworkMode: NetworkUnmetered}, true, ""},
		{"charging", Spec{NetworkMode: NetworkAny, ChargingOnly: true}, true, ""},
		{"battery", Spec{NetworkMode: NetworkAny, MinBattery: 50}, true, ""},
	}
	for _, tt := range tests {
		d, err := Evaluate(ctx, p, tt.name, tt.spec, false)
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed != tt.allowed || tt.reason != "" && !hasReason(d, tt.reason) {
			t.Fatalf("%s decision=%+v", tt.name, d)
		}
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "cellular")
	d, err := Evaluate(ctx, p, "wifi-block", Spec{NetworkMode: NetworkWiFi}, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || !hasReason(d, "wifi_required") {
		t.Fatalf("wifi policy did not block cellular: %+v", d)
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	t.Setenv("RNEXUS_NETWORK_METERED", "true")
	d, err = Evaluate(ctx, p, "metered", Spec{NetworkMode: NetworkUnmetered}, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || !hasReason(d, "unmetered_required") {
		t.Fatalf("unmetered policy did not block metered network: %+v", d)
	}
	t.Setenv("RNEXUS_CHARGING", "false")
	d, err = Evaluate(ctx, p, "charge-block", Spec{NetworkMode: NetworkOfflineAllowed, ChargingOnly: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || !hasReason(d, "charging_required") {
		t.Fatalf("charging policy did not block: %+v", d)
	}
	t.Setenv("RNEXUS_CHARGING", "true")
	t.Setenv("RNEXUS_BATTERY_PERCENT", "20")
	d, err = Evaluate(ctx, p, "battery-block", Spec{NetworkMode: NetworkOfflineAllowed, MinBattery: 30}, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || !hasReason(d, "battery_below_minimum") {
		t.Fatalf("battery policy did not block: %+v", d)
	}
}

func TestNetworkSettleUsesPersistentTransitionTime(t *testing.T) {
	p := policyPaths(t)
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	d, err := EvaluateAndRecord(context.Background(), p, "drive", Spec{NetworkMode: NetworkWiFi, NetworkSettle: 25 * time.Millisecond}, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || !hasReason(d, "network_settle") {
		t.Fatalf("first observation should settle: %+v", d)
	}
	time.Sleep(35 * time.Millisecond)
	d, err = EvaluateAndRecord(context.Background(), p, "drive", Spec{NetworkMode: NetworkWiFi, NetworkSettle: 25 * time.Millisecond}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed {
		t.Fatalf("stable network remained blocked: %+v", d)
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "cellular")
	d, err = EvaluateAndRecord(context.Background(), p, "drive", Spec{NetworkMode: NetworkAny, NetworkSettle: 25 * time.Millisecond}, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || !hasReason(d, "network_settle") {
		t.Fatalf("network transition did not reset settle window: %+v", d)
	}
}

func TestUnmeteredFailsClosedWhenUnknown(t *testing.T) {
	d, err := evaluateObservation(Observation{
		Online:       true,
		NetworkClass: "wifi",
		MeteredKnown: false,
	}, Spec{NetworkMode: NetworkUnmetered}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || !hasReason(d, "metering_unknown") {
		t.Fatalf("unknown metering must fail closed: %+v", d)
	}

	d, err = evaluateObservation(Observation{
		Online:       true,
		NetworkClass: "wifi",
		MeteredKnown: true,
		Metered:      false,
	}, Spec{NetworkMode: NetworkUnmetered}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed {
		t.Fatalf("known unmetered network must be allowed: %+v", d)
	}
}
