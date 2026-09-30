package daemon

import (
	"context"
	"os"
	"path/filepath"
	"rclone-nexus/internal/control"
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
	p := paths.Paths{StateDir: filepath.Join(d, "state"), RunDir: filepath.Join(d, "state", "run"), ProviderModuleDir: filepath.Join(d, "provider"), RcloneConfig: filepath.Join(d, "rclone.conf")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.RcloneConfig, []byte("[r]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	count := filepath.Join(d, "count")
	script := filepath.Join(d, "rclone")
	body := "#!/bin/sh\nn=0\n[ -f '" + count + "' ] && n=$(cat '" + count + "')\nn=$((n+1))\necho $n > '" + count + "'\nsleep 0.15\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", script)
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
	go RunJobScheduler(ctx, p, control.New(p))
	<-ctx.Done()
	time.Sleep(80 * time.Millisecond)
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
	p := paths.Paths{StateDir: filepath.Join(d, "state"), RunDir: filepath.Join(d, "state", "run"), ProviderModuleDir: filepath.Join(d, "provider"), RcloneConfig: filepath.Join(d, "rclone.conf")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.RcloneConfig, []byte("[r]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	count := filepath.Join(d, "restart-count")
	script := filepath.Join(d, "rclone")
	body := "#!/bin/sh\nn=0\n[ -f '" + count + "' ] && n=$(cat '" + count + "')\nn=$((n+1))\necho $n > '" + count + "'\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", script)
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
		go RunJobScheduler(ctx, p, control.New(p))
		<-ctx.Done()
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
	p := paths.Paths{StateDir: filepath.Join(d, "state"), RunDir: filepath.Join(d, "state", "run"), ProviderModuleDir: filepath.Join(d, "provider"), RcloneConfig: filepath.Join(d, "rclone.conf")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.RcloneConfig, []byte("[r]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(d, "dispatched-complete")
	script := filepath.Join(d, "rclone")
	body := "#!/bin/sh\nsleep 0.20\necho complete > '" + marker + "'\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", script)
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
	go RunJobScheduler(ctx, p, control.New(p))
	<-ctx.Done()

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
		if markerOK && stateOK && journalOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dispatched scheduled run was cancelled or not durably completed: marker=%v state=%+v journal_complete=%v err=%v", markerOK, state, journalOK, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
