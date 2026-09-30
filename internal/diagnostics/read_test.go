package diagnostics

import (
	"os"
	"path/filepath"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/websettings"
	"strings"
	"testing"
)

func TestReadLogsBoundedAndRedacted(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{StateDir: root, LogDir: filepath.Join(root, "logs"), DiagnosticsDir: filepath.Join(root, "diagnostics")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if err := Append(p, "policy", "drive", "WARN", "retrying", map[string]any{"token": "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.LogDir, "racd.log"), []byte("Authorization: Bearer verysecret\nINFO hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := ReadLogs(p, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := []byte{}
	for _, r := range snap.Records {
		b = append(b, []byte(r.Message)...)
		if r.Severity == "" {
			t.Fatal("severity missing")
		}
	}
	if strings.Contains(string(b), "verysecret") {
		t.Fatal("secret leaked")
	}
	if len(snap.Records) == 0 || len(snap.Records) > 10 {
		t.Fatalf("bad records: %d", len(snap.Records))
	}
}

func TestLogLimitsUsePersistedWebUISettings(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{StateDir: root, LogDir: filepath.Join(root, "logs"), DiagnosticsDir: filepath.Join(root, "diagnostics")}.Normalize()
	preview, err := websettings.PreviewCandidate(p, websettings.Settings{LogMaxBytes: 32 << 10, LogBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := websettings.Apply(p, preview.CurrentRevision, preview.CandidateDigest, preview.Settings); err != nil {
		t.Fatal(err)
	}
	maxBytes, backups := logLimits(p)
	if maxBytes != 32<<10 || backups != 2 {
		t.Fatalf("limits=%d/%d", maxBytes, backups)
	}
}
