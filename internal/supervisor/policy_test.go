package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rclone-nexus/internal/mounts"
)

func setLegacyPolicy(t *testing.T, pPath string, networkMode string) {
	t.Helper()
	data, err := os.ReadFile(pPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "network_mode=") {
		return
	}
	text += "network_mode=" + networkMode + "\n"
	if err := os.WriteFile(pPath, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyBlocksStartThenResumesWithoutRestartBudget(t *testing.T) {
	p, _ := supervisorPaths(t, false)
	conf := filepath.Join(p.MountsDir, "drive.conf")
	setLegacyPolicy(t, conf, "wifi")
	t.Setenv("RNEXUS_NETWORK_STATE", "cellular")
	report, err := ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Health) != 1 || report.Health[0].Policy.Allowed || report.Health[0].Reason != "policy_blocked" {
		t.Fatalf("expected policy block: %+v", report)
	}
	if report.Health[0].Attempts != 0 {
		t.Fatalf("policy block consumed restart budget: %+v", report.Health[0])
	}
	obs, _ := mounts.ObserveRuntime(p, "drive")
	if obs.ProcessAlive {
		t.Fatalf("mount started while policy blocked: %+v", obs)
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	report, err = ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stopIfRunning(t, p)
	if !report.Health[0].Policy.Allowed || report.Health[0].State != Running {
		t.Fatalf("policy resume did not start mount: %+v", report.Health[0])
	}
}

func TestRunningMountRetainedWhenPolicyBecomesBlocked(t *testing.T) {
	p, _ := supervisorPaths(t, false)
	conf := filepath.Join(p.MountsDir, "drive.conf")
	setLegacyPolicy(t, conf, "wifi")
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	if _, err := ReconcileOnce(context.Background(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	defer stopIfRunning(t, p)
	before, _ := mounts.ObserveRuntime(p, "drive")
	if !before.ProcessAlive {
		t.Fatal("expected running mount")
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "cellular")
	report, err := ReconcileOnce(context.Background(), p, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := mounts.ObserveRuntime(p, "drive")
	if !after.ProcessAlive || after.PID != before.PID {
		t.Fatalf("policy transition destroyed valid mount: before=%+v after=%+v", before, after)
	}
	if report.Health[0].Policy.Allowed || report.Health[0].Policy.Reason != "wifi_required" {
		t.Fatalf("blocked policy not reported: %+v", report.Health[0].Policy)
	}
}
