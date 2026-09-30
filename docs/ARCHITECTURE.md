# Architecture

Rclone Nexus is intentionally a **consumer** of NewFuture's
`rclone-fuse3-magisk` module rather than a fork or replacement.

Dependency direction:

```text
rclone upstream
      |
      v
NewFuture rclone-fuse3-magisk (module id: rclone)
      |  rclone + FUSE runtime + canonical rclone.conf
      v
Rclone Nexus (module id: rclone_nexus)
      |  lifecycle + Android integration + diagnostics
      v
managed mount instances
```

## Ownership boundaries

Rclone Nexus MUST NOT:

- bundle its own `rclone`, `fusermount3`, or libfuse runtime;
- edit files inside `/data/adb/modules/rclone`;
- replace NewFuture's boot/service scripts;
- assume module updates preserve files stored under its own module directory.

Persistent Rclone Nexus state is therefore rooted at `/data/adb/rclone-nexus`.
The initial implementation owns mount definitions, PID/runtime state, logs and
per-mount VFS cache there.

## Configuration and lifecycle authority (LIFE-X01)

`mounts.d/<name>.conf` is now a **v0.1 import source**, not the long-term
transaction authority. If no v2 registry exists, Nexus parses those literal
`key=value` files without `source`/`eval`, normalizes them, and reports
`source=legacy-v0.1`. The first successful v2 apply atomically publishes:

```text
/data/adb/rclone-nexus/config/registry-v2.json
/data/adb/rclone-nexus/config/previous-v2.json
```

The active registry is one revisioned/digested document so a multi-mount edit
has one publication point. Mutation is `candidate -> validate -> preview ->
revision/digest-bound apply`; stale revisions and mismatched preview digests
fail before publication. The previous-known-good slot is itself a valid v2
registry and rollback creates a new revision rather than rewinding history.

The normalized mount model supports `enabled`, `remote`, `mountpoint`,
`vfs_cache_mode`, `vfs_cache_max_size`, `vfs_cache_max_age`, `dir_cache_time`,
`poll_interval`, `allow_other`, `read_only`, and `log_level`. Mountpoints are
checked for duplicates/nesting and protected Nexus/provider-state overlap.

`args_file` remains a root-local compatibility field. Legacy/private registry
persistence may contain its path, but generic typed config export/mutation only
reveals `has_args_file`; it never exposes the path or turns file contents into
a WebUI argv/shell surface.

Per-mount lifecycle is serialized across processes with private file locks. A
managed process record stores PID plus `/proc/<pid>/stat` start-time identity,
so PID reuse cannot cause Nexus to signal an unrelated process. Stop uses TERM,
a bounded wait, and revalidates identity immediately before any KILL fallback.
Each mount retains its own log, cache, process record and failure domain;
reconcile continues after one mount fails.

Persistent desired state lives separately from observed runtime state under
`/data/adb/rclone-nexus/desired`. Explicit stop therefore remains stopped across
reconcile instead of being mistaken for a crashed enabled mount. Config diffs
plan lifecycle work only for changed mounts and re-evaluate desired/observed
state after acquiring each mount lock to avoid racing explicit lifecycle actions.

## Native control-plane boundary (CORE-X01)

`racctl` is now the native authority shared by compatibility CLI launchers and
`racd`. Machine clients use a versioned, bounded, redacted JSON protocol over a
root-owned Unix socket. The registry exposes only fixed typed operations; there
is no arbitrary shell/argv RPC. Provider discovery reports readiness facts but
never provider/config/state filesystem paths.

CORE-X01 established the protocol/daemon authority. LIFE-X01 owns the
transactional configuration registry and authoritative process lifecycle.

## Readiness, recovery and operation truth (LIFE-X02)

`racd` owns a continuous supervisor. It evaluates a readiness graph for each
desired mount covering private persistent state, Android boot completion when
required, provider module/binary/FUSE/config availability, target storage,
required network class, and optional bounded remote reachability probes.
Readiness failures are explicit waiting reasons rather than fixed shell sleeps.

Runtime health distinguishes the managed process identity from the actual mount
table. The public states are `RUNNING`, `DEGRADED`, `REMOTE_OFFLINE`,
`MOUNT_STALE`, `AUTH_ERROR`, `FUSE_ERROR`, `RETRYING`, and `STOPPED`. A live VFS
mount is not destroyed merely because its remote/network is unavailable. Stale
FUSE cleanup is allowed only when Nexus has both a stale process record and a
matching FUSE/rclone mountpoint, so an unrelated mount is never unmounted as a
recovery shortcut.

Recovery uses persistent per-mount attempt counters, exponential backoff and a
bounded restart budget. Health/retry state lives under `health/` and therefore
survives daemon/browser restarts. `service.sh` starts the daemon directly; the
native supervisor owns boot/provider/storage/network waiting and continues
self-healing after boot.

Every typed `run`/`reconcile` operation enters the root-owned `operations/`
journal before mutation. Progress events and terminal state are persisted with
recursive secret redaction and bounded payloads. On reopen, records whose owner
PID/start identity disappeared are reconciled to `INTERRUPTED`; UI/CLI clients
query this backend truth instead of keeping browser-local operation state. Only
operations explicitly marked cancellable in capabilities can be cancelled;
configuration publish/rollback is intentionally non-cancellable once entered.

## Planned boundaries

Namespace propagation, richer health classification, network/battery policy, RC
metrics, and the WebUI remain later milestones. Those features extend the same
control plane rather than fork the provider module.
