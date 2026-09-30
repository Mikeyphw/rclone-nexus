package jobs

import (
	"context"
	"os"
	"path/filepath"
	"rclone-nexus/internal/paths"
	"testing"
	"time"
)

func jp(t *testing.T) paths.Paths {
	d := t.TempDir()
	p := paths.Paths{StateDir: d, RunDir: filepath.Join(d, "run"), ProviderModuleDir: filepath.Join(d, "provider"), RcloneConfig: filepath.Join(d, "rclone.conf")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.RcloneConfig, []byte("[x]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestSyncPreviewExposesDestructiveAndApplyRequiresApproval(t *testing.T) {
	p := jp(t)
	candidate := []Config{{Name: "s", Enabled: true, Type: TypeSync, Source: "r:a", Destination: "r:b", Every: "1h", NetworkMode: "offline-allowed"}}
	preview, err := PreviewCandidate(p, candidate)
	if err != nil {
		t.Fatalf("preview must remain available before destructive confirmation: %v", err)
	}
	if len(preview.Destructive) != 1 || preview.Destructive[0] != "s" {
		t.Fatalf("destructive sync not surfaced by preview: %+v", preview)
	}
	if _, err := ApplyCandidate(p, preview.CurrentRevision, preview.CandidateDigest, candidate); err == nil {
		t.Fatal("unconfirmed sync must not be persisted")
	}
	candidate[0].ConfirmDestructive = true
	preview, err = PreviewCandidate(p, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyCandidate(p, preview.CurrentRevision, preview.CandidateDigest, candidate); err != nil {
		t.Fatalf("confirmed sync should apply: %v", err)
	}
}
func TestApplyPersistsNextRunAcrossReload(t *testing.T) {
	p := jp(t)
	c := []Config{{Name: "c", Enabled: true, Type: TypeCopy, Source: "r:a", Destination: "r:b", Every: "1h", NetworkMode: "offline-allowed"}}
	pr, err := PreviewCandidate(p, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyCandidate(p, 0, pr.CandidateDigest, c); err != nil {
		t.Fatal(err)
	}
	s1, err := ReadState(p, "c")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ReadState(p, "c")
	if err != nil || s1.NextRunUnixMS != s2.NextRunUnixMS {
		t.Fatalf("state not persistent: %#v %#v", s1, s2)
	}
}
func TestRunParsesProgressAndCancellation(t *testing.T) {
	p := jp(t)
	script := filepath.Join(t.TempDir(), "rclone")
	body := "#!/bin/sh\necho '{\"bytes\":12,\"speed\":3,\"eta\":4}'\nif [ \"$1\" = copy ]; then sleep 5; fi\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", script)
	c := []Config{{Name: "c", Enabled: true, Type: TypeCopy, Source: "r:a", Destination: "r:b", Every: "1h", NetworkMode: "offline-allowed"}}
	pr, _ := PreviewCandidate(p, c)
	_, _ = ApplyCandidate(p, 0, pr.CandidateDigest, c)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	seen := false
	_, err := Run(ctx, p, "c", "manual", "req", func(_, _ string, data any) { seen = true })
	if err == nil || !seen {
		t.Fatalf("err=%v seen=%v", err, seen)
	}
}

func TestScheduledClaimMovesNextRunBeforeProcessAndPreventsImmediateDuplicate(t *testing.T) {
	p := jp(t)
	script := filepath.Join(t.TempDir(), "rclone")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", script)
	c := []Config{{Name: "c", Enabled: true, Type: TypeCopy, Source: "r:a", Destination: "r:b", Every: "1m", NetworkMode: "offline-allowed"}}
	pr, _ := PreviewCandidate(p, c)
	if _, err := ApplyCandidate(p, 0, pr.CandidateDigest, c); err != nil {
		t.Fatal(err)
	}
	s, _ := ReadState(p, "c")
	s.NextRunUnixMS = time.Now().Add(-time.Second).UnixMilli()
	if err := WriteState(p, s); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), p, "c", "schedule", "scheduled-c-1", nil); err != nil {
		t.Fatal(err)
	}
	after, err := ReadState(p, "c")
	if err != nil {
		t.Fatal(err)
	}
	if after.NextRunUnixMS <= time.Now().UnixMilli() {
		t.Fatalf("next run was not durably advanced: %+v", after)
	}
	due, err := Due(p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("duplicate schedule remained due: %v", due)
	}
}

func TestPolicyBlockedJobDoesNotLaunchProvider(t *testing.T) {
	p := jp(t)
	marker := filepath.Join(t.TempDir(), "launched")
	script := filepath.Join(t.TempDir(), "rclone")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", script)
	t.Setenv("RNEXUS_NETWORK_STATE", "cellular")
	c := []Config{{Name: "c", Enabled: true, Type: TypeCopy, Source: "r:a", Destination: "r:b", Every: "1h", NetworkMode: "wifi"}}
	pr, _ := PreviewCandidate(p, c)
	if _, err := ApplyCandidate(p, 0, pr.CandidateDigest, c); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), p, "c", "manual", "req", nil); err == nil {
		t.Fatal("expected policy block")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("provider launched despite policy block")
	}
}

func TestRejectsOnTheFlyBackendAndOptionLikeEndpoints(t *testing.T) {
	p := jp(t)
	for _, source := range []string{"--config=/tmp/x", ":s3,access_key_id=secret:bucket"} {
		if _, err := PreviewCandidate(p, []Config{{Name: "bad", Enabled: true, Type: TypeCopy, Source: source, Destination: "r:b", Every: "1h", NetworkMode: "offline-allowed"}}); err == nil {
			t.Fatalf("accepted endpoint %q", source)
		}
	}
}

func TestConcurrentRegistryApplyAllowsOnlyOneRevisionWinner(t *testing.T) {
	p := jp(t)
	candidate := []Config{{Name: "c", Enabled: true, Type: TypeCopy, Source: "r:a", Destination: "r:b", Every: "1h", NetworkMode: "offline-allowed"}}
	preview, err := PreviewCandidate(p, candidate)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := ApplyCandidate(p, preview.CurrentRevision, preview.CandidateDigest, candidate)
			results <- err
		}()
	}
	close(start)
	successes := 0
	failures := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("expected one revision winner and one stale loser, successes=%d failures=%d", successes, failures)
	}
	registry, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Revision != 1 {
		t.Fatalf("unexpected registry revision after concurrent apply: %d", registry.Revision)
	}
}
