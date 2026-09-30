package diagnostics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"rclone-nexus/internal/paths"
)

const maxReadableLogLine = 64 << 10

type LogRecord struct {
	TimeUnixMS int64  `json:"time_unix_ms"`
	Source     string `json:"source"`
	Severity   string `json:"severity"`
	Category   string `json:"category,omitempty"`
	Name       string `json:"name,omitempty"`
	State      string `json:"state,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	Data       any    `json:"data,omitempty"`
}

type LogSnapshot struct {
	SchemaVersion int         `json:"schema_version"`
	Records       []LogRecord `json:"records"`
	Truncated     bool        `json:"truncated"`
}

func severityFor(state, code, message string) string {
	value := strings.ToLower(state + " " + code + " " + message)
	switch {
	case strings.Contains(value, "fail") || strings.Contains(value, "error") || strings.Contains(value, "fatal"):
		return "ERROR"
	case strings.Contains(value, "warn") || strings.Contains(value, "retry") || strings.Contains(value, "degrad") || strings.Contains(value, "offline"):
		return "WARN"
	case strings.Contains(value, "debug"):
		return "DEBUG"
	default:
		return "INFO"
	}
}

func ReadLogs(p paths.Paths, limit int, afterUnixMS int64) (LogSnapshot, error) {
	p = p.Normalize()
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	records := make([]LogRecord, 0, limit)
	truncated := false

	eventPath := filepath.Join(p.DiagnosticsDir, "events.jsonl")
	if f, err := os.Open(eventPath); err == nil {
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), maxReadableLogLine)
		for scanner.Scan() {
			var ev Event
			if json.Unmarshal(scanner.Bytes(), &ev) != nil || ev.TimeUnixMS <= afterUnixMS {
				continue
			}
			records = append(records, LogRecord{TimeUnixMS: ev.TimeUnixMS, Source: "events", Severity: severityFor(ev.State, ev.Code, ""), Category: ev.Category, Name: ev.Name, State: ev.State, Code: ev.Code, Data: ev.Data})
			if len(records) > 2000 {
				records = records[len(records)-2000:]
				truncated = true
			}
		}
		_ = f.Close()
		if err := scanner.Err(); err != nil {
			return LogSnapshot{}, err
		}
	} else if !os.IsNotExist(err) {
		return LogSnapshot{}, err
	}

	entries, _ := os.ReadDir(p.LogDir)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !(name == "nexus.log" || name == "service.log" || name == "racd.log" || strings.HasPrefix(name, "mount-")) {
			continue
		}
		path := filepath.Join(p.LogDir, name)
		info, err := entry.Info()
		if err != nil || info.ModTime().UnixMilli() <= afterUnixMS {
			continue
		}
		data, err := tailFileBounded(path, 32<<10)
		if err != nil {
			continue
		}
		clean := SanitizeText(string(data), p.StateDir, p.ModuleDir, p.ProviderModuleDir, p.RcloneConfig)
		lines := strings.Split(strings.TrimSpace(clean), "\n")
		start := 0
		if len(lines) > 40 {
			start = len(lines) - 40
			truncated = true
		}
		for _, line := range lines[start:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			records = append(records, LogRecord{TimeUnixMS: info.ModTime().UnixMilli(), Source: name, Severity: severityFor("", "", line), Message: line})
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].TimeUnixMS < records[j].TimeUnixMS })
	if len(records) > limit {
		records = records[len(records)-limit:]
		truncated = true
	}
	return LogSnapshot{SchemaVersion: 1, Records: records, Truncated: truncated}, nil
}

func tailFileBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := info.Size() - max
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		return nil, err
	}
	b := make([]byte, info.Size()-start)
	n, err := f.Read(b)
	if err != nil && n == 0 {
		return nil, fmt.Errorf("read log: %w", err)
	}
	return b[:n], nil
}
