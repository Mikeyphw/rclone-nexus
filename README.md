# Rclone Nexus


## SOURCE-G1 behavioral supply-chain seal

SOURCE-G1 probes Android NDK host executability before requiring the optional
local SOURCE-X02 build proof; an installed but host-incompatible compiler is
reported as an environment limitation, while any runnable compiler must complete
the real build + production verifier successfully.

SOURCE-G1 proves the source/update stack with real external artifacts rather than
static presence checks. Its rooted-device proof activates a production-qualified
historical NewFuture release, stages current latest through the normal update
manager, proves the live mount executable changed to the staged digest, and proves
one-click rollback restores the historical bytes. An already-current device runtime
therefore cannot create a false failure or a fake transition.

## UPDATE-X01 safe runtime update manager

UPDATE-X01 adds a persisted update policy/state machine above the deterministic source registry and immutable runtime store. By default Nexus checks the `bclone` source automatically, securely downloads or consumes the resolved artifact, qualifies it, and stages only passing bytes. It does **not** hot-swap the live runtime: activation is deferred to the next reboot or an explicit CLI/WebUI action, with transactional rollback and bounded protected runtime-history GC. Release ZIPs are hash-bound before safe extraction; traversal, symlink, malformed, interrupted, mismatched and ambiguous artifacts fail closed.

## SOURCE-X01 deterministic runtime sources

SOURCE-X01 adds a Nexus-owned runtime source registry in front of the immutable runtime store. Built-in bclone/rclone/NewFuture sources, arbitrary GitHub repositories, explicit URLs, local binaries, and local source-build outputs now share typed latest/pinned/manual channel semantics. Mutable GitHub selections are persisted as immutable repository/release/commit/asset identities before qualification; `racctl runtime source import-resolution` feeds only that saved resolution into the existing runtime qualifier.

SOURCE-X02 adds a reproducible Android arm64 source-build path for bclone/rclone-compatible repositories. CI resolves mutable refs once to an exact commit, builds with pinned Go/NDK Android parameters, emits hash-bound provenance, verifies the actual Android AArch64 ELF, and routes accepted output back through the SOURCE-X01 resolution and RUNTIME-X02 qualification authorities.

A non-invasive Android root module for managed or externally supplied rclone runtimes.
It adds a native typed control plane, transactional per-mount configuration,
authoritative lifecycle management, boot reconciliation, evidence-backed Android
mount-namespace visibility and diagnostics. The RUNTIME-STANDALONE campaign makes
Nexus authoritative for runtime/config selection in managed mode while retaining
NewFuture/rclone-fuse3-magisk as an explicit noncanonical compatibility/migration path.

> Android app visibility is never claimed universally: Nexus reports exactly
> which discovered namespaces/users can observe each mount and fails closed when
> no qualified propagation strategy exists.

## Design rules

- Legacy/external provider module id: `rclone`
- Nexus module id: `rclone_nexus`
- Persistent state: `/data/adb/rclone-nexus`
- Managed runtime root: `/data/adb/rclone-nexus/runtime`
- Immutable runtime candidate store: `/data/adb/rclone-nexus/runtimes`
- Managed rclone config: `/data/adb/rclone-nexus/config/rclone/rclone.conf`
- No mutation of `/data/adb/modules/rclone`
- Provider/PATH lookup is compatibility lowering only; it is never managed-mode authority
- Configuration is parsed as data; no `eval`/sourcing of mount definitions

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

Active implementation campaign: [`docs/campaign/RUNTIME_STANDALONE_ROADMAP.md`](docs/campaign/RUNTIME_STANDALONE_ROADMAP.md) — 11 positions with RUNTIME-GRAND-G1 still open. [`docs/ROADMAP.md`](docs/ROADMAP.md) is the historical 16-position predecessor roadmap and is retained as evidence only.


## Runtime Manager

The canonical runtime/source/update/migration projection is available to both CLI and WebUI:

```sh
racctl runtime manager
racctl runtime source list
racctl runtime update status
racctl migration status
```

The Runtime page consumes the same typed control operations. Action availability is backend-owned: unqualified candidates cannot activate, terminal update failures do not expose retry, rollback requires a present qualified previous runtime, and migration actions follow durable provider/migration state.

## Platform diagnostics and root-manager lifecycle

Rclone Nexus now exposes platform-native diagnostics and upgrade safety:

```sh
rclone-nexus doctor
rclone-nexus doctor --bundle
rclone-nexus platform status
rclone-nexus platform root-manager
rclone-nexus platform verify-integrity
rclone-nexus platform purge-on-uninstall status
```

Persistent state is preserved on uninstall by default. To intentionally delete
it during the *next* uninstall, explicitly arm the one-shot purge first:

```sh
rclone-nexus platform purge-on-uninstall enable
```

The module supports Magisk, KernelSU/KernelSU Next and APatch through a common
capability layer. Unknown managers using the standard `/data/adb/modules`
layout receive only conservative capabilities. The NewFuture provider module is
never modified.

## Development with Devtool

The repository is configured around Devtool-native target-local jobs and
workflows. The checked-in `devtoolw` is generated by Devtool and is only a
frontend over those canonical workflows.

```sh
./devtoolw validate
./devtoolw test
./devtoolw build
./devtoolw install
./devtoolw install-stack
./devtoolw install-verify
./devtoolw core-g1
./devtoolw runtime-standalone-x01
./devtoolw runtime-standalone-x02
./devtoolw runtime-standalone-x03
./devtoolw runtime-standalone-g1-source
./devtoolw runtime-standalone-g1-device
./devtoolw runtime-standalone-g1
./devtoolw source-x01
./devtoolw ux-x01
./devtoolw device-smoke
./devtoolw android-g1
./devtoolw platform-g1
./devtoolw release
```

The target is `rclone_nexus` and is marked `native-termux`; it has no Gradle or
Android SDK dependency. CORE-X01 added Go-native `racctl`/`racd`; LIFE-X01 adds
the revisioned configuration registry and process-identity-safe lifecycle;
LIFE-X02 adds readiness-aware continuous supervision, persistent health/retry
truth and a crash-recoverable operation journal. CORE-G1 qualifies that entire
control/lifecycle window and hardens unmount ownership plus reconcile desired-
state races. ANDROID-X01 adds namespace topology/visibility authority,
transactional Nexus-owned same-path bind propagation and multi-user evidence.
POLICY-X01 adds explicit network/power/storage policy and owned VFS cache
governance; RUNTIME-X01 adds typed scheduled jobs and authenticated loopback-only
RC metrics. ANDROID-G1 qualifies that complete Android/runtime window, including
namespace churn, policy process-idempotence, cache ownership, scheduler restart
deduplication, destructive-sync preview/approval, and RC credential isolation.
PLATFORM-X01 adds diagnostics, support bundles, root-manager capabilities and
upgrade/uninstall safety; PLATFORM-G1 qualifies that platform boundary and
hardens fail-closed integrity-before-migration plus private diagnostics ownership.
The arm64 Android backend build remains deterministic.

## Build output

```text
dist/rclone-nexus-v0.1.0.zip
```

The package script creates a deterministic root-module zip with the contents of
`module/` at the archive root and injects the reproducibly-built arm64
`system/bin/racctl` backend.

## Device setup

RUNTIME-STANDALONE X02 adds immutable runtime candidate import/qualification. RUNTIME-STANDALONE X03 adds the durable activation state and transactional activate/rollback/recovery authority. RUNTIME-G1 adversarially qualifies that chain on rooted Android, including providerless managed operation, distinct-byte activation/rollback, and a Linux-arm64 execute-but-fail-FUSE negative candidate. Candidate intake remains `racctl runtime import`; once activation state exists, managed execution resolves the active runtime ID and digest from the immutable store while `runtime/active/bin/rclone` is only a compatibility projection. A legacy NewFuture provider is detected as `migration-required` unless external compatibility mode is explicitly selected. On a fresh/legacy setup,
`mounts.d/*.conf` remains the v0.1 import format. LIFE-X01 reads those files
losslessly until the first v2 configuration apply, after which
`config/registry-v2.json` is authoritative and the old files are retained only
as an untouched import source.

Import and qualify a runtime candidate (qualification fails closed unless the real device contract is satisfied):

```sh
su
racctl runtime import --source local-file --engine rclone --path /sdcard/Download/rclone
racctl runtime list
racctl runtime inspect <runtime-id>
racctl runtime test <runtime-id>
racctl runtime activation-status
racctl runtime activate <runtime-id>
racctl runtime rollback
racctl runtime recover

# SOURCE-X01 registry + immutable resolution
racctl runtime source list
racctl runtime source resolve bclone
racctl runtime source resolutions
# import only a persisted immutable resolution (release assets may still require SOURCE-X02 Android builds)
racctl runtime source import-resolution <resolution-id>

# safe update manager: check/qualify/stage without default hot swap
racctl runtime update status
racctl runtime update check
racctl runtime update activate
racctl runtime update rollback
racctl runtime update policy
```

Create a legacy/import mount definition:

```sh
su
mkdir -p /data/adb/rclone-nexus/mounts.d
cp /path/to/drive.conf /data/adb/rclone-nexus/mounts.d/drive.conf
rclone-mountctl start drive
rclone-mountctl status drive
rclone-doctor
```

Example definition:

```ini
enabled=true
remote=gdrive:
mountpoint=/storage/emulated/0/Rclone/Drive
vfs_cache_mode=full
vfs_cache_max_size=8GiB
vfs_cache_max_age=24h
dir_cache_time=1h
poll_interval=15s
allow_other=true
read_only=false
log_level=INFO
require_network=true
probe_remote=false
```

The native supervisor starts with `racd` and continuously reconciles enabled
mounts. Boot completion, provider/FUSE/config readiness, target storage and
required network/remote readiness are explicit conditions with bounded retry;
there is no fixed boot sleep in `service.sh`. Explicit `start`/`stop` writes a
separate desired-state record, so observed process state and requested state are
not conflated. A valid VFS mount is retained through network/remote outages.

The v2 machine API exposes `config.snapshot`, `config.preview`, `config.apply`,
`config.previous`, `config.rollback.preview`, and `config.rollback`. Apply is
bound to both the previewed revision and candidate digest. `args_file` contents
and paths never cross that generic typed mutation surface; existing root-local
values are preserved server-side during v2 changes.

## Android namespace visibility

Inspection is always read-only:

```sh
su -c 'racctl namespace inspect drive'
su -c 'racctl namespace preview drive'
```

`namespace apply` is opt-in. It persists app-visible intent and performs only
qualified same-path binds into observed Android shell/zygote/app namespaces.
Every bind has a private ownership marker; rollback will not unmount a target
whose mount identity no longer matches that marker.

```sh
su -c 'racctl namespace apply drive'
su -c 'racctl namespace inspect drive'
su -c 'racctl namespace rollback-preview drive'
su -c 'racctl namespace rollback drive'
```

After apply, `racd` reconciles the persisted visibility intent when zygote/app
namespaces are recreated. Root/service visibility continues to work even when
ordinary-app propagation is unsupported.

The optional Devtool device evidence surface is:

```sh
./devtoolw device-smoke
```

A privileged execution destination can opt into a reversible propagation smoke
by setting `RNEXUS_DEVICE_SMOKE_MOUNT=<name>` and
`RNEXUS_DEVICE_SMOKE_APPLY=1`. The smoke script itself never invokes `adb` or
`su`; privilege/transport stays owned by Devtool's execution destination.

## Current commands

```text
rclone-nexus status|reconcile|paths|config|version|capabilities|provider|health|namespace|operations|operation|cancel|doctor
rclone-mountctl list|status|start|stop|restart|reconcile
rclone-doctor
racctl version|capabilities|rpc|racd|namespace
```


## Resource policy and VFS

POLICY-X01 adds typed network/power/storage policy, named VFS profiles, advisory recommendations, and ownership-bounded cache status/prune/clear/forget operations.


## Scheduled jobs and RC telemetry

Rclone Nexus owns typed `sync`, `copy`, and read-only `check` jobs under `/data/adb/rclone-nexus/jobs`. Job definitions cannot supply arbitrary rclone argv or on-the-fly backend syntax. Destructive `sync` jobs are previewable but cannot be published until `confirm_destructive=true` is explicit. The daemon persists `next_run_unix_ms` before scheduled launch and uses a per-job lock so restart/reconnect cannot duplicate the same due run. Charging, network, and minimum-battery policy gates reuse the same policy engine as mounts.

Every Nexus-started mount receives a random authenticated rclone RC endpoint bound only to `127.0.0.1`. Credentials remain in root-owned runtime state under `/data/adb/rclone-nexus/run/rc`; typed clients receive only allow-listed metrics and never RC credentials or a generic RC proxy.

Useful commands:

```sh
./devtoolw runtime-x01
rclone-nexus jobs status
rclone-nexus jobs config
rclone-nexus job preview NAME
rclone-nexus job run NAME
rclone-nexus rc MOUNT
```

## Secure WebUI foundation

WEB-X01 adds an authenticated standalone WebUI on an ephemeral IPv4 loopback
port plus capability-gated KernelSU/APatch-style embedded transport. Module
Action starts or reuses the standalone authority and opens a fresh one-use
bootstrap URL. WEB-X02 adds Home/Mounts/Operations workflows and a transactional mount editor.
Mount edits are backend-previewed and require a fresh one-use proof bound to the
configuration revision and candidate digest. WEB-X03 completes the final
Home/Mounts/Jobs/Runtime/Logs/Settings navigation: jobs/settings are preview-proof
bound; Runtime converges namespace/policy/RC/cache and operation truth; logs and
support exports are bounded/redacted; and configured remote names can be browsed
without exposing rclone.conf credentials. Mount creation now reuses that safe remote
browser directly, offers resource-aware VFS profiles and shared-storage suggestions,
runs multi-issue field-addressable validation before review, and presents the existing
revision-bound one-use preview as a user-oriented Review/Create step with expiry and
operation feedback. WEB-G1 seals the WebUI boundary: broken
embedded-manager capability negotiation falls back only to a proven standalone
authority, and namespace/cache destructive mutations now require native one-use
preview proofs rather than relying on browser convention.

```sh
rclone-nexus webui start --open
racctl webui start --json
./devtoolw webui
./devtoolw web-x02
./devtoolw web-x03
./devtoolw web-g1
```

The browser has no generic shell/argv/file/rclone-RC endpoint and never reads
provider credentials. All backend calls are versioned typed operations from the
same native registry used by CLI and `racd`.


## Release qualification

REL-X01 promotes the module to **v0.1.0** and adds deterministic release artifacts, failure-injection validation, release documentation, and a real-device evidence harness. Release-facing documentation lives under `docs/release/`.

Build/qualify the release candidate:

```sh
./devtoolw rel-x01
```

Capture Android qualification evidence on the actual Termux device. The canonical passive capture is:

```sh
./devtoolw release-evidence
```

Optionally repeat `--mount` for configured mounts when invoking the helper directly. Schema v3 has no free-form `record pass` escape hatch: use `run`/`resume` so each PASS is derived from machine observations.

```sh
python3 scripts/dev/release_device_qualification.py capture --mount drive
python3 scripts/dev/release_device_qualification.py status
python3 scripts/dev/release_device_qualification.py run reboot
# perform the requested device action, then:
python3 scripts/dev/release_device_qualification.py resume reboot
python3 scripts/dev/release_device_qualification.py validate --require-complete
```

Device qualification evidence is intentionally excluded from the reproducible release source digest. After every mandatory endurance case is machine-qualified, run the authoritative final seal:

```sh
./devtoolw release
```

GRAND-G1 reruns the full executable qualification chain, including every prior gate audit and the WebUI JavaScript/security contracts in the configured `androidos` chroot. It fails closed if real-device evidence is missing, malformed, pending, failed, or contains an unexplained skip. Successful release outputs are `dist/rclone-nexus-v0.1.0.zip`, `dist/SHA256SUMS`, `dist/release-manifest.json`, and `dist/release-verdict.json`. The verdict binds the release artifact and source digest to the completed evidence digest without copying private device details into the distributable package.

### GRAND-G1 transaction-clean source qualification

The apply-time `grand-g1-source` workflow is transaction-clean: deterministic release checks use a temporary `racctl` prebuilt and a terminal cleanup restores/removes only known generated validation outputs. This lets Devtool preserve intentional dirty generated paths in the primary checkout. The authoritative post-commit `release` workflow still retains the final release artifacts and requires completed schema-v3 real-device evidence.

### Root-manager build/install workflow

The Devtool wrapper can now bootstrap the actual two-module device stack without directly copying files into `/data/adb/modules`:

```sh
./devtoolw install          # build/package/install Nexus; managed mode does not require a provider
./devtoolw install-stack    # optional legacy/external bootstrap: fetch+verify provider if missing, then install Nexus
./devtoolw install-status   # show detected manager plus active/staged module state
# reboot when reported, then:
./devtoolw install-verify
```

`install` is valid without an `rclone` provider and never replaces an externally owned provider. Managed execution uses the Nexus-qualified immutable runtime store. `install-stack` is an explicit legacy/external compatibility bootstrap and preserves an already-installed provider by default; provider replacement requires the direct helper's explicit `--replace-provider` flag. Magisk uses `magisk --install-module`, KernelSU/KernelSU Next use `ksud module install`, and APatch uses `apd module install`. Unknown-compatible managers fail closed instead of writing module directories directly. Provider auto-bootstrap downloads the latest `NewFuture/rclone-fuse3-magisk` release through GitHub's release API, requires a GitHub SHA-256 asset digest, and validates root-level `module.prop` id `rclone` before installation.

### Guided mount editor promise closure

The mount editor's structured validation is registry-aware: issues carry the affected mount identity as well as category/field metadata, so an existing mount's problem is not misrepresented as an inline error on the mount currently being edited. Destination availability indicators distinguish local syntax checks from completed backend checks.

Source setup reports provider binary/FUSE/config readiness and remote reachability. Named VFS profiles own and disable their raw effective settings until `Custom` is selected, while the recommendation reports the device RAM/cache-free observations used by Nexus. The authoritative review includes behavior/policy choices and an explicit safety classification. Expired previews state that no configuration changed. If configuration publication succeeds but lifecycle work does not, the Mounts view provides retry/edit/log/operation recovery actions.

### GRAND-G1 device-runtime qualification remediation

Real-device qualification now validates the generated mount CLI against the exact selected NewFuture provider binary before launch. Nexus no longer emits the obsolete `--rc-no-open-browser` option. Terminal provider/CLI/FUSE failures retain structured root-cause metadata and are not automatically reclassified as connectivity problems or charged against the restart budget; explicitly transient network failures remain retryable.

That remediation originally made provider discovery consistent across shell and Go. Under RUNTIME-STANDALONE X01, that ordering is retained only inside explicit `external` compatibility mode. Managed mode is instead pinned to the Nexus-owned runtime/config roots; generic `PATH`, provider env, and `RCLONE_CONFIG` cannot redirect it. Shell runtime/config helpers project the native `racctl runtime` resolver rather than selecting a second authority. Offline-allowed mounts may cold-start from VFS cache while the device itself is offline, and Android connectivity discovery includes IPv4, IPv6, and active-interface/VPN fallbacks.

Runtime/Logs now expose the same backend-owned lifecycle truth. Namespace visibility controls remain unavailable until a live Nexus-owned source mount exists; non-applicable namespace states are neutral. Rclone logs use per-line timestamps/severity, suppress repetitive CLI-help floods behind expandable details, and can be filtered by severity/source/search. Mobile WebUI chrome respects Android safe-area insets and scroll-centers the active primary tab rather than clipping navigation beneath system UI.

The focused contract is documented in `docs/GRAND_G1_DEVICE_RUNTIME_WEBUI_REMEDIATION_AUDIT.md` and checked by:

```sh
python3 scripts/dev/check_device_runtime_webui_remediation.py
```

### GRAND-G1 runtime/WebUI v2 promise closure

The device-runtime remediation now exports the NewFuture provider environment across all installed entry points, qualifies CLI flags against the exact selected provider executable, treats deterministic provider/config/argv failures as non-retryable without consuming restart budget, keeps transient network failures retryable, sanitizes startup diagnostics, and disables terminal retry controls in the WebUI.

> GRAND-G1 runtime note: provider CLI preflight checks the exact selected rclone binary and merges mount-specific plus global flag help, because rclone does not list every global option in `mount --help`.

## Android source-build bundle

```sh
# verify or import a SOURCE-X02 CI build bundle
racctl runtime source verify-build ./runtime-build
racctl runtime source import-build ./runtime-build
```

### SOURCE-G1 supply-chain seal

The SOURCE/UPDATE campaign is sealed by a behavioral gate, not a source-presence check. SOURCE-G1 must resolve real external sources, download and hash/archive-validate actual bytes, pass the Android/FUSE runtime qualifier, stage without hot-swapping, activate the staged runtime, and prove rollback restores the previous executable bytes. Adversarial archives and offline/disappeared sources must fail closed without changing runtime authority. See `docs/implementation/SOURCE-G1.md`.


### SOURCE-G1 resolution evidence authority

SOURCE-G1 snapshots immutable runtime-source resolutions from the same canonical state layout used by production (`runtime/sources/resolutions` beneath the Nexus state root). The gate captures those records at resolution/import time before later update and rollback operations, then verifies their immutable repository/release/commit/asset identity as part of the rooted supply-chain seal.


SOURCE-G1 canonical milestone seals RNX-P374..RNX-P434 (61 promises), including its nine mandatory real-execution gate obligations.

## Migrating from the NewFuture provider

Standalone migration is a reviewed authority handoff, not an in-place mutation
of the legacy module. Nexus detects `/data/adb/modules/rclone` read-only, validates
its `conf/rclone.conf` with the selected managed runtime and can discover active
provider mounts plus `conf/sync`/`conf/copy` job definitions.

```sh
racctl migration inspect
racctl migration preview --mount <mount-id> --job sync:1 --job-every 24h
racctl migration apply <proof> <revision> <digest> --mount <mount-id> --job sync:1 --job-every 24h
# Disable/remove the legacy rclone module explicitly in your root manager.
racctl migration finalize-preview
racctl migration finalize <proof> <revision> <digest>
```

Imports are opt-in and disabled until finalization. Nexus never creates the
legacy module's disable/remove markers and never modifies its files. If migration
is interrupted, finalization fails, or the legacy lifecycle later becomes active
again, the durable migration authority recovers or fails closed.
