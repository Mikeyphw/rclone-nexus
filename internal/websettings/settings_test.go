package websettings

import (
	"os"
	"path/filepath"
	"rclone-nexus/internal/paths"
	"sync"
	"testing"
)

func TestSettingsRevisionAndValidation(t *testing.T) {
	p := paths.Paths{StateDir: t.TempDir()}.Normalize()
	prev, err := PreviewCandidate(p, Settings{RefreshSeconds: 7, LogLimit: 80, LogFollow: true, LogMaxBytes: 32768, LogBackups: 2, ReducedMotion: true, DefaultView: "runtime", DefaultMountView: "dense", WebUIIdleSeconds: 75})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Apply(p, prev.CurrentRevision, prev.CandidateDigest, prev.Settings)
	if err != nil || snap.Revision != 1 {
		t.Fatalf("%+v %v", snap, err)
	}

	if !snap.Settings.ReducedMotion || !snap.Settings.DenseMode || snap.Settings.LogMaxBytes != 32768 || snap.Settings.LogBackups != 2 || snap.Settings.WebUIIdleSeconds != 75 {
		t.Fatalf("settings not preserved: %+v", snap.Settings)
	}
	if _, err := Apply(p, 0, prev.CandidateDigest, prev.Settings); err == nil {
		t.Fatal("stale apply accepted")
	}
	info, err := filepath.Glob(filepath.Join(p.StateDir, "webui", "settings-v1.json"))
	if err != nil || len(info) != 1 {
		t.Fatal("settings missing")
	}
	st, err := os.Stat(info[0])
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode=%o", st.Mode().Perm())
	}
}
func TestConcurrentSettingsRevisionHasSingleWinner(t *testing.T) {
	p := paths.Paths{StateDir: t.TempDir()}.Normalize()
	prev, _ := PreviewCandidate(p, Settings{RefreshSeconds: 5, LogLimit: 100, DefaultView: "home"})
	var wg sync.WaitGroup
	wins := 0
	var mu sync.Mutex
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Apply(p, prev.CurrentRevision, prev.CandidateDigest, prev.Settings); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("wins=%d", wins)
	}
}
