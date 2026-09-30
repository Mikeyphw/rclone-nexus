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

func TestPolicyTransitionsRemainIdempotentWithoutDuplicateProcesses(t *testing.T) {
	p, _ := supervisorPaths(t, false)
	conf := filepath.Join(p.MountsDir, "drive.conf")
	setLegacyPolicy(t, conf, "wifi")
	t.Setenv("RNEXUS_NETWORK_STATE", "cellular")
	if _, err := ReconcileOnce(context.Background(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	if _, err := ReconcileOnce(context.Background(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	defer stopIfRunning(t, p)
	first, err := mounts.ObserveRuntime(p, "drive")
	if err != nil || !first.ProcessAlive || !first.MountAlive {
		t.Fatalf("expected first policy-resumed mount: obs=%+v err=%v", first, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := ReconcileOnce(context.Background(), p, true, nil); err != nil {
			t.Fatal(err)
		}
		current, err := mounts.ObserveRuntime(p, "drive")
		if err != nil {
			t.Fatal(err)
		}
		if !current.ProcessAlive || current.PID != first.PID {
			t.Fatalf("policy reconcile created/replaced process at cycle %d: first=%+v current=%+v", i, first, current)
		}
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "cellular")
	if _, err := ReconcileOnce(context.Background(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_NETWORK_STATE", "wifi")
	if _, err := ReconcileOnce(context.Background(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	final, err := mounts.ObserveRuntime(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if final.PID != first.PID || !final.ProcessAlive || !final.MountAlive {
		t.Fatalf("blocked/resumed policy transition duplicated valid mount: first=%+v final=%+v", first, final)
	}
}
