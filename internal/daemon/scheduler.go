package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/jobs"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
)

func RunJobScheduler(ctx context.Context, p paths.Paths, engine *control.Engine) {
	interval := jobs.SchedulerInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var mu sync.Mutex
	running := map[string]bool{}
	runDue := func() {
		names, err := jobs.Due(p, time.Now())
		if err != nil {
			return
		}
		for _, name := range names {
			mu.Lock()
			if running[name] {
				mu.Unlock()
				continue
			}
			running[name] = true
			mu.Unlock()
			go func(name string) {
				defer func() { mu.Lock(); delete(running, name); mu.Unlock() }()
				id := fmt.Sprintf("scheduled-%s-%d", name, time.Now().UnixNano())
				req := protocol.NewRequest(id, "job.run", protocol.ClassRun, map[string]any{"name": name, "trigger": "schedule"})
				_ = engine.Execute(ctx, req, nil)
			}(name)
		}
	}
	runDue()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runDue()
		}
	}
}
