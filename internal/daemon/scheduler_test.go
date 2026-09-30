package daemon

import (
	"context"
	"os"
	"path/filepath"
	"rclone-nexus/internal/control"
	"rclone-nexus/internal/jobs"
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
