# POLICY-X01 — resource policy engine + VFS/cache governor

Full-plan position **6/16**. This overlay merges the former POLICY-X01 and
VFS-X01 boundaries because both govern mount resources and are consumed by the
same supervisor/config authority.

## Delivered

- Typed network modes: `any`, `wifi`, `unmetered`, `offline-allowed`.
- Charging-only, minimum battery, minimum cache free-space, boot-settle and
  network-settle constraints.
- Android/Linux connectivity, power and storage observation with environment
  fixtures for deterministic tests; expensive metering observation is queried
  only when an `unmetered` policy requires it.
- Policy truth is orthogonal to lifecycle truth. A running valid VFS mount is
  retained when policy becomes blocked, while stopped/recovering mounts wait
  without consuming restart budget.
- Persistent network transition time under the private policy state directory.
- Named VFS profiles: `streaming`, `balanced`, `offline`, `minimal`, `custom`.
  Existing mounts normalize to `custom`; named profiles are explicit opt-in.
- Advisory RAM/free-space recommendation without automatic config mutation.
- Effective profile expansion is used for the actual rclone mount argv and
  mount-start preview.
- Per-mount owned cache pressure/status with configured max/high/low/min-free
  limits.
- Ownership-bounded oldest-first prune to low-water, performed automatically
  only while a mount is stopped.
- Typed preview/run operations for cache prune, clear and full reset (`forget`).
  Manual mutation requires the mount to be stopped and never exposes cache file
  paths through the typed API.
- Policy-only config changes do not restart mounts; VFS option/profile changes
  remain restart-sensitive.
- `rclone-nexus policy`, `rclone-nexus vfs`, and `rclone-nexus cache ...` CLI
  surfaces plus typed backend operations for future WebUI use.

## Validation boundary

`./devtoolw policy-x01` owns the targeted boundary: policy transition matrix,
network-settle persistence, fail-closed unknown metering, VFS profile golden
expansion, custom-profile non-mutation, low-space/cache high-low behavior,
cache ownership/symlink safety, cancellation, lifecycle policy transition and
compatibility tests.

## Validation determinism hardening

POLICY-X01 validation keeps restart-budget tests independent of host process-reaping and wall-clock timing. The supervisor failure fixture uses an executable with a deliberately unavailable interpreter so `cmd.Start` fails before a child exists; the test then advances persisted retry eligibility explicitly rather than sleeping. This preserves the restart-budget/backoff contract while avoiding Android/Termux `/proc` timing races.
