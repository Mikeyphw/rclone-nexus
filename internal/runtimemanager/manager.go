package runtimemanager

import (
	"fmt"
	"strings"

	"rclone-nexus/internal/migration"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/runtimeactivation"
	"rclone-nexus/internal/runtimeauth"
	"rclone-nexus/internal/runtimesource"
	"rclone-nexus/internal/runtimestore"
	"rclone-nexus/internal/runtimeupdate"
)

const SchemaVersion = 1

type Action struct {
	Enabled   bool   `json:"enabled"`
	Operation string `json:"operation"`
	Class     string `json:"class"`
	Reason    string `json:"reason,omitempty"`
}

type Issue struct {
	Code            string   `json:"code"`
	Severity        string   `json:"severity"`
	Summary         string   `json:"summary"`
	Detail          string   `json:"detail,omitempty"`
	Retryable       bool     `json:"retryable"`
	RecoveryActions []string `json:"recovery_actions,omitempty"`
}

type Candidate struct {
	Manifest runtimestore.Manifest `json:"manifest"`
	Active   bool                  `json:"active"`
	Previous bool                  `json:"previous"`
	Staged   bool                  `json:"staged"`
	Actions  map[string]Action     `json:"actions"`
}

type MigrationView struct {
	Present   bool                `json:"present"`
	State     migration.State     `json:"state"`
	Detection migration.Detection `json:"detection"`
}

type Snapshot struct {
	SchemaVersion         int                        `json:"schema_version"`
	Runtime               runtimeauth.Resolution     `json:"runtime"`
	Active                *runtimestore.Manifest     `json:"active,omitempty"`
	SelectedFUSERuntimeID string                     `json:"selected_fuse_runtime_id,omitempty"`
	Candidates            []Candidate                `json:"candidates"`
	Activation            runtimeactivation.Status   `json:"activation"`
	Update                runtimeupdate.Snapshot     `json:"update"`
	Sources               []runtimesource.Spec       `json:"sources"`
	Resolutions           []runtimesource.Resolution `json:"resolutions"`
	Migration             MigrationView              `json:"migration"`
	Actions               map[string]Action          `json:"actions"`
	Issues                []Issue                    `json:"issues"`
}

func enabled(op, class string) Action { return Action{Enabled: true, Operation: op, Class: class} }
func disabled(op, class, reason string) Action {
	return Action{Enabled: false, Operation: op, Class: class, Reason: reason}
}

func qualified(m runtimestore.Manifest) bool {
	return m.Qualification.Qualified && m.Qualification.State == "qualified"
}

func hasPassedCheck(m runtimestore.Manifest, name string) bool {
	for _, c := range m.Qualification.Checks {
		if c.Name == name && c.Status == "pass" {
			return true
		}
	}
	return false
}

func find(items []runtimestore.Manifest, id string) *runtimestore.Manifest {
	if id == "" {
		return nil
	}
	for i := range items {
		if items[i].RuntimeID == id {
			copy := items[i]
			return &copy
		}
	}
	return nil
}

// Project centralizes UX/action policy so the CLI and WebUI do not reinvent
// safety decisions independently from runtime/update/migration authority.
func Project(runtime runtimeauth.Resolution, manifests []runtimestore.Manifest, activation runtimeactivation.Status, update runtimeupdate.Snapshot, sources []runtimesource.Spec, resolutions []runtimesource.Resolution, migrationState migration.State, migrationPresent bool, detection migration.Detection) Snapshot {
	s := Snapshot{SchemaVersion: SchemaVersion, Runtime: runtime, Activation: activation, Update: update, Sources: sources, Resolutions: resolutions, Migration: MigrationView{Present: migrationPresent, State: migrationState, Detection: detection}, Actions: map[string]Action{}, Issues: []Issue{}, Candidates: []Candidate{}}
	activeID := activation.State.ActiveRuntimeID
	if activeID == "" {
		activeID = runtime.ActiveRuntimeID
	}
	previousID := activation.State.PreviousRuntimeID
	stagedID := update.StagedRuntimeID
	if stagedID == "" {
		stagedID = activation.State.StagedRuntimeID
	}
	active := find(manifests, activeID)
	s.Active = active
	if active != nil && qualified(*active) && hasPassedCheck(*active, "fuse_smoke_mount") {
		s.SelectedFUSERuntimeID = active.RuntimeID
	}

	for _, m := range manifests {
		isActive, isPrevious, isStaged := m.RuntimeID == activeID, m.RuntimeID == previousID, m.RuntimeID == stagedID
		a := disabled("runtime.activate", "run", "candidate is not qualified")
		if activation.TransitionInProgress {
			a = disabled("runtime.activate", "run", "runtime transition is in progress")
		} else if isActive {
			a = disabled("runtime.activate", "run", "candidate is already active")
		} else if qualified(m) {
			a = enabled("runtime.activate", "run")
		}
		s.Candidates = append(s.Candidates, Candidate{Manifest: m, Active: isActive, Previous: isPrevious, Staged: isStaged, Actions: map[string]Action{"activate": a, "test": enabled("runtime.test", "run")}})
	}

	prev := find(manifests, previousID)
	if activation.TransitionInProgress {
		s.Actions["rollback"] = disabled("runtime.rollback", "run", "runtime transition is in progress")
	} else if prev == nil {
		s.Actions["rollback"] = disabled("runtime.rollback", "run", "previous runtime is not present")
	} else if !qualified(*prev) {
		s.Actions["rollback"] = disabled("runtime.rollback", "run", "previous runtime is not qualified")
	} else {
		s.Actions["rollback"] = enabled("runtime.rollback", "run")
	}

	if activation.TransitionInProgress {
		s.Actions["recover"] = enabled("runtime.recover", "reconcile")
	} else {
		s.Actions["recover"] = disabled("runtime.recover", "reconcile", "no runtime transaction requires recovery")
	}
	s.Actions["update_check"] = enabled("runtime.update.check", "run")
	staged := find(manifests, update.StagedRuntimeID)
	if activation.TransitionInProgress || update.TransitionInProgress {
		s.Actions["update_activate"] = disabled("runtime.update.activate", "run", "runtime transition is in progress")
	} else if update.StagedRuntimeID == "" {
		s.Actions["update_activate"] = disabled("runtime.update.activate", "run", "no staged runtime")
	} else if update.StagedMissing || staged == nil {
		s.Actions["update_activate"] = disabled("runtime.update.activate", "run", "staged runtime bytes are missing")
	} else if !qualified(*staged) {
		s.Actions["update_activate"] = disabled("runtime.update.activate", "run", "staged runtime is not qualified")
	} else {
		s.Actions["update_activate"] = enabled("runtime.update.activate", "run")
	}
	if update.State.Retryable {
		s.Actions["update_retry"] = enabled("runtime.update.check", "run")
	} else {
		s.Actions["update_retry"] = disabled("runtime.update.check", "run", "last update result is not retryable")
	}

	s.Actions["source_resolve"] = enabled("runtime.source.resolve", "run")
	s.Actions["source_import_local"] = enabled("runtime.source.import-local", "run")
	s.Actions["source_register"] = enabled("runtime.source.register", "run")

	phase := migrationState.Phase
	if detection.ProviderPresent && detection.ProviderEnabled && (!migrationPresent || phase == migration.PhaseRolledBack || phase == migration.PhaseConflict) {
		s.Actions["migration_preview"] = enabled("migration.preview", "preview")
	} else {
		reason := "legacy provider is not present and enabled"
		if migrationPresent && phase == migration.PhaseAwaitingProviderDisable {
			reason = "migration is awaiting explicit provider disable"
		}
		if phase == migration.PhaseCompleted {
			reason = "standalone migration is already complete"
		}
		s.Actions["migration_preview"] = disabled("migration.preview", "preview", reason)
	}
	if migrationPresent && phase == migration.PhaseAwaitingProviderDisable && !detection.ProviderEnabled {
		s.Actions["migration_finalize"] = enabled("migration.finalize.preview", "preview")
	} else {
		s.Actions["migration_finalize"] = disabled("migration.finalize.preview", "preview", "finalization requires AWAITING_PROVIDER_DISABLE with the legacy provider disabled")
	}
	if migrationPresent && phase != migration.PhaseCompleted && phase != migration.PhaseRolledBack {
		s.Actions["migration_rollback"] = enabled("migration.rollback", "run")
	} else {
		s.Actions["migration_rollback"] = disabled("migration.rollback", "run", "no reversible migration transaction is active")
	}

	if runtime.AmbiguousAuthority {
		s.Issues = append(s.Issues, Issue{Code: "ambiguous_runtime_authority", Severity: "error", Summary: "Runtime authority is ambiguous", Detail: "Managed execution will fail closed until competing authority is resolved.", RecoveryActions: []string{"migration.inspect"}})
	}
	if activation.TransitionInProgress {
		s.Issues = append(s.Issues, Issue{Code: "runtime_transition_in_progress", Severity: "warning", Summary: "Runtime transaction needs recovery", Retryable: true, RecoveryActions: []string{"runtime.recover"}})
	}
	if strings.TrimSpace(update.State.LastError) != "" {
		issue := Issue{Code: "runtime_update_failed", Severity: "error", Summary: "Runtime update failed", Detail: update.State.LastError, Retryable: update.State.Retryable}
		if update.State.Retryable {
			issue.RecoveryActions = []string{"runtime.update.check"}
		}
		s.Issues = append(s.Issues, issue)
	}
	if migrationPresent && strings.TrimSpace(migrationState.LastError) != "" {
		actions := []string{}
		if phase != migration.PhaseCompleted {
			actions = append(actions, "migration.rollback")
		}
		s.Issues = append(s.Issues, Issue{Code: "migration_failed", Severity: "error", Summary: "Migration needs attention", Detail: migrationState.LastError, Retryable: phase != migration.PhaseConflict, RecoveryActions: actions})
	}
	if activeID != "" && active == nil {
		s.Issues = append(s.Issues, Issue{Code: "active_runtime_missing", Severity: "error", Summary: "Active runtime manifest is missing", Detail: fmt.Sprintf("activation references %s but immutable store does not contain it", activeID), RecoveryActions: []string{"runtime.recover"}})
	}
	return s
}

func SnapshotOf(p paths.Paths) (Snapshot, error) {
	runtime, err := runtimeauth.Resolve(p)
	if err != nil {
		return Snapshot{}, err
	}
	manifests, err := runtimestore.List(p)
	if err != nil {
		return Snapshot{}, err
	}
	activation, err := runtimeactivation.StatusOf(p)
	if err != nil {
		return Snapshot{}, err
	}
	update, err := runtimeupdate.SnapshotOf(p)
	if err != nil {
		return Snapshot{}, err
	}
	sources, err := runtimesource.List(p)
	if err != nil {
		return Snapshot{}, err
	}
	resolutions, err := runtimesource.ListResolutions(p)
	if err != nil {
		return Snapshot{}, err
	}
	migrationState, present, err := migration.StateSnapshot(p)
	if err != nil {
		return Snapshot{}, err
	}
	detection, err := migration.Detect(p)
	if err != nil {
		return Snapshot{}, err
	}
	return Project(runtime, manifests, activation, update, sources, resolutions, migrationState, present, detection), nil
}
