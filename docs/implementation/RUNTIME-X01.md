# RUNTIME-X01 — scheduled jobs and local-only RC telemetry

Position: 7/16.

Owns typed scheduled `sync`/`copy`/`check` jobs, persistent next-run state, policy gating, cancellation/progress journaling, destructive-sync proof, and authenticated loopback-only rclone RC metrics. No arbitrary argv or generic RC tunnel is exposed. Credentials remain root-owned backend state.
