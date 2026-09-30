package namespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rclone-nexus/internal/paths"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeProc(t *testing.T, root string, pid, uid int, ns, command, mountinfo string) {
	t.Helper()
	base := filepath.Join(root, fmt.Sprint(pid))
	if err := os.MkdirAll(filepath.Join(base, "ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ns, filepath.Join(base, "ns", "mnt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(base, "status"), fmt.Sprintf("Name:\ttest\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid))
	writeFile(t, filepath.Join(base, "cmdline"), command+"\x00")
	writeFile(t, filepath.Join(base, "mountinfo"), mountinfo)
}

const baseMount = "21 1 0:1 / / rw,relatime shared:1 - rootfs rootfs rw\n"

func mountLine(id int, mountpoint, fs string) string {
	return fmt.Sprintf("%d 21 0:45 / %s rw,nosuid,nodev - %s rclone rw\n", id, mountpoint, fs)
}

func TestParseMountInfoAndStorageCapabilities(t *testing.T) {
	text := "36 25 0:32 / /storage/emulated/0 rw,nosuid,nodev shared:7 - fuse sdcard rw,user_id=0\n" +
		"37 25 0:33 / /mnt/runtime/write/emulated/0 rw master:7 - fuse sdcard rw\n" +
		"38 25 0:34 / /mnt/pass_through/0/emulated/0 rw - fuse sdcard rw\n" +
		"39 25 0:35 / /mnt/user/0/primary rw - fuse sdcard rw\n" +
		"40 25 0:36 / /storage/My\\040Drive rw - fuse.rclone rclone rw\n"
	infos, err := ParseMountInfo(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 5 || infos[4].Mountpoint != "/storage/My Drive" {
		t.Fatalf("infos=%+v", infos)
	}
	caps := storageCapabilities(infos)
	got := strings.Join(caps, ",")
	for _, want := range []string{"pass-through-emulated", "runtime-emulated", "shared-propagation", "slave-propagation", "storage-emulated", "user-primary"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func FuzzParseMountInfo(f *testing.F) {
	f.Add("36 25 0:32 / /storage/emulated/0 rw shared:7 - fuse sdcard rw\n")
	f.Add("broken\n")
	f.Fuzz(func(t *testing.T, value string) {
		_, _ = ParseMountInfo(strings.NewReader(value))
	})
}

func TestDiscoverMultiUserAndObservedVisibility(t *testing.T) {
	proc := t.TempDir()
	mountpoint := "/storage/emulated/0/Rclone/Drive"
	serviceMI := baseMount + mountLine(50, mountpoint, "fuse.rclone")
	writeProc(t, proc, 100, 0, "mnt:[1]", "racd", serviceMI)
	writeProc(t, proc, 200, 0, "mnt:[2]", "zygote64", baseMount)
	writeProc(t, proc, 300, 2000, "mnt:[3]", "/system/bin/sh", baseMount)
	writeProc(t, proc, 400, 10123, "mnt:[4]", "com.termux", baseMount+mountLine(51, mountpoint, "fuse.rclone"))
	writeProc(t, proc, 500, 110123, "mnt:[5]", "com.example.secondary", baseMount)
	t.Setenv("RNEXUS_PROC_ROOT", proc)
	t.Setenv("RNEXUS_SERVICE_PID", "100")
	t.Setenv("RNEXUS_MOUNTINFO_PATH", filepath.Join(proc, "100", "mountinfo"))

	p := paths.Paths{StateDir: t.TempDir()}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p.MountsDir, "drive.conf"), "enabled=true\nremote=fake:\nmountpoint="+mountpoint+"\n")
	got, err := Inspect(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ServiceVisible {
		t.Fatal("service mount should be visible")
	}
	if got.Claim != "partial_app_visibility" {
		t.Fatalf("claim=%s", got.Claim)
	}
	if len(got.Users) != 2 || !got.Users[0].Primary || got.Users[1].UserID != 1 {
		t.Fatalf("users=%+v", got.Users)
	}
	if got.Users[0].VisibleNamespaces != 1 || got.Users[1].VisibleNamespaces != 0 {
		t.Fatalf("users=%+v", got.Users)
	}
	if !contains(got.AchievedClasses, "termux") {
		t.Fatalf("achieved=%v", got.AchievedClasses)
	}
}

type fakeMutator struct {
	bindCount  int
	failBindAt int
	unmountErr error
	unmounted  []int
	onBind     func(int)
}

func (f *fakeMutator) Supported() (bool, string) { return true, "" }
func (f *fakeMutator) Bind(_, target int, _ string, _ string) (MountSignature, error) {
	f.bindCount++
	if f.onBind != nil {
		f.onBind(f.bindCount)
	}
	if f.failBindAt == f.bindCount {
		return MountSignature{}, errors.New("injected bind failure")
	}
	return MountSignature{MountID: 77, MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}, nil
}
func (f *fakeMutator) Unmount(target int, _ string, _ string, _ MountSignature) error {
	f.unmounted = append(f.unmounted, target)
	return f.unmountErr
}

func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	p := paths.Paths{StateDir: t.TempDir()}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApplyTransactionRollsBackPartialBindsAndState(t *testing.T) {
	p := testPaths(t)
	writeFile(t, filepath.Join(p.MountsDir, "drive.conf"), "enabled=true\nremote=fake:\nmountpoint=/storage/emulated/0/Rclone/Drive\n")
	sig := MountSignature{MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}
	evidence := Inspection{SchemaVersion: 1, Name: "drive", Mountpoint: "/storage/emulated/0/Rclone/Drive", ServiceVisible: true, SourceOwned: true, SourceSignature: &sig, Topology: Topology{SchemaVersion: 1, ServicePID: 100, ServiceNamespace: "mnt:[1]", Namespaces: []NamespaceRef{{ID: "mnt:[1]", RepresentativePID: 100, Classes: []string{"service"}, Accessible: true}, {ID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, UserIDs: []int{0}, Accessible: true}, {ID: "mnt:[3]", RepresentativePID: 300, Classes: []string{"app"}, UserIDs: []int{1}, Accessible: true}}}}
	plan := Plan{SchemaVersion: 1, Name: "drive", Mountpoint: "/storage/emulated/0/Rclone/Drive", Desired: DesiredAppVisible, Adapter: "same-path-bind-v1", Qualified: true, Evidence: evidence, Actions: []Action{{NamespaceID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Mutation: "bind-same-path"}, {NamespaceID: "mnt:[3]", RepresentativePID: 300, Classes: []string{"app"}, Mutation: "bind-same-path"}}}
	m := &fakeMutator{failBindAt: 2}
	_, err := applyQualifiedPlan(context.Background(), p, plan, m, true)
	if err == nil || !strings.Contains(err.Error(), "injected bind failure") {
		t.Fatalf("err=%v", err)
	}
	if len(m.unmounted) != 1 || m.unmounted[0] != 200 {
		t.Fatalf("rollback=%v", m.unmounted)
	}
	if _, err := LoadState(p, "drive"); !os.IsNotExist(err) {
		t.Fatalf("failed first apply must restore absent state, err=%v", err)
	}
}

func TestRollbackOwnershipMismatchRetainsMarker(t *testing.T) {
	proc := t.TempDir()
	mountpoint := "/mnt/drive"
	writeProc(t, proc, 100, 0, "mnt:[1]", "racd", baseMount+mountLine(50, mountpoint, "fuse.rclone"))
	writeProc(t, proc, 200, 10123, "mnt:[2]", "com.example", baseMount+mountLine(51, mountpoint, "fuse.rclone"))
	t.Setenv("RNEXUS_PROC_ROOT", proc)
	t.Setenv("RNEXUS_SERVICE_PID", "100")
	t.Setenv("RNEXUS_MOUNTINFO_PATH", filepath.Join(proc, "100", "mountinfo"))
	p := testPaths(t)
	writeFile(t, filepath.Join(p.MountsDir, "drive.conf"), "enabled=true\nremote=fake:\nmountpoint="+mountpoint+"\n")
	state := State{SchemaVersion: 1, Name: "drive", Desired: DesiredAppVisible, Mountpoint: mountpoint, SourceSignature: MountSignature{MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}, Targets: []OwnedTarget{{NamespaceID: "mnt:[2]", Classes: []string{"app"}, Signature: MountSignature{MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}}}}
	if err := writeState(p, state); err != nil {
		t.Fatal(err)
	}
	m := &fakeMutator{unmountErr: errors.New("namespace ownership mismatch")}
	_, err := rollbackWithMutator(context.Background(), p, "drive", m, true)
	if err == nil {
		t.Fatal("expected ownership mismatch")
	}
	got, err := LoadState(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 1 {
		t.Fatalf("state=%+v", got)
	}
}

func TestPlanEvidenceDoesNotEquateUnreadableTargetsWithVisibility(t *testing.T) {
	evidence := Inspection{ServiceVisible: true, Visibility: []NamespaceVisibility{{NamespaceID: "mnt:[2]", Classes: []string{"app"}, Accessible: false, ErrorCode: "mountinfo_unreadable"}}, Users: []AndroidUser{{UserID: 0, Primary: true, AppNamespaces: 1, AccessibleNamespaces: 0, VisibleNamespaces: 0}}}
	if visibilityClaim(evidence) != "service_only" {
		t.Fatalf("claim=%s", visibilityClaim(evidence))
	}
}

func TestReconcileDropsStaleMarkerAndRebindsMissingOwnedTarget(t *testing.T) {
	proc := t.TempDir()
	mountpoint := "/storage/emulated/0/Rclone/Drive"
	writeProc(t, proc, 100, 0, "mnt:[1]", "racd", baseMount+mountLine(50, mountpoint, "fuse.rclone"))
	writeProc(t, proc, 200, 10123, "mnt:[2]", "com.example", baseMount)
	t.Setenv("RNEXUS_PROC_ROOT", proc)
	t.Setenv("RNEXUS_SERVICE_PID", "100")
	t.Setenv("RNEXUS_MOUNTINFO_PATH", filepath.Join(proc, "100", "mountinfo"))
	p := testPaths(t)
	writeFile(t, filepath.Join(p.MountsDir, "drive.conf"), "enabled=true\nremote=fake:\nmountpoint="+mountpoint+"\n")
	old := MountSignature{MountID: 41, MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}
	if err := writeState(p, State{SchemaVersion: 1, Name: "drive", Desired: DesiredAppVisible, Mountpoint: mountpoint, SourceSignature: old, Targets: []OwnedTarget{{NamespaceID: "mnt:[2]", Classes: []string{"app"}, Signature: old}}}); err != nil {
		t.Fatal(err)
	}
	source := MountSignature{MountID: 50, MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}
	plan := Plan{SchemaVersion: 1, Name: "drive", Mountpoint: mountpoint, Desired: DesiredAppVisible, Adapter: "same-path-bind-v1", Qualified: true, Evidence: Inspection{SchemaVersion: 1, Name: "drive", Mountpoint: mountpoint, ServiceVisible: true, SourceOwned: true, SourceSignature: &source, Topology: Topology{SchemaVersion: 1, ServicePID: 100, ServiceNamespace: "mnt:[1]", Namespaces: []NamespaceRef{{ID: "mnt:[1]", RepresentativePID: 100, Classes: []string{"service"}, Accessible: true}, {ID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Accessible: true}}}, Visibility: []NamespaceVisibility{{NamespaceID: "mnt:[1]", RepresentativePID: 100, Classes: []string{"service"}, Accessible: true, Visible: true}, {NamespaceID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Accessible: true, Visible: false}}}, Actions: []Action{{NamespaceID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Mutation: "bind-same-path"}}}
	m := &fakeMutator{}
	if _, err := applyQualifiedPlan(context.Background(), p, plan, m, false); err != nil {
		t.Fatal(err)
	}
	if m.bindCount != 1 {
		t.Fatalf("bindCount=%d", m.bindCount)
	}
	state, err := LoadState(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Targets) != 1 || state.Targets[0].Signature.MountID != 77 {
		t.Fatalf("state=%+v", state)
	}
}

func TestReconcileDropsOwnershipWhenVisibleMountIdentityChanged(t *testing.T) {
	proc := t.TempDir()
	mountpoint := "/storage/emulated/0/Rclone/Drive"
	writeProc(t, proc, 100, 0, "mnt:[1]", "racd", baseMount+mountLine(50, mountpoint, "fuse.rclone"))
	writeProc(t, proc, 200, 10123, "mnt:[2]", "com.example", baseMount+mountLine(99, mountpoint, "fuse.rclone"))
	t.Setenv("RNEXUS_PROC_ROOT", proc)
	t.Setenv("RNEXUS_SERVICE_PID", "100")
	t.Setenv("RNEXUS_MOUNTINFO_PATH", filepath.Join(proc, "100", "mountinfo"))
	p := testPaths(t)
	writeFile(t, filepath.Join(p.MountsDir, "drive.conf"), "enabled=true\nremote=fake:\nmountpoint="+mountpoint+"\n")
	old := MountSignature{MountID: 51, MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}
	source := MountSignature{MountID: 50, MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}
	changed := MountSignature{MountID: 99, MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}
	if err := writeState(p, State{SchemaVersion: 1, Name: "drive", Desired: DesiredAppVisible, Mountpoint: mountpoint, SourceSignature: source, Targets: []OwnedTarget{{NamespaceID: "mnt:[2]", Classes: []string{"app"}, Signature: old}}}); err != nil {
		t.Fatal(err)
	}
	plan := Plan{SchemaVersion: 1, Name: "drive", Mountpoint: mountpoint, Desired: DesiredAppVisible, Adapter: "same-path-bind-v1", Qualified: true, Evidence: Inspection{SchemaVersion: 1, Name: "drive", Mountpoint: mountpoint, ServiceVisible: true, SourceOwned: true, SourceSignature: &source, Topology: Topology{SchemaVersion: 1, ServicePID: 100, ServiceNamespace: "mnt:[1]", Namespaces: []NamespaceRef{{ID: "mnt:[1]", RepresentativePID: 100, Classes: []string{"service"}, Accessible: true}, {ID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Accessible: true}}}, Visibility: []NamespaceVisibility{{NamespaceID: "mnt:[1]", RepresentativePID: 100, Classes: []string{"service"}, Accessible: true, Visible: true}, {NamespaceID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Accessible: true, Visible: true, Signature: &changed}}}, Actions: []Action{{NamespaceID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Mutation: "none", AlreadyVisible: true}}}
	m := &fakeMutator{}
	if _, err := applyQualifiedPlan(context.Background(), p, plan, m, false); err != nil {
		t.Fatal(err)
	}
	if m.bindCount != 0 || len(m.unmounted) != 0 {
		t.Fatalf("unexpected mutation bind=%d unmount=%v", m.bindCount, m.unmounted)
	}
	state, err := LoadState(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Targets) != 0 {
		t.Fatalf("changed external mount retained ownership: %+v", state)
	}
}

func TestApplyCancellationRollsBackCreatedBindAndRestoresState(t *testing.T) {
	p := testPaths(t)
	writeFile(t, filepath.Join(p.MountsDir, "drive.conf"), "enabled=true\nremote=fake:\nmountpoint=/storage/emulated/0/Rclone/Drive\n")
	sig := MountSignature{MountID: 50, MajorMinor: "0:45", Root: "/", FSType: "fuse.rclone"}
	previous := State{SchemaVersion: 1, Name: "drive", Desired: DesiredAppVisible, Mountpoint: "/storage/emulated/0/Rclone/Drive", SourceSignature: sig, LastClaim: "service_only"}
	if err := writeState(p, previous); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &fakeMutator{onBind: func(count int) {
		if count == 1 {
			cancel()
		}
	}}
	plan := Plan{
		SchemaVersion: 1, Name: "drive", Mountpoint: previous.Mountpoint, Desired: DesiredAppVisible, Adapter: "same-path-bind-v1", Qualified: true,
		Evidence: Inspection{SchemaVersion: 1, Name: "drive", Mountpoint: previous.Mountpoint, ServiceVisible: true, SourceOwned: true, SourceSignature: &sig, Topology: Topology{SchemaVersion: 1, ServicePID: 100, ServiceNamespace: "mnt:[1]", Namespaces: []NamespaceRef{{ID: "mnt:[1]", RepresentativePID: 100, Classes: []string{"service"}, Accessible: true}, {ID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Accessible: true}, {ID: "mnt:[3]", RepresentativePID: 300, Classes: []string{"app"}, Accessible: true}}}},
		Actions:  []Action{{NamespaceID: "mnt:[2]", RepresentativePID: 200, Classes: []string{"app"}, Mutation: "bind-same-path"}, {NamespaceID: "mnt:[3]", RepresentativePID: 300, Classes: []string{"app"}, Mutation: "bind-same-path"}},
	}
	_, err := applyQualifiedPlan(ctx, p, plan, m, true)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err=%v", err)
	}
	if len(m.unmounted) != 1 || m.unmounted[0] != 200 {
		t.Fatalf("rollback=%v", m.unmounted)
	}
	got, err := LoadState(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastClaim != previous.LastClaim || len(got.Targets) != 0 || got.SourceSignature != previous.SourceSignature {
		t.Fatalf("state not restored: %+v", got)
	}
}

func TestPreviewDoesNotQualifyShellOnlyVisibility(t *testing.T) {
	plan := Plan{Actions: []Action{{NamespaceID: "mnt:[2]", Classes: []string{"shell"}, Mutation: "bind-same-path"}}}
	qualifyPlan(&plan)
	if plan.Qualified || plan.Reason != "no_app_namespace_targets" {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestPreviewFailsClosedWhenTopologyIsTruncated(t *testing.T) {
	plan := Plan{Evidence: Inspection{Topology: Topology{Truncated: true}}, Actions: []Action{{NamespaceID: "mnt:[2]", Classes: []string{"app"}, Mutation: "bind-same-path"}}}
	qualifyPlan(&plan)
	if plan.Qualified || plan.Reason != "topology_truncated" {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestAndroidAppMountpointRejectsStorageRoots(t *testing.T) {
	for _, path := range []string{"/storage", "/mnt/runtime", "/mnt/user", "/mnt/pass_through", "/data/local/tmp"} {
		if androidAppMountpoint(path) {
			t.Fatalf("unexpected supported root: %s", path)
		}
	}
	for _, path := range []string{"/storage/emulated/0/Rclone", "/mnt/runtime/write/emulated/0/Rclone", "/mnt/user/0/primary/Rclone"} {
		if !androidAppMountpoint(path) {
			t.Fatalf("expected supported path: %s", path)
		}
	}
}
