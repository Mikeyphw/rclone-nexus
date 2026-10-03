package diagnostics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
)

const maxReadableLogLine = 64 << 10

type LogRecord struct {
	TimeUnixMS int64    `json:"time_unix_ms"`
	Source     string   `json:"source"`
	Severity   string   `json:"severity"`
	Category   string   `json:"category,omitempty"`
	Name       string   `json:"name,omitempty"`
	State      string   `json:"state,omitempty"`
	Code       string   `json:"code,omitempty"`
	Message    string   `json:"message,omitempty"`
	Data       any      `json:"data,omitempty"`
	Suppressed int      `json:"suppressed,omitempty"`
	Details    []string `json:"details,omitempty"`
}

type LogSnapshot struct {
	SchemaVersion int         `json:"schema_version"`
	Records       []LogRecord `json:"records"`
	Truncated     bool        `json:"truncated"`
}

func severityFor(state, code, message string) string {
	value := strings.ToLower(strings.TrimSpace(state + " " + code))
	switch {
	case strings.Contains(value, "fail") || strings.Contains(value, "error") || strings.Contains(value, "fatal"):
		return "ERROR"
	case strings.Contains(value, "warn") || strings.Contains(value, "retry") || strings.Contains(value, "degrad") || strings.Contains(value, "offline"):
		return "WARN"
	case strings.Contains(value, "debug"):
		return "DEBUG"
	}
	trimmed := strings.TrimSpace(message)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "fatal error:"), strings.HasPrefix(lower, "error:"), strings.HasPrefix(lower, "failed:"):
		return "ERROR"
	case strings.HasPrefix(lower, "warning:"), strings.HasPrefix(lower, "warn:"):
		return "WARN"
	case strings.HasPrefix(lower, "debug:"):
		return "DEBUG"
	default:
		return "INFO"
	}
}

func parseRuntimeLogLine(line string, fallbackUnixMS int64) (int64, string, string) {
	trimmed := strings.TrimSpace(line)
	stamp := fallbackUnixMS
	level := ""
	message := trimmed
	if len(trimmed) >= 20 {
		prefix := trimmed[:19]
		if parsed, err := time.ParseInLocation("2006/01/02 15:04:05", prefix, time.Local); err == nil {
			stamp = parsed.UnixMilli()
			message = strings.TrimSpace(trimmed[19:])
		}
	}
	if idx := strings.Index(message, ": "); idx > 0 {
		candidate := strings.ToUpper(strings.TrimSpace(message[:idx]))
		switch candidate {
		case "DEBUG", "INFO", "NOTICE", "WARNING", "WARN", "ERROR", "FATAL":
			level = candidate
			message = strings.TrimSpace(message[idx+2:])
		}
	}
	severity := "INFO"
	switch level {
	case "DEBUG":
		severity = "DEBUG"
	case "WARNING", "WARN":
		severity = "WARN"
	case "ERROR", "FATAL":
		severity = "ERROR"
	}
	lower := strings.ToLower(message)
	if strings.HasPrefix(lower, "fatal error:") || strings.HasPrefix(lower, "error:") {
		severity = "ERROR"
	}
	return stamp, severity, message
}

func runtimeLogTimestamped(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 19 {
		return false
	}
	_, err := time.ParseInLocation("2006/01/02 15:04:05", trimmed[:19], time.Local)
	return err == nil
}

func cliHelpLine(message string) bool {
	line := strings.TrimSpace(message)
	lower := strings.ToLower(line)
	if strings.HasPrefix(line, "--") || (strings.HasPrefix(line, "-") && strings.Contains(line, "--")) {
		return true
	}
	for _, prefix := range []string{
		"usage:", "flags:", "global flags:", "command flags:", "available commands:",
		"flags for ", "filter options:", "listing options:", "copy options:", "sync options:",
		"vfs options:", "use \"rclone help", "use \"rclone [command] --help",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
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
		clean := SanitizeText(string(data), p.StateDir, p.RuntimeDir, p.ModuleDir, p.ProviderModuleDir, p.ManagedRcloneConfig, p.RcloneConfig)
		lines := strings.Split(strings.TrimSpace(clean), "\n")
		start := 0
		if len(lines) > 200 {
			start = len(lines) - 200
			truncated = true
		}
		help := make([]LogRecord, 0)
		localRecords := make([]LogRecord, 0, len(lines)-start)
		inHelpBurst := false
		for _, line := range lines[start:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			timestamped := runtimeLogTimestamped(line)
			stamp, severity, message := parseRuntimeLogLine(line, info.ModTime().UnixMilli())
			record := LogRecord{TimeUnixMS: stamp, Source: name, Severity: severity, Message: message}
			isHelp := cliHelpLine(message)
			if isHelp || (inHelpBurst && !timestamped && severity == "INFO") {
				help = append(help, record)
				inHelpBurst = true
				continue
			}
			if timestamped {
				inHelpBurst = false
			}
			localRecords = append(localRecords, record)
		}
		if len(help) > 8 {
			details := make([]string, 0, 40)
			for _, record := range help {
				if len(details) >= 40 {
					break
				}
				details = append(details, record.Message)
			}
			attached := false
			for i := len(localRecords) - 1; i >= 0; i-- {
				if localRecords[i].Severity == "ERROR" {
					localRecords[i].Suppressed = len(help)
					localRecords[i].Details = details
					attached = true
					break
				}
			}
			if !attached {
				localRecords = append(localRecords, LogRecord{TimeUnixMS: help[len(help)-1].TimeUnixMS, Source: name, Severity: "INFO", Message: "rclone command help suppressed after startup output", Suppressed: len(help), Details: details})
			}
			truncated = true
		} else {
			localRecords = append(localRecords, help...)
		}
		records = append(records, localRecords...)
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
