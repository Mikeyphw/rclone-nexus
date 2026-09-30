# LIFE-X01 — transactional configuration + authoritative per-mount lifecycle

Status: **implemented; pending CORE-G1 gate qualification**.

Campaign position: **2/16**; gate-window position **2/3**. This overlay merges
former CFG-X01 + LIFE-X01 exactly as defined by the compressed roadmap.

## Delivered promise ledger

### Configuration registry v2

- Treats v0.1 literal `mounts.d/*.conf` files as a lossless import source until
  v2 is first published; legacy parsing remains data-only with no shell eval.
- Adds one normalized typed mount model with explicit VFS/cache/runtime fields.
- Validates mount names/remotes, absolute/protected mountpoints, VFS cache mode,
  sizes/durations, log levels, relative `args_file` containment, duplicate names
  and overlapping/nested destinations.
- Adds credential-free `config.snapshot` plus full-registry `config.preview`.
- Binds `config.apply` to both the previewed revision and candidate SHA-256
  digest; stale revisions and digest mismatches are stable machine errors.
- Publishes v2 registry JSON with same-directory fsync + atomic rename.
- Adds a previous-known-good registry slot and revisioned rollback operations.
- Adds diff planning so only changed mounts can receive lifecycle work.
- Keeps `args_file` out of the generic typed mutation surface and public export;
  existing root-local values are preserved by mount name across v2 mutations.
- Supports advanced fields: VFS cache max size/age, directory cache time, poll
  interval and read-only mode in addition to the v0.1 fields.

### Authoritative lifecycle

- Keeps start/stop/restart/status/list/reconcile independently addressable per
  mount, now serialized with cross-process `flock` locks.
- Replaces integer PID files with private JSON process records containing PID +
  Linux `/proc` start-time identity + config digest.
- Treats malformed/dead/reused PID records as stale metadata and never signals
  the referenced process unless identity revalidation succeeds.
- Revalidates process identity immediately before bounded SIGKILL fallback.
- Stores persistent desired state separately from observed process state.
- Keeps logs/cache/process state per mount and removes any global kill-all need.
- Reconcile records per-mount failures and continues healthy mounts instead of
  aborting the whole set.
- Config publication may restart/start/stop only affected mounts; lifecycle
  state is re-read after acquiring each mount lock to avoid stale-plan races.

## Deliberately deferred to LIFE-X02

This overlay does **not** claim readiness dependency graphs, network/provider
late-availability retry policy, richer degraded/auth/offline states, stale FUSE
recovery, exponential backoff/restart budgets, or the persistent operation
journal. Those remain position 3/16.

## Targeted validation owned by this overlay

- v0.1 migration and private `args_file` preservation/non-disclosure;
- invalid typed values and overlapping mountpoint rejection;
- atomic-write pre-rename failure preserving the published registry;
- revision and candidate-digest stale-proof rejection;
- previous-known-good rollback at a new revision;
- fake-rclone lifecycle integration;
- PID reuse simulation using the current process with deliberately mismatched
  `/proc` start-time identity (proving no unrelated process is signalled);
- concurrent starts and mixed start/stop races;
- one-mount reconcile failure isolation;
- affected-only config restart with unaffected PID stability;
- existing Python CLI/protocol compatibility tests;
- shell/module/package contracts and reproducible Android arm64 backend build.
