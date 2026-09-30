package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

type Result struct {
	Available bool     `json:"available"`
	OK        bool     `json:"ok"`
	Checked   int      `json:"checked"`
	Issues    []string `json:"issues,omitempty"`
}

func Load(moduleDir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(moduleDir, "integrity.manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	if m.SchemaVersion != 1 {
		return Manifest{}, fmt.Errorf("unsupported integrity manifest schema")
	}
	return m, nil
}

func Verify(moduleDir string) Result {
	m, err := Load(moduleDir)
	if err != nil {
		return Result{Available: false, OK: false, Issues: []string{"manifest_unavailable"}}
	}
	result := Result{Available: true, OK: true}
	entries := append([]Entry(nil), m.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	for _, e := range entries {
		clean := filepath.Clean(e.Path)
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || len(clean) >= 3 && clean[:3] == "../" {
			result.OK = false
			result.Issues = append(result.Issues, "invalid_manifest_path:"+e.Path)
			continue
		}
		path := filepath.Join(moduleDir, clean)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			result.OK = false
			result.Issues = append(result.Issues, "missing:"+e.Path)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			result.OK = false
			result.Issues = append(result.Issues, "unreadable:"+e.Path)
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != e.SHA256 {
			result.OK = false
			result.Issues = append(result.Issues, "hash_mismatch:"+e.Path)
		}
		if uint32(info.Mode().Perm()) != e.Mode {
			result.OK = false
			result.Issues = append(result.Issues, "mode_mismatch:"+e.Path)
		}
		if info.Size() != e.Size {
			result.OK = false
			result.Issues = append(result.Issues, "size_mismatch:"+e.Path)
		}
		result.Checked++
	}
	return result
}
