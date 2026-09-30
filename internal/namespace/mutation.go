package namespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/diagnostics"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
)

type Action struct {
	NamespaceID       string   `json:"namespace_id"`
	RepresentativePID int      `json:"representative_pid"`
	UserIDs           []int    `json:"user_ids,omitempty"`
	Classes           []string `json:"classes"`
	Mutation          string   `json:"mutation"`
	AlreadyVisible    bool     `json:"already_visible"`
}

type Plan struct {
	SchemaVersion int        `json:"schema_version"`
	Name          string     `json:"name"`
	Mountpoint    string     `json:"mountpoint"`
	Desired       string     `json:"desired"`
	Adapter       string     `json:"adapter"`
	Qualified     bool       `json:"qualified"`
	Reason        string     `json:"reason,omitempty"`
	Actions       []Action   `json:"actions"`
	Evidence      Inspection `json:"evidence"`
}

type MutationReport struct {
	SchemaVersion int        `json:"schema_version"`
	Name          string     `json:"name"`
	Desired       string     `json:"desired"`
	Adapter       string     `json:"adapter"`
	Applied       int        `json:"applied"`
	Released      int        `json:"released,omitempty"`
	Evidence      Inspection `json:"evidence"`
}

type Mutator interface {
	Supported() (bool, string)
	Bind(sourcePID, targetPID int, targetNamespaceID, mountpoint string) (MountSignature, error)
	Unmount(targetPID int, targetNamespaceID, mountpoint string, expected MountSignature) error
}

type linuxMutator struct{}

func defaultMutator() Mutator { return linuxMutator{} }
func (linuxMutator) Supported() (bool, string) {
	if os.Geteuid() != 0 {
		return false, "root_required"
	}
	if _, err := os.Stat("/proc/self/ns/mnt"); err != nil {
		return false, "mount_namespace_unavailable"
	}
	if !hasCapSysAdmin() {
		return false, "cap_sys_admin_required"
	}
	return true, ""
}

func hasCapSysAdmin() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return false
		}
		value, err := strconv.ParseUint(fields[1], 16, 64)
		return err == nil && value&(uint64(1)<<21) != 0
	}
	return false
}

func (linuxMutator) Bind(sourcePID, targetPID int, targetNamespaceID, mountpoint string) (MountSignature, error) {
	source, err := os.Open(mountpoint)
	if err != nil {
		return MountSignature{}, fmt.Errorf("open source mount: %w", err)
	}
	defer source.Close()
	var sig MountSignature
	err = withMountNamespace(targetPID, targetNamespaceID, func() error {
		st, err := os.Lstat(mountpoint)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target mountpoint unavailable")
		}
		sourcePath := fmt.Sprintf("/proc/self/fd/%d", source.Fd())
		if err := syscall.Mount(sourcePath, mountpoint, "", uintptr(syscall.MS_BIND), ""); err != nil {
			return fmt.Errorf("bind mount failed: %w", err)
		}
		infos, err := readSelfMountInfo()
		if err != nil {
			return err
		}
		info, ok := findMount(infos, mountpoint)
		if !ok {
			return fmt.Errorf("bind mount not observable")
		}
		sig = info.Signature()
		return nil
	})
	return sig, err
}

func (linuxMutator) Unmount(targetPID int, targetNamespaceID, mountpoint string, expected MountSignature) error {
	return withMountNamespace(targetPID, targetNamespaceID, func() error {
		infos, err := readSelfMountInfo()
		if err != nil {
			return err
		}
		info, ok := findMount(infos, mountpoint)
		if !ok {
			return nil
		}
		if info.Signature() != expected {
			return fmt.Errorf("namespace ownership mismatch")
		}
		if err := syscall.Unmount(mountpoint, syscall.MNT_DETACH); err != nil {
			return fmt.Errorf("owned namespace unmount failed: %w", err)
		}
		return nil
	})
}

func readSelfMountInfo() ([]MountInfo, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseMountInfo(f)
}

func androidAppMountpoint(value string) bool {
	clean := filepath.Clean(value)
	for _, prefix := range []string{"/storage/", "/mnt/runtime/", "/mnt/user/", "/mnt/pass_through/"} {
		root := strings.TrimSuffix(prefix, "/")
		if clean != root && strings.HasPrefix(clean+"/", prefix) {
			return true
		}
	}
	return false
}

func targetClass(classes []string) bool {
	return contains(classes, "zygote") || contains(classes, "shell") || contains(classes, "app") || contains(classes, "termux")
}

func Preview(p paths.Paths, name string) (Plan, error) {
	return previewWithMutator(p, name, defaultMutator())
}
func previewWithMutator(p paths.Paths, name string, m Mutator) (Plan, error) {
	evidence, err := Inspect(p, name)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{SchemaVersion: SchemaVersion, Name: name, Mountpoint: evidence.Mountpoint, Desired: DesiredAppVisible, Adapter: "same-path-bind-v1", Evidence: evidence}
	supported, reason := m.Supported()
	if !evidence.SourceOwned || !evidence.ServiceVisible {
		plan.Reason = "source_mount_not_owned"
		return plan, nil
	}
	if !androidAppMountpoint(plan.Mountpoint) {
		plan.Reason = "unsupported_mountpoint_boundary"
		return plan, nil
	}
	if !supported {
		plan.Reason = reason
		return plan, nil
	}
	for _, entry := range evidence.Visibility {
		if entry.NamespaceID == evidence.Topology.ServiceNamespace || !targetClass(entry.Classes) {
			continue
		}
		action := Action{NamespaceID: entry.NamespaceID, RepresentativePID: entry.RepresentativePID, UserIDs: entry.UserIDs, Classes: entry.Classes, AlreadyVisible: entry.Visible}
		if entry.Visible {
			action.Mutation = "none"
		} else if entry.Accessible {
			action.Mutation = "bind-same-path"
		} else {
			action.Mutation = "unavailable"
		}
		plan.Actions = append(plan.Actions, action)
	}
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].NamespaceID < plan.Actions[j].NamespaceID })
	qualifyPlan(&plan)
	return plan, nil
}

func qualifyPlan(plan *Plan) {
	accessibleTargets := 0
	unavailableTargets := 0
	appTargets := 0
	accessibleAppTargets := 0
	for _, action := range plan.Actions {
		appRelevant := contains(action.Classes, "zygote") || contains(action.Classes, "app")
		if appRelevant {
			appTargets++
		}
		switch action.Mutation {
		case "bind-same-path", "none":
			accessibleTargets++
			if appRelevant {
				accessibleAppTargets++
			}
		case "unavailable":
			unavailableTargets++
		}
	}
	plan.Qualified = accessibleAppTargets > 0
	switch {
	case plan.Evidence.Topology.Truncated:
		plan.Qualified = false
		plan.Reason = "topology_truncated"
	case len(plan.Actions) == 0:
		plan.Reason = "no_target_namespaces_observed"
	case appTargets == 0:
		plan.Reason = "no_app_namespace_targets"
	case accessibleAppTargets == 0:
		plan.Reason = "app_namespaces_unreadable"
	case unavailableTargets > 0:
		plan.Reason = "partial_target_access"
	case accessibleTargets == 0:
		plan.Reason = "target_namespaces_unreadable"
	default:
		plan.Reason = ""
	}
}

func Apply(ctx context.Context, p paths.Paths, name string) (MutationReport, error) {
	return applyWithMutator(ctx, p, name, defaultMutator(), true)
}
func ReconcileDesired(ctx context.Context, p paths.Paths, name string) (MutationReport, error) {
	if !Desired(p, name) {
		evidence, err := Inspect(p, name)
		return MutationReport{SchemaVersion: SchemaVersion, Name: name, Desired: "root", Adapter: "none", Evidence: evidence}, err
	}
	return applyWithMutator(ctx, p, name, defaultMutator(), false)
}

func applyWithMutator(ctx context.Context, p paths.Paths, name string, m Mutator, setDesired bool) (MutationReport, error) {
	p = p.Normalize()
	var report MutationReport
	err := withNamespaceLock(p, name, func() error {
		return mounts.WithLock(p, name, func() error {
			// Preview again while the lifecycle lock is held. A restart between an
			// earlier UI preview and apply must never change the source mount or
			// target namespace set behind the mutation transaction.
			plan, err := previewWithMutator(p, name, m)
			if err != nil {
				return err
			}
			if !plan.Qualified {
				return fmt.Errorf("namespace strategy unsupported: %s", plan.Reason)
			}
			var applyErr error
			report, applyErr = applyQualifiedPlanUnlocked(ctx, p, plan, m, setDesired)
			return applyErr
		})
	})
	if err == nil {
		_ = diagnostics.Append(p, "namespace", name, "applied", "", map[string]any{"desired": report.Desired, "applied": report.Applied, "released": report.Released})
	}
	return report, err
}

// applyQualifiedPlan exists for deterministic transaction/failure-injection
// tests. Production applyWithMutator always refreshes the plan while holding the
// namespace + lifecycle locks before entering this transaction core.
func applyQualifiedPlan(ctx context.Context, p paths.Paths, plan Plan, m Mutator, setDesired bool) (MutationReport, error) {
	p = p.Normalize()
	var report MutationReport
	err := withNamespaceLock(p, plan.Name, func() error {
		return mounts.WithLock(p, plan.Name, func() error {
			var applyErr error
			report, applyErr = applyQualifiedPlanUnlocked(ctx, p, plan, m, setDesired)
			return applyErr
		})
	})
	return report, err
}

func applyQualifiedPlanUnlocked(ctx context.Context, p paths.Paths, plan Plan, m Mutator, setDesired bool) (MutationReport, error) {
	previous, previousErr := LoadState(p, plan.Name)
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return MutationReport{}, previousErr
	}
	state := State{SchemaVersion: SchemaVersion, Name: plan.Name, Desired: DesiredAppVisible, Mountpoint: plan.Mountpoint, UpdatedUnixMS: time.Now().UnixMilli()}
	if previousErr == nil {
		state = previous
		state.Desired = DesiredAppVisible
		state.Mountpoint = plan.Mountpoint
	}
	if plan.Evidence.SourceSignature == nil {
		return MutationReport{}, fmt.Errorf("source mount signature unavailable")
	}
	state.SourceSignature = *plan.Evidence.SourceSignature
	if setDesired || previousErr != nil {
		if err := writeState(p, state); err != nil {
			return MutationReport{}, err
		}
	}
	// Revalidate persisted ownership against the currently observed target
	// mount. Namespace churn can destroy a bind, while Android storage remounts
	// can replace it with a different mount at the same path. Missing binds lose
	// their stale marker and may be recreated; changed visible mounts lose Nexus
	// ownership and are never overmounted/unmounted merely because the path is
	// the same. Unreadable namespaces retain their marker until they can be
	// observed safely again.
	visibility := map[string]NamespaceVisibility{}
	for _, item := range plan.Evidence.Visibility {
		visibility[item.NamespaceID] = item
	}
	revalidated := make([]OwnedTarget, 0, len(state.Targets))
	for _, target := range state.Targets {
		item, ok := visibility[target.NamespaceID]
		if !ok {
			continue
		}
		if !item.Accessible {
			revalidated = append(revalidated, target)
			continue
		}
		if item.Visible && item.Signature != nil && *item.Signature == target.Signature {
			revalidated = append(revalidated, target)
		}
	}
	state.Targets = revalidated
	if err := writeState(p, state); err != nil {
		return MutationReport{}, err
	}
	existing := map[string]OwnedTarget{}
	for _, target := range state.Targets {
		existing[target.NamespaceID] = target
	}
	newly := []OwnedTarget{}
	restorePrevious := func() error {
		if previousErr == nil {
			return writeState(p, previous)
		}
		if os.IsNotExist(previousErr) {
			return Forget(p, plan.Name)
		}
		return previousErr
	}
	rollbackTransaction := func(cause error) error {
		rollbackErr := rollbackTargets(p, plan.Mountpoint, newly, m, plan.Evidence.Topology)
		restoreErr := restorePrevious()
		parts := []string{cause.Error()}
		if rollbackErr != nil {
			parts = append(parts, "rollback failed: "+rollbackErr.Error())
		}
		if restoreErr != nil {
			parts = append(parts, "state restore failed: "+restoreErr.Error())
		}
		return errors.New(strings.Join(parts, "; "))
	}
	for _, action := range plan.Actions {
		select {
		case <-ctx.Done():
			return MutationReport{}, rollbackTransaction(ctx.Err())
		default:
		}
		if action.AlreadyVisible || action.Mutation != "bind-same-path" {
			continue
		}
		if _, ok := existing[action.NamespaceID]; ok {
			continue
		}
		sig, err := m.Bind(plan.Evidence.Topology.ServicePID, action.RepresentativePID, action.NamespaceID, plan.Mountpoint)
		if err != nil {
			return MutationReport{}, rollbackTransaction(fmt.Errorf("namespace apply failed: %w", err))
		}
		target := OwnedTarget{NamespaceID: action.NamespaceID, UserIDs: action.UserIDs, Classes: action.Classes, Signature: sig, AppliedUnixMS: time.Now().UnixMilli()}
		state.Targets = append(state.Targets, target)
		newly = append(newly, target)
		existing[target.NamespaceID] = target
		if err := writeState(p, state); err != nil {
			return MutationReport{}, rollbackTransaction(fmt.Errorf("persist namespace ownership: %w", err))
		}
	}
	live := map[string]bool{}
	for _, ref := range plan.Evidence.Topology.Namespaces {
		live[ref.ID] = true
	}
	kept := state.Targets[:0]
	for _, target := range state.Targets {
		if live[target.NamespaceID] {
			kept = append(kept, target)
		}
	}
	state.Targets = kept
	evidence, err := Inspect(p, plan.Name)
	if err != nil {
		return MutationReport{}, rollbackTransaction(fmt.Errorf("verify namespace visibility: %w", err))
	}
	state.LastClaim = evidence.Claim
	if err := writeState(p, state); err != nil {
		return MutationReport{}, rollbackTransaction(fmt.Errorf("persist namespace verification: %w", err))
	}
	return MutationReport{SchemaVersion: SchemaVersion, Name: plan.Name, Desired: DesiredAppVisible, Adapter: plan.Adapter, Applied: len(newly), Evidence: evidence}, nil
}

func RollbackPreview(p paths.Paths, name string) (Plan, error) {
	evidence, err := Inspect(p, name)
	if err != nil {
		return Plan{}, err
	}
	state, err := LoadState(p, name)
	if err != nil {
		if os.IsNotExist(err) {
			return Plan{SchemaVersion: SchemaVersion, Name: name, Mountpoint: evidence.Mountpoint, Desired: "root", Adapter: "none", Qualified: true, Evidence: evidence}, nil
		}
		return Plan{}, err
	}
	plan := Plan{SchemaVersion: SchemaVersion, Name: name, Mountpoint: state.Mountpoint, Desired: "root", Adapter: "same-path-bind-v1", Qualified: true, Evidence: evidence}
	refs := map[string]NamespaceRef{}
	for _, ref := range evidence.Topology.Namespaces {
		refs[ref.ID] = ref
	}
	for _, target := range state.Targets {
		ref, ok := refs[target.NamespaceID]
		action := Action{NamespaceID: target.NamespaceID, Classes: target.Classes, UserIDs: target.UserIDs, Mutation: "release-owned-bind"}
		if ok {
			action.RepresentativePID = ref.RepresentativePID
		} else {
			action.Mutation = "namespace-gone"
		}
		plan.Actions = append(plan.Actions, action)
	}
	return plan, nil
}

func Rollback(ctx context.Context, p paths.Paths, name string) (MutationReport, error) {
	return rollbackWithMutator(ctx, p, name, defaultMutator(), true)
}
func SuspendOwned(ctx context.Context, p paths.Paths, name string) (MutationReport, error) {
	return rollbackWithMutator(ctx, p, name, defaultMutator(), false)
}

func rollbackWithMutator(ctx context.Context, p paths.Paths, name string, m Mutator, clearDesired bool) (MutationReport, error) {
	p = p.Normalize()
	var report MutationReport
	err := withNamespaceLock(p, name, func() error {
		return mounts.WithLock(p, name, func() error {
			state, err := LoadState(p, name)
			if err != nil {
				if os.IsNotExist(err) {
					e, _ := Inspect(p, name)
					report = MutationReport{SchemaVersion: SchemaVersion, Name: name, Desired: "root", Adapter: "none", Evidence: e}
					return nil
				}
				return err
			}
			topo, _, err := Discover()
			if err != nil {
				return err
			}
			refs := map[string]NamespaceRef{}
			for _, ref := range topo.Namespaces {
				refs[ref.ID] = ref
			}
			remaining := append([]OwnedTarget(nil), state.Targets...)
			released := 0
			for i := len(state.Targets) - 1; i >= 0; i-- {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				target := state.Targets[i]
				ref, ok := refs[target.NamespaceID]
				if !ok {
					remaining = removeTarget(remaining, target.NamespaceID)
					released++
					continue
				}
				if err := m.Unmount(ref.RepresentativePID, target.NamespaceID, state.Mountpoint, target.Signature); err != nil {
					state.Targets = remaining
					_ = writeState(p, state)
					return err
				}
				remaining = removeTarget(remaining, target.NamespaceID)
				released++
				state.Targets = remaining
				if err := writeState(p, state); err != nil {
					return err
				}
			}
			if clearDesired {
				if err := Forget(p, name); err != nil {
					return err
				}
			} else {
				state.Targets = nil
				state.Desired = DesiredAppVisible
				if err := writeState(p, state); err != nil {
					return err
				}
			}
			evidence, inspectErr := Inspect(p, name)
			if inspectErr != nil {
				if clearDesired {
					return inspectErr
				}
				evidence = Inspection{SchemaVersion: SchemaVersion, Name: name, Mountpoint: state.Mountpoint, Claim: "source_unavailable"}
			}
			report = MutationReport{SchemaVersion: SchemaVersion, Name: name, Desired: func() string {
				if clearDesired {
					return "root"
				}
				return DesiredAppVisible
			}(), Adapter: "same-path-bind-v1", Released: released, Evidence: evidence}
			return nil
		})
	})
	if err == nil {
		_ = diagnostics.Append(p, "namespace", name, "released", "", map[string]any{"desired": report.Desired, "released": report.Released})
	}
	return report, err
}

func rollbackTargets(p paths.Paths, mountpoint string, targets []OwnedTarget, m Mutator, topo Topology) error {
	refs := map[string]NamespaceRef{}
	for _, r := range topo.Namespaces {
		refs[r.ID] = r
	}
	var errs []string
	for i := len(targets) - 1; i >= 0; i-- {
		target := targets[i]
		ref, ok := refs[target.NamespaceID]
		if !ok {
			continue
		}
		if err := m.Unmount(ref.RepresentativePID, target.NamespaceID, mountpoint, target.Signature); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
func removeTarget(in []OwnedTarget, id string) []OwnedTarget {
	out := in[:0]
	for _, target := range in {
		if target.NamespaceID != id {
			out = append(out, target)
		}
	}
	return out
}

func withNamespaceLock(p paths.Paths, name string, fn func() error) error {
	p = p.Normalize()
	if err := os.MkdirAll(p.LockDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(p.LockDir, "namespace-"+name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
