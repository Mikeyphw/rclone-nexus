package webui

import (
	"testing"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/websettings"
)

func TestResolveIdleUsesPersistedWebUISettings(t *testing.T) {
	p := paths.Paths{StateDir: t.TempDir()}.Normalize()
	preview, err := websettings.PreviewCandidate(p, websettings.Settings{WebUIIdleSeconds: 75})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := websettings.Apply(p, preview.CurrentRevision, preview.CandidateDigest, preview.Settings); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveIdle(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != 75*time.Second {
		t.Fatalf("idle=%s", got)
	}
	explicit, err := ResolveIdle(p, "90")
	if err != nil {
		t.Fatal(err)
	}
	if explicit != 90*time.Second {
		t.Fatalf("explicit idle=%s", explicit)
	}
}
