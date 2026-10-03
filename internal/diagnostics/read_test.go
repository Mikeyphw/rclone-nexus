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

func TestRcloneHelpTextIsNotMisclassifiedAndIsCollapsed(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{StateDir: root, LogDir: filepath.Join(root, "logs"), DiagnosticsDir: filepath.Join(root, "diagnostics")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "--fake-flag string Description mentioning error handling")
	}
	lines = append(lines, "2026/10/02 23:19:41 NOTICE: Fatal error: unknown flag: --rc-no-open-browser")
	if err := os.WriteFile(filepath.Join(p.LogDir, "mount-drive.log"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := ReadLogs(p, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fatal, collapsed bool
	for _, rec := range snap.Records {
		if strings.Contains(rec.Message, "unknown flag") {
			fatal = rec.Severity == "ERROR"
			collapsed = rec.Suppressed == 20 && len(rec.Details) > 0
		}
		if strings.Contains(rec.Message, "Description mentioning error") && rec.Severity == "ERROR" {
			t.Fatalf("help text misclassified as error: %+v", rec)
		}
	}
	if !fatal || !collapsed {
		t.Fatalf("fatal=%v collapsed=%v records=%+v", fatal, collapsed, snap.Records)
	}
}

func TestRcloneLogTimestampIsParsedPerLine(t *testing.T) {
	stamp, severity, message := parseRuntimeLogLine("2026/10/02 20:19:41 INFO  : hello", 1)
	if stamp == 1 || severity != "INFO" || message != "hello" {
		t.Fatalf("stamp=%d severity=%q message=%q", stamp, severity, message)
	}
}
