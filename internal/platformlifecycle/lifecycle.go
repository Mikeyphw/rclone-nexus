package platformlifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"rclone-nexus/internal/mounts"
	ns "rclone-nexus/internal/namespace"
	"rclone-nexus/internal/paths"
)

type PurgeStatus struct {
	Armed bool `json:"armed"`
}

type CleanupResult struct {
	MountsStopped          int      `json:"mounts_stopped"`
	NamespaceBindsReleased int      `json:"namespace_binds_released"`
	PersistentPurged       bool     `json:"persistent_purged"`
	Errors                 []string `json:"errors,omitempty"`
}

func markerPath(p paths.Paths) string {
	return filepath.Join(p.Normalize().PlatformDir, "purge-on-uninstall")
}

func PurgeState(p paths.Paths) PurgeStatus {
	_, err := os.Stat(markerPath(p))
	return PurgeStatus{Armed: err == nil}
}
func SetPurge(p paths.Paths, armed bool) error {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return err
	}
	if !armed {
		err := os.Remove(markerPath(p))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(markerPath(p), []byte("armed\n"), 0o600)
}

func CleanupForUninstall(ctx context.Context, p paths.Paths) (CleanupResult, error) {
	p = p.Normalize()
	result := CleanupResult{}
	names, err := mounts.List(p)
	if err != nil && !os.IsNotExist(err) {
		result.Errors = append(result.Errors, err.Error())
	}
	for _, name := range names {
		if rep, e := ns.SuspendOwned(ctx, p, name); e == nil {
			result.NamespaceBindsReleased += rep.Released
		} else if !os.IsNotExist(e) {
			result.Errors = append(result.Errors, fmt.Sprintf("namespace %s: %v", name, e))
		}
		if _, e := mounts.Stop(ctx, p, name); e == nil {
			result.MountsStopped++
		} else {
			result.Errors = append(result.Errors, fmt.Sprintf("stop %s: %v", name, e))
		}
	}
	armed := PurgeState(p).Armed
	if armed {
		if err := safeRemoveState(p.StateDir); err != nil {
			return result, err
		}
		result.PersistentPurged = true
		return result, nil
	}
	for _, dir := range []string{p.RunDir, p.HealthDir} {
		_ = os.RemoveAll(dir)
	}
	_ = p.EnsureState()
	return result, nil
}

func safeRemoveState(path string) error {
	clean := filepath.Clean(path)
	if clean == "/" || clean == "." || clean == "/data" || clean == "/data/adb" || clean == "/data/adb/modules" {
		return fmt.Errorf("unsafe state removal path")
	}
	return os.RemoveAll(clean)
}

func MarshalStatus(p paths.Paths) []byte { b, _ := json.Marshal(PurgeState(p)); return b }
