package runtimemanager

import (
	"testing"

	"rclone-nexus/internal/migration"
	"rclone-nexus/internal/runtimeactivation"
	"rclone-nexus/internal/runtimeauth"
	"rclone-nexus/internal/runtimesource"
	"rclone-nexus/internal/runtimestate"
	"rclone-nexus/internal/runtimestore"
	"rclone-nexus/internal/runtimeupdate"
)

func manifest(id string, qualified bool, fuse bool) runtimestore.Manifest {
	m := runtimestore.Manifest{RuntimeID: id, Engine: "rclone", BinarySHA256: id + "sha", Qualification: runtimestore.Qualification{State: "failed", Qualified: false}}
	if qualified {
		m.Qualification.State = "qualified"
		m.Qualification.Qualified = true
	}
	if fuse {
		m.Qualification.Checks = []runtimestore.Check{{Name: "fuse_smoke_mount", Status: "pass", Required: true}}
	}
	return m
}

func TestProjectDisablesUnqualifiedActivationAndAllowsQualifiedRollback(t *testing.T) {
	items := []runtimestore.Manifest{manifest("active", true, true), manifest("previous", true, true), manifest("bad", false, false)}
	activation := runtimeactivation.Status{State: runtimestate.State{ActiveRuntimeID: "active", PreviousRuntimeID: "previous"}}
	got := Project(runtimeauth.Resolution{ActiveRuntimeID: "active"}, items, activation, runtimeupdate.Snapshot{}, nil, nil, migration.State{}, false, migration.Detection{})
	if got.SelectedFUSERuntimeID != "active" {
		t.Fatalf("selected FUSE runtime=%q", got.SelectedFUSERuntimeID)
	}
	if !got.Actions["rollback"].Enabled {
		t.Fatalf("rollback should be enabled: %+v", got.Actions["rollback"])
	}
	var bad Action
	for _, c := range got.Candidates {
		if c.Manifest.RuntimeID == "bad" {
			bad = c.Actions["activate"]
		}
	}
	if bad.Enabled || bad.Reason == "" {
		t.Fatalf("unqualified activate should be disabled with reason: %+v", bad)
	}
}

func TestProjectRejectsRollbackWhenPreviousMissingOrUnqualified(t *testing.T) {
	items := []runtimestore.Manifest{manifest("active", true, true), manifest("previous", false, false)}
	activation := runtimeactivation.Status{State: runtimestate.State{ActiveRuntimeID: "active", PreviousRuntimeID: "previous"}}
	got := Project(runtimeauth.Resolution{ActiveRuntimeID: "active"}, items, activation, runtimeupdate.Snapshot{}, nil, nil, migration.State{}, false, migration.Detection{})
	if got.Actions["rollback"].Enabled {
		t.Fatalf("rollback enabled for unqualified previous")
	}
	activation.State.PreviousRuntimeID = "missing"
	got = Project(runtimeauth.Resolution{ActiveRuntimeID: "active"}, items, activation, runtimeupdate.Snapshot{}, nil, nil, migration.State{}, false, migration.Detection{})
	if got.Actions["rollback"].Enabled {
		t.Fatalf("rollback enabled for missing previous")
	}
}

func TestProjectExposesRetryOnlyForRetryableUpdate(t *testing.T) {
	update := runtimeupdate.Snapshot{State: runtimeupdate.State{LastError: "network down", Retryable: true}}
	got := Project(runtimeauth.Resolution{}, nil, runtimeactivation.Status{}, update, nil, nil, migration.State{}, false, migration.Detection{})
	if !got.Actions["update_retry"].Enabled {
		t.Fatalf("retry should be enabled")
	}
	if len(got.Issues) != 1 || !got.Issues[0].Retryable {
		t.Fatalf("retryable issue missing: %+v", got.Issues)
	}
	update.State.Retryable = false
	got = Project(runtimeauth.Resolution{}, nil, runtimeactivation.Status{}, update, nil, nil, migration.State{}, false, migration.Detection{})
	if got.Actions["update_retry"].Enabled {
		t.Fatalf("retry should be disabled for terminal failure")
	}
	if len(got.Issues[0].RecoveryActions) != 0 {
		t.Fatalf("terminal issue should have no retry action")
	}
}

func TestProjectMigrationActionsFollowProviderAndPhase(t *testing.T) {
	detect := migration.Detection{ProviderPresent: true, ProviderEnabled: true}
	got := Project(runtimeauth.Resolution{}, nil, runtimeactivation.Status{}, runtimeupdate.Snapshot{}, nil, nil, migration.State{}, false, detect)
	if !got.Actions["migration_preview"].Enabled {
		t.Fatalf("preview should be enabled when provider exists")
	}
	state := migration.State{Phase: migration.PhaseAwaitingProviderDisable}
	detect.ProviderEnabled = false
	got = Project(runtimeauth.Resolution{}, nil, runtimeactivation.Status{}, runtimeupdate.Snapshot{}, nil, nil, state, true, detect)
	if !got.Actions["migration_finalize"].Enabled {
		t.Fatalf("finalize preview should be enabled after explicit provider disable")
	}
}

func TestProjectPublishesCanonicalSourceChannelChoices(t *testing.T) {
	sources := []runtimesource.Spec{
		{ID: "gh-build", Kind: runtimesource.KindGitHub, DefaultChannel: runtimesource.ChannelLatestStable, BuildRepository: "owner/build"},
		{ID: "newfuture", Kind: runtimesource.KindNewFuture, DefaultChannel: runtimesource.ChannelLatestStable},
		{ID: "local", Kind: runtimesource.KindLocalBinary, DefaultChannel: runtimesource.ChannelManualOnly},
	}
	got := Project(runtimeauth.Resolution{}, nil, runtimeactivation.Status{}, runtimeupdate.Snapshot{}, sources, nil, migration.State{}, false, migration.Detection{})
	byID := map[string]SourceChoice{}
	for _, choice := range got.SourceChoices {
		byID[choice.ID] = choice
	}
	if len(byID["gh-build"].Channels) != 3 || byID["gh-build"].Channels[2].Channel != runtimesource.ChannelPinnedCommit || !byID["gh-build"].Channels[2].RequiresRef {
		t.Fatalf("github build choices not canonical: %+v", byID["gh-build"])
	}
	for _, ch := range byID["newfuture"].Channels {
		if ch.Channel == runtimesource.ChannelPinnedCommit || ch.Channel == runtimesource.ChannelManualOnly {
			t.Fatalf("newfuture advertised impossible channel: %+v", byID["newfuture"])
		}
	}
	if len(byID["local"].Channels) != 1 || byID["local"].Channels[0].Channel != runtimesource.ChannelManualOnly {
		t.Fatalf("local source choices wrong: %+v", byID["local"])
	}
}
