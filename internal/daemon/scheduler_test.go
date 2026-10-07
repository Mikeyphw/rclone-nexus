package daemon

import (
	"context"
	"os"
	"path/filepath"
	"rclone-nexus/internal/control"
	"rclone-nexus/internal/diagnostics"
	"rclone-nexus/internal/jobs"
	"rclone-nexus/internal/journal"
	"rclone-nexus/internal/paths"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSchedulerDoesNotDuplicateDueJob(t *testing.T) {
	d := t.TempDir()
	p := paths.Paths{ModuleDir: filepath.Join(d, "module"), StateDir: filepath.Join(d, "state"), RunDir: filepath.Join(d, "state", "run"), ProviderModuleDir: filepath.Join(d, "provider")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneConfig, []byte("[r]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	count := filepath.Join(d, "count")
	body := "#!/bin/sh\nn=0\n[ -f '" + count + "' ] && n=$(cat '" + count + "')\nn=$((n+1))\necho $n > '" + count + "'\nsleep 0.15\nexit 0\n"
	if err := os.MkdirAll(filepath.Join(p.ModuleDir, "system", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ModuleDir, "system", "bin", "rclone"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_JOB_SCHEDULER_INTERVAL", "100ms")
	c := []jobs.Config{{Name: "copy", Enabled: true, Type: jobs.TypeCopy, Source: "r:a", Destination: "r:b", Every: "1m", NetworkMode: "offline-allowed"}}
	pr, _ := jobs.PreviewCandidate(p, c)
	if _, err := jobs.ApplyCandidate(p, 0, pr.CandidateDigest, c); err != nil {
		t.Fatal(err)
	}
	s, _ := jobs.ReadState(p, "copy")
	s.NextRunUnixMS = time.Now().Add(-time.Second).UnixMilli()
	if err := jobs.WriteState(p, s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer cancel()
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		RunJobScheduler(ctx, p, control.New(p))
	}()
	<-ctx.Done()
	waitSchedulerStopped(t, schedulerDone)

	deadline := time.Now().Add(2 * time.Second)
	for {
		b, readErr := os.ReadFile(count)
		state, stateErr := jobs.ReadState(p, "copy")
		journalComplete := false
		if stateErr == nil && state.LastRequestID != "" {
			record, journalErr := journal.Get(p, state.LastRequestID)
			journalComplete = journalErr == nil && record.State == journal.StateSucceeded
		}
		if readErr == nil && strings.TrimSpace(string(b)) == "1" && stateErr == nil && state.RunCount == 1 && state.LastState == "SUCCEEDED" && journalComplete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduled job did not settle exactly once: count=%q read_err=%v state=%+v state_err=%v journal_complete=%v", strings.TrimSpace(string(b)), readErr, state, stateErr, journalComplete)
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("scheduled job ran %d times", n)
	}
}

func TestSchedulerRestartDoesNotReplayAlreadyClaimedRun(t *testing.T) {
	d := t.TempDir()
	p := paths.Paths{ModuleDir: filepath.Join(d, "module"), StateDir: filepath.Join(d, "state"), RunDir: filepath.Join(d, "state", "run"), ProviderModuleDir: filepath.Join(d, "provider")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneConfig, []byte("[r]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	count := filepath.Join(d, "restart-count")
	body := "#!/bin/sh\nn=0\n[ -f '" + count + "' ] && n=$(cat '" + count + "')\nn=$((n+1))\necho $n > '" + count + "'\nexit 0\n"
	if err := os.MkdirAll(filepath.Join(p.ModuleDir, "system", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ModuleDir, "system", "bin", "rclone"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_JOB_SCHEDULER_INTERVAL", "100ms")
	cfg := []jobs.Config{{Name: "copy", Enabled: true, Type: jobs.TypeCopy, Source: "r:a", Destination: "r:b", Every: "1m", NetworkMode: "offline-allowed"}}
	pr, err := jobs.PreviewCandidate(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.ApplyCandidate(p, 0, pr.CandidateDigest, cfg); err != nil {
		t.Fatal(err)
	}
	s, err := jobs.ReadState(p, "copy")
	if err != nil {
		t.Fatal(err)
	}
	s.NextRunUnixMS = time.Now().Add(-time.Second).UnixMilli()
	if err := jobs.WriteState(p, s); err != nil {
		t.Fatal(err)
	}

	runScheduler := func(d time.Duration) {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		schedulerDone := make(chan struct{})
		go func() {
			defer close(schedulerDone)
			RunJobScheduler(ctx, p, control.New(p))
		}()
		<-ctx.Done()
		waitSchedulerStopped(t, schedulerDone)
	}
	runScheduler(220 * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		b, err := os.ReadFile(count)
		if err == nil && strings.TrimSpace(string(b)) == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first scheduled run did not complete: data=%q err=%v", string(b), err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Simulate racd restart. next_run was claimed durably before launch, so the
	// restarted scheduler must not replay the same due occurrence.
	runScheduler(220 * time.Millisecond)
	b, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "1" {
		t.Fatalf("scheduler restart replayed claimed run: count=%q", strings.TrimSpace(string(b)))
	}
}

func TestSchedulerCancellationDoesNotCancelDispatchedRunBeforeDurableClaim(t *testing.T) {
	d := t.TempDir()
	p := paths.Paths{ModuleDir: filepath.Join(d, "module"), StateDir: filepath.Join(d, "state"), RunDir: filepath.Join(d, "state", "run"), ProviderModuleDir: filepath.Join(d, "provider")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneConfig, []byte("[r]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(d, "dispatched-complete")
	body := "#!/bin/sh\nsleep 0.20\necho complete > '" + marker + "'\nexit 0\n"
	if err := os.MkdirAll(filepath.Join(p.ModuleDir, "system", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ModuleDir, "system", "bin", "rclone"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_JOB_SCHEDULER_INTERVAL", "100ms")
	cfg := []jobs.Config{{Name: "copy", Enabled: true, Type: jobs.TypeCopy, Source: "r:a", Destination: "r:b", Every: "1m", NetworkMode: "offline-allowed"}}
	pr, err := jobs.PreviewCandidate(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.ApplyCandidate(p, 0, pr.CandidateDigest, cfg); err != nil {
		t.Fatal(err)
	}
	s, err := jobs.ReadState(p, "copy")
	if err != nil {
		t.Fatal(err)
	}
	s.NextRunUnixMS = time.Now().Add(-time.Second).UnixMilli()
	if err := jobs.WriteState(p, s); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		RunJobScheduler(ctx, p, control.New(p))
	}()
	<-ctx.Done()
	waitSchedulerStopped(t, schedulerDone)

	deadline := time.Now().Add(2 * time.Second)
	for {
		markerOK := false
		if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == "complete" {
			markerOK = true
		}
		state, err := jobs.ReadState(p, "copy")
		stateOK := err == nil && state.RunCount == 1 && state.LastState == "SUCCEEDED" && state.NextRunUnixMS > time.Now().UnixMilli()
		journalOK := false
		if stateOK && state.LastRequestID != "" {
			record, journalErr := journal.Get(p, state.LastRequestID)
			journalOK = journalErr == nil && record.State == journal.StateSucceeded
		}
		// journal.Complete happens before the engine appends its final diagnostics
		// event. Wait for that event too so the dispatched operation has finished
		// all filesystem writes before t.TempDir cleanup starts. This matters on
		// slower filesystems (notably Termux), where cleanup could otherwise race
		// diagnostics.Append and fail with "directory not empty".
		diagnosticsOK := false
		if snapshot, diagnosticsErr := diagnostics.ReadLogs(p, 50, 0); diagnosticsErr == nil {
			for _, record := range snapshot.Records {
				if record.Source == "events" && record.Category == "operation" && record.Name == "job.run" && record.State == "succeeded" {
					diagnosticsOK = true
					break
				}
			}
		}
		if markerOK && stateOK && journalOK && diagnosticsOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dispatched scheduled run was cancelled or not durably completed: marker=%v state=%+v journal_complete=%v diagnostics_complete=%v err=%v", markerOK, state, journalOK, diagnosticsOK, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitSchedulerStopped(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop after context cancellation")
	}
}
