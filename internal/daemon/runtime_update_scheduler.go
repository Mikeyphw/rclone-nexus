package daemon

import (
	"context"
	"fmt"
	"time"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
	"rclone-nexus/internal/runtimeupdate"
)

// RunRuntimeUpdateScheduler owns automatic update checks only. Checks may
// resolve/download/qualify/stage according to policy, but the default policy
// never hot-swaps the running runtime; activation is deferred to boot or an
// explicit action.
func RunRuntimeUpdateScheduler(ctx context.Context, p paths.Paths, engine *control.Engine) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	runDue := func(now time.Time) {
		policy, err := runtimeupdate.LoadPolicy(p)
		if err != nil || !policy.CheckAutomatically {
			return
		}
		snapshot, err := runtimeupdate.SnapshotOf(p)
		if err != nil {
			return
		}
		interval := time.Duration(policy.CheckIntervalMinutes) * time.Minute
		if snapshot.State.LastCheckUnixMS != 0 && now.Sub(time.UnixMilli(snapshot.State.LastCheckUnixMS)) < interval {
			return
		}
		id := fmt.Sprintf("runtime-update-%d", now.UnixNano())
		req := protocol.NewRequest(id, "runtime.update.check", protocol.ClassRun, map[string]any{})
		_ = engine.Execute(context.WithoutCancel(ctx), req, nil)
	}
	runDue(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			runDue(now)
		}
	}
}
