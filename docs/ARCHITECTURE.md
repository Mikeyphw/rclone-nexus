# Architecture

Rclone Nexus has one supported runtime relationship: the installed Nexus module
owns a single rclone-family executable at `system/bin/rclone`. The persistent
managed configuration remains under `/data/adb/rclone-nexus`; a NewFuture
provider may be observed for migration but cannot become execution authority.

Canonical runtime direction:

```text
module ZIP build
  RNEXUS_RCLONE_PREBUILT=/path/to/rclone-or-bclone
      |
      v
/data/adb/modules/rclone_nexus/system/bin/rclone
      |
      v
internal/runtimeauth (single static runtime/config resolver)
      |
      +--> bundled executable: <module>/system/bin/rclone
      +--> managed config: /data/adb/rclone-nexus/config/rclone/rclone.conf
      |
      v
racctl/control + mount lifecycle + supervisor/jobs/diagnostics/WebUI
      |
      v
managed mount instances
```

PATH, runtime environment overrides, provider binaries, activation state,
runtime candidate/source/update stores, and generic `RCLONE_CONFIG` are outside
that authority. Replacing the runtime means packaging and flashing a new module.

## Ownership boundaries

Rclone Nexus owns executable/config selection, persistent lifecycle state, logs and
per-mount VFS cache. Generic `PATH`, runtime override environment variables, provider
module layouts and `RCLONE_CONFIG` cannot redefine runtime authority.

Normal runtime/control paths MUST NOT edit files inside `/data/adb/modules/rclone` or
execute that provider's boot/service scripts. The provider is observational migration
input only. Nexus-owned FUSE-helper handling remains a separate authority from the
packaged rclone-family executable.

Persistent Rclone Nexus state is rooted at `/data/adb/rclone-nexus`. Managed
configuration, mount definitions, process state, logs and per-mount VFS cache therefore
live outside the replaceable module directory. Runtime executable bytes are intentionally
inside the replaceable Nexus module because module replacement is the update mechanism.

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
required, canonical runtime authority/executable/config, FUSE availability, target storage,
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
boot first requires the canonical runtime authority to be operational; the native supervisor then owns runtime/storage/network waiting and continues
self-healing after boot. Legacy-provider presence is observational and may inform migration state, but never redirects the static runtime resolver.

Every typed `run`/`reconcile` operation enters the root-owned `operations/`
journal before mutation. Progress events and terminal state are persisted with
recursive secret redaction and bounded payloads. On reopen, records whose owner
PID/start identity disappeared are reconciled to `INTERRUPTED`; UI/CLI clients
query this backend truth instead of keeping browser-local operation state. Only
operations explicitly marked cancellable in capabilities can be cancelled;
configuration publish/rollback is intentionally non-cancellable once entered.

## Android namespace visibility authority (ANDROID-X01)

Rclone mounts are no longer treated as app-visible merely because they appear
in the service/root mount table. Nexus inventories distinct mount namespaces
from `/proc/<pid>/ns/mnt`, parses each readable `mountinfo`, classifies observed
service/root/shell/zygote/Termux/app membership, and derives Android user IDs
from UIDs. Visibility is therefore an observed per-namespace/per-user fact.

The public visibility claim is deliberately bounded. `service_only`,
`partial_app_visibility`, `service_visible_no_app_evidence`, and
`observed_all_discovered_app_namespaces` describe only evidence Nexus actually
saw. An unreadable app namespace prevents qualification rather than being
silently counted as visible.

App visibility is opt-in. The `same-path-bind-v1` adapter is eligible only when:

- the configured source mount is live and provably Nexus-owned;
- the mountpoint is inside a recognized Android storage boundary;
- Nexus runs as root with effective `CAP_SYS_ADMIN`;
- app-relevant target namespaces are observed without topology truncation and
  have stable namespace identities.

Before `setns`, the target PID's namespace symlink is revalidated against the
previewed namespace ID. Inside the target namespace Nexus rejects symlink or
non-directory destinations. Each successful bind records the target mount ID
plus device/root/fs signature under `/data/adb/rclone-nexus/namespace`.
Rollback/unwind compares that exact signature before `MNT_DETACH`; a changed or
unowned mount is left untouched and reported as an ownership mismatch.

Multi-target apply is transactional: if a later bind, cancellation, persistence
step, or verification step fails, only binds created by that transaction are
released and the exact prior persistent visibility state is restored. The persistent desired state survives daemon/reboot churn. `racd`
reconciles newly created zygote/app namespaces, prunes vanished namespace
markers, and suspends owned app binds before source-mount stop/stale repair so
old FUSE instances are not kept alive invisibly.

## Static bundled runtime authority

The installed module owns `system/bin/rclone`. `internal/runtimeauth` resolves that
single executable and the Nexus-owned managed config. It does not load activation state
or select from PATH, a provider module, a mutable candidate store, a source registry, or
an update policy.

`scripts/dev/package_module.py` accepts `RNEXUS_RCLONE_PREBUILT` (or a manually
placed `module/system/bin/rclone`), writes the executable into the flashable ZIP and
records it in `integrity.manifest.json`. Boot requires `runtime.status` to be operational
before reconciliation and performs no activation recovery or staged update promotion.

The old runtime candidate/source/activation/update Go packages have been retired from
the compiled tree. Historical campaign documents remain as provenance only. NewFuture,
official rclone, or bclone may supply build-time bytes; none is live runtime authority
outside the bundled module path.

The WebUI Runtime page is status-only and consumes `runtime.status`. The supported CLI
surface is `runtime status|executable|config`; runtime replacement is a package operation.

## Standalone provider migration authority

MIGRATE-X01 adds `$STATE/migration/state-v1.json` as the durable authority for
handoff from the external `rclone` module. The provider module is strictly a
read-only migration input. Provider processes are identified by `/proc` ownership,
its config is copied as opaque bytes only after semantic validation by the active
managed runtime, and provider sync/copy files are parsed as data without shell
evaluation. Reviewed mounts/jobs enter Nexus disabled.

The provider-to-Nexus switch requires a second preview proof after an explicit
external/root-manager provider disable. Finalization enables only reviewed Nexus
definitions. Interrupted transactions restore pre-migration Nexus state; a legacy
provider that becomes active after completion puts migration into `CONFLICT` and
blocks daemon authority. This prevents silent dual control.

## Runtime status projection

`runtime.status` is the only typed runtime RPC. It projects the bundled executable,
managed configuration, operational state, legacy-provider observation and static-runtime
issues. Clients cannot request runtime import, source resolution, activation, rollback or
update because those operations are not registered by the production control engine.
