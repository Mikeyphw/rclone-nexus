package daemon

import (
	"context"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/paths"
)

// RunRuntimeUpdateScheduler is intentionally disabled in the static-runtime
// product line. Runtime replacement happens by building/flashing a new module
// ZIP with a different bundled system/bin/rclone.
func RunRuntimeUpdateScheduler(ctx context.Context, p paths.Paths, engine *control.Engine) {
	_ = ctx
	_ = p
	_ = engine
}
