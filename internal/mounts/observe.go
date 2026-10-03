package mounts

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rclone-nexus/internal/paths"
)

type RuntimeObservation struct {
	Name                 string `json:"name"`
	ProcessRecordPresent bool   `json:"process_record_present"`
	ProcessAlive         bool   `json:"process_alive"`
	ProcessManaged       bool   `json:"process_managed"`
	PID                  int    `json:"pid,omitempty"`
	MountAlive           bool   `json:"mount_alive"`
	MountFSType          string `json:"mount_fs_type,omitempty"`
	MountSource          string `json:"mount_source,omitempty"`
	OwnedMount           bool   `json:"owned_mount"`
}

type mountInfo struct {
	Mountpoint string
	FSType     string
	Source     string
}

func mountInfoPath() string {
	if path := os.Getenv("RNEXUS_MOUNTINFO_PATH"); path != "" {
		return path
	}
	return "/proc/self/mountinfo"
}

func decodeMountField(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func readMountInfo() ([]mountInfo, error) {
	file, err := os.Open(mountInfoPath())
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []mountInfo
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		dash := -1
		for i, field := range fields {
			if field == "-" {
				dash = i
				break
			}
		}
		if dash < 0 || dash+2 >= len(fields) || len(fields) < 5 {
			continue
		}
		out = append(out, mountInfo{Mountpoint: decodeMountField(fields[4]), FSType: fields[dash+1], Source: decodeMountField(fields[dash+2])})
	}
	return out, scanner.Err()
}

func ObserveRuntime(p paths.Paths, name string) (RuntimeObservation, error) {
	p = p.Normalize()
	cfg, err := Parse(p, name)
	if err != nil {
		return RuntimeObservation{}, err
	}
	obs := RuntimeObservation{Name: name}
	record, recordErr := readProcessRecord(p, name)
	recordOwnsMount := false
	if recordErr == nil {
		obs.ProcessRecordPresent = true
		obs.PID = record.PID
		identityErr := validateProcessRecord(record)
		if identityErr == nil {
			obs.ProcessAlive = true
			obs.ProcessManaged = true
		}
		// A process record is ownership evidence only when it was created for
		// the current mount definition and either still names that exact
		// process or the recorded process is now dead. A live PID with a
		// different /proc start time is PID reuse and must never authorize an
		// unmount of whatever now occupies the configured mountpoint.
		if digest, digestErr := digestConfigs([]Config{cfg}); digestErr == nil && digest == record.ConfigDigest {
			if identityErr == nil || !alive(record.PID) {
				recordOwnsMount = true
			}
		}
	}
	infos, err := readMountInfo()
	if err == nil {
		target := filepath.Clean(cfg.Mountpoint)
		for _, info := range infos {
			if filepath.Clean(info.Mountpoint) != target {
				continue
			}
			obs.MountAlive = true
			obs.MountFSType = info.FSType
			obs.MountSource = info.Source
			break
		}
	}
	if obs.MountAlive && recordOwnsMount {
		fs := strings.ToLower(obs.MountFSType)
		source := strings.ToLower(obs.MountSource)
		obs.OwnedMount = strings.HasPrefix(fs, "fuse") || strings.Contains(fs, "rclone") || strings.Contains(source, "rclone")
	}
	return obs, nil
}

func ReconcileStart(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	p = p.Normalize()
	var result ActionResult
	err := withMountLock(p, name, func() error {
		cfg, err := Parse(p, name)
		if err != nil {
			return err
		}
		if DesiredState(p, cfg) != DesiredRunning {
			result = ActionResult{Name: name, State: "stopped", Noop: true}
			return nil
		}
		result, err = startUnlocked(ctx, p, cfg, false)
		return err
	})
	return result, err
}

func ReconcileStop(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	p = p.Normalize()
	var result ActionResult
	err := withMountLock(p, name, func() error {
		cfg, err := Parse(p, name)
		if err != nil {
			return err
		}
		if DesiredState(p, cfg) != DesiredStopped {
			result = ActionResult{Name: name, State: "running", Noop: true}
			return nil
		}
		result, err = stopUnlocked(ctx, p, cfg)
		return err
	})
	return result, err
}

func RepairStale(ctx context.Context, p paths.Paths, name string) (ActionResult, error) {
	p = p.Normalize()
	var result ActionResult
	err := withMountLock(p, name, func() error {
		cfg, err := Parse(p, name)
		if err != nil {
			return err
		}
		obs, err := ObserveRuntime(p, name)
		if err != nil {
			return err
		}
		if obs.ProcessAlive && obs.MountAlive {
			return fmt.Errorf("%s: runtime is not stale", name)
		}
		if obs.ProcessAlive && !obs.MountAlive {
			result, err = stopUnlocked(ctx, p, cfg)
			return err
		}
		if obs.MountAlive {
			if !obs.OwnedMount {
				return fmt.Errorf("%s: stale mount is not provably Nexus-owned", name)
			}
			unmount(p, cfg.Mountpoint)
		}
		removeProcessRecord(p, name)
		result = ActionResult{Name: name, State: "stale-cleaned"}
		return nil
	})
	return result, err
}
