package namespace

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type MountInfo struct {
	ID           int      `json:"id"`
	ParentID     int      `json:"parent_id"`
	MajorMinor   string   `json:"major_minor"`
	Root         string   `json:"root"`
	Mountpoint   string   `json:"mountpoint"`
	Options      []string `json:"options,omitempty"`
	Optional     []string `json:"optional,omitempty"`
	FSType       string   `json:"fs_type"`
	Source       string   `json:"-"`
	SuperOptions []string `json:"super_options,omitempty"`
}

type MountSignature struct {
	MountID    int    `json:"mount_id"`
	MajorMinor string `json:"major_minor"`
	Root       string `json:"root"`
	FSType     string `json:"fs_type"`
}

func (m MountInfo) Signature() MountSignature {
	return MountSignature{MountID: m.ID, MajorMinor: m.MajorMinor, Root: m.Root, FSType: m.FSType}
}

func decodeMountField(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func ParseMountInfo(r io.Reader) ([]MountInfo, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var out []MountInfo
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil, fmt.Errorf("mountinfo line %d is too short", lineNo)
		}
		dash := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				dash = i
				break
			}
		}
		if dash < 6 || dash+3 > len(fields)-1 {
			return nil, fmt.Errorf("mountinfo line %d has no valid separator", lineNo)
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("mountinfo line %d has invalid mount id", lineNo)
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil || parent < 0 {
			return nil, fmt.Errorf("mountinfo line %d has invalid parent id", lineNo)
		}
		if !strings.Contains(fields[2], ":") {
			return nil, fmt.Errorf("mountinfo line %d has invalid device", lineNo)
		}
		item := MountInfo{
			ID: id, ParentID: parent, MajorMinor: fields[2], Root: decodeMountField(fields[3]),
			Mountpoint: decodeMountField(fields[4]), Options: splitCSV(fields[5]),
			Optional: append([]string(nil), fields[6:dash]...), FSType: fields[dash+1],
			Source: decodeMountField(fields[dash+2]), SuperOptions: splitCSV(fields[dash+3]),
		}
		out = append(out, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func findMount(infos []MountInfo, mountpoint string) (MountInfo, bool) {
	for _, info := range infos {
		if info.Mountpoint == mountpoint {
			return info, true
		}
	}
	return MountInfo{}, false
}
