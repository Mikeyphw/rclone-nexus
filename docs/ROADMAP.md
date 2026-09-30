# Rclone Nexus — compressed implementation roadmap

Status: **REL-X01 implemented (15/16); GRAND-G1 is next (16/16)**.

This roadmap deliberately compresses the original 31-position campaign into
**16 positions: 11 implementation overlays, 4 intermediate gates, and 1 final
seal**. No product, security, lifecycle, Android, diagnostics, portability,
WebUI, or release promise was removed. The reduction comes from merging work
that shares one ownership boundary and can be validated together.

Rclone Nexus remains non-invasive: NewFuture's `rclone-fuse3-magisk` remains
the provider of rclone, FUSE and the canonical `rclone.conf`; this project owns
Android integration, lifecycle, policy, diagnostics and UI. Rclone Nexus must
not patch `/data/adb/modules/rclone`, ship a second rclone/FUSE runtime, or make
browser code a privileged shell.

The WebUI architecture follows the strongest parts of chrootctl's root-module
WebUI design: static `webroot/` assets, a typed allow-listed backend contract, a
universal authenticated loopback server, and an embedded KernelSU/APatch-style
bridge only when a real compatible bridge exists. Business logic stays in one
backend shared by CLI, boot hooks and both WebUI transports.

## Campaign rules

- The committed v0.1.0-dev repository is **position 0** and remains the migration
  source for all later work.
- Before each implementation overlay, inspect the next three roadmap positions
  and merge only when they still share one ownership/validation boundary.
- Implementation/remediation overlays run targeted Devtool-owned validation when
  a meaningful boundary exists. Broad device/endurance/release validation stays
  in the named gate/seal positions.
- Gates remain separate overlays. Each gate audits every implementation overlay
  since the previous gate, folds discovered gaps into remediation, and reruns
  until clean.
- The final implementation overlay remains separate from the final seal.
- User state lives under `/data/adb/rclone-nexus`; module replacement,
  provider upgrades and Rclone Nexus upgrades must not erase it.
- Configuration is data. No mount definition, WebUI payload or API parameter may
  become shell text through `eval`, `source`, `sh -c`, or arbitrary argv RPC.
- Mutations use typed preview/apply operations with resource revision and payload
  digest so stale browser/CLI state cannot overwrite newer state.
- Credentials remain provider/backend-only. The WebUI may expose remote names and
  credential-free readiness, but never reads or renders `rclone.conf` secrets.
- Namespace mutation, stale-mount cleanup, cache deletion, configuration
  replacement and support-bundle export require explicit ownership plus bounded
  rollback/recovery behavior.

## Target end-state

```text
                         rclone upstream
                              |
                              v
                 NewFuture rclone-fuse3-magisk
                 rclone + FUSE + rclone.conf
                              |
                              v
+------------------------------------------------------------------+
|                   Rclone Nexus                       |
|                                                                  |
|  service.sh ---> racd native supervisor                          |
|                    |                                             |
|                    +--> typed control plane <--- racctl CLI       |
|                    |           ^                                 |
|                    |           |                                 |
|                    |      WebUI transports                       |
|                    |       /           \\                         |
|                    | loopback HTTP   KSU/APatch bridge           |
|                    |                                             |
|                    +--> mounts / jobs / policies / health        |
|                    +--> namespace visibility adapters            |
|                    +--> VFS cache governor                       |
|                    +--> local-only rclone RC metrics             |
|                    +--> operation journal / logs / doctor        |
+------------------------------------------------------------------+
```

`service.sh`, `post-fs-data.sh`, `action.sh` and install/uninstall hooks stay
small. Long-running state, validation, process ownership, WebUI HTTP service and
structured protocols move into a native Go control plane. Shell entry points
remain compatibility wrappers around that control plane.

---

# Wave 1 — canonical control plane and lifecycle

## 1/16 — CORE-X01: native control plane + typed operation protocol ✅ implemented

Merge of former **CORE-X01 + CORE-X02**. These are one backend authority and one
Go/protocol validation boundary.

Deliver:

- deterministic Android/Termux Go builds, initially arm64 with architecture
  plumbing ready for more Android ABIs;
- `racctl` plus daemon/internal `racd` mode, with current shell commands reduced
  to compatibility launchers;
- typed provider discovery: module identity, rclone version, FUSE readiness and
  config readiness without exposing private paths to UI clients;
- root-owned runtime directories with explicit modes;
- single-instance daemon locking and clean signal shutdown;
- version/capabilities JSON contract;
- versioned JSON request/response envelopes;
- NDJSON progress/event streams for long-running operations;
- an allow-listed operation registry rather than arbitrary command/argv
  execution;
- typed query, preview, run, cancel and reconcile operation classes;
- bounded stdout/stderr/event payloads;
- stable machine error codes separated from human-readable detail;
- protocol capability negotiation so mismatched clients fail safely;
- backend redaction helpers used before data crosses any transport boundary;
- initial read-only provider and mount status operations;
- compatibility tests proving existing `rclone-mountctl` behavior is retained;
- Devtool Go build/test jobs while preserving shell/module-contract jobs.

Targeted validation: Go unit tests, shell compatibility, module contract,
protocol golden/invalid-schema tests, payload bounds and redaction tests.

## 2/16 — LIFE-X01: transactional configuration + authoritative per-mount lifecycle ✅ implemented

Merge of former **CFG-X01 + LIFE-X01**. Configuration changes and the lifecycle
planner that applies them are one mutation boundary and should land together.

Deliver configuration registry v2:

- parser/migrator for v0.1 literal `key=value` mount files;
- normalized typed mount model;
- semantic validation of remote, mountpoint, VFS/cache values and supported
  advanced options;
- duplicate/overlapping destination detection;
- candidate -> validate -> preview -> atomic publish transaction;
- revision/digest proof on apply;
- previous-known-good rollback slot;
- diff planner that restarts only affected mounts;
- root-local `args_file` remains trusted CLI configuration and is never exposed
  as a generic WebUI command surface;
- config export contains no rclone credentials.

Deliver authoritative per-mount lifecycle:

- start/stop/restart/status/list/reconcile per mount;
- process identity validation to reject stale/reused PIDs;
- independent per-mount state, logs and failure domains;
- graceful stop followed by bounded forced cleanup;
- no global `kill-all` dependency;
- one failed mount cannot block healthy mounts;
- persistent desired state distinct from observed runtime state.

Targeted validation: migration corpus, invalid/overlap cases, crash-safe atomic
writes, stale-revision rejection, fake-rclone lifecycle harness, PID reuse,
concurrent start/stop races and one-mount failure isolation.

## 3/16 — LIFE-X02: readiness, self-healing and persistent operation truth

**Implementation status: delivered and qualified by CORE-G1.**

Merge of former **LIFE-X02 + LIFE-X03**. Readiness and recovery are both owned by
the supervisor state machine and should not be split across overlays.

Readiness graph covers:

- `/data` and Nexus persistent state;
- provider module/binary/FUSE/config availability;
- Android boot completion where required;
- target storage/mountpoint readiness;
- network class when required;
- optional remote reachability probes.

Deliver:

- bounded exponential retry with explicit waiting reason;
- independent mount activation and boot reconciliation;
- offline remotes do not automatically destroy a valid VFS mount;
- health states `RUNNING`, `DEGRADED`, `REMOTE_OFFLINE`, `MOUNT_STALE`,
  `AUTH_ERROR`, `FUSE_ERROR`, `RETRYING`, and `STOPPED`;
- process-alive vs mount-alive distinction;
- stale FUSE detection and owned cleanup;
- bounded recovery/backoff and restart budgets;
- persistent operation journal with progress/events and terminal state;
- reboot/reopen reconciliation rather than browser-local truth;
- cancellation only where safely supported.

Targeted validation: dependency simulation, provider/network/storage-late cases,
retry bounds, clean shutdown while waiting, process death, stale mount,
remote/auth failure, restart exhaustion and journal recovery.

## 4/16 — CORE-G1: control-plane/lifecycle gate

**Gate status: qualified after audit-loop remediation.**

Separate gate over positions 1-3.

Authoritative checks:

- v0.1 configuration migrates losslessly;
- lifecycle operations survive repeated start/stop/reconcile cycles;
- crash and stale-state injection cannot cross mount ownership boundaries;
- persistent state survives module replacement simulation;
- protocol/redaction/security invariants remain intact;
- generated flashable package still passes module/package contracts.

Gate audit found and closed two gaps before qualification: unmount authority
now requires provable Nexus ownership even for stale/missing process identity,
and reconcile re-reads desired state under the per-mount lock so a stale
supervisor decision cannot override a newer explicit start/stop. The gate has no
remaining unresolved gap.

---

# Wave 2 — Android visibility, policy, VFS and rclone runtime intelligence

## 5/16 — ANDROID-X01: namespace discovery, propagation and app-visibility qualification

Status: **implemented and qualified by ANDROID-G1**.

Merge of former **NS-X01 + NS-X02 + NS-X03**. Discovery, mutation and
qualification are one Android namespace capability boundary; keeping them in one
overlay avoids landing a half-authoritative visibility subsystem.

Deliver read-only topology authority:

- parse `/proc/*/mountinfo` and relevant Android storage topology;
- identify Nexus service/root/shell/zygote/app-visible namespace boundaries
  by capabilities rather than Android version strings alone;
- report per-mount visibility;
- `racctl namespace inspect <mount>` and structured backend query.

Deliver safe visibility mutation:

- capability-selected adapter interface for discovered storage layouts;
- explicit preview of proposed bind/propagation mutations;
- mount ownership markers so unrelated paths are never unmounted;
- apply/reconcile/rollback transaction;
- safe behavior when no qualified strategy exists;
- root/service visibility remains useful even when ordinary-app visibility is
  unsupported.

Deliver real Android qualification:

- primary and secondary Android-user awareness;
- user-specific mount visibility state;
- shell/Termux/ordinary-app probes where technically possible;
- reboot and zygote/storage-remount reconciliation;
- evidence stating exactly which visibility class is achieved.

No universal app-visibility claim is allowed without observed evidence.

Targeted validation: mountinfo fixtures across Android/root-manager layouts,
parser fuzz/error cases, namespace sandbox failure/rollback injection, and
controlled device-side propagation/visibility smoke.

## 6/16 — POLICY-X01: resource policy engine + VFS/cache governor

Status: **implemented and qualified by ANDROID-G1**.

Merge of former **POLICY-X01 + VFS-X01**. Both own resource governance and are
consumed by the same mount/job reconciliation layer.

Network modes:

- `any`
- `wifi`
- `unmetered`
- `offline-allowed`

Resource constraints include charging-only, minimum battery, minimum free cache
space and optional boot/network settle windows.

Deliver:

- Android connectivity/power/storage observation without aggressive polling;
- `pause/retry/resume` decisions separated from lifecycle state;
- explicit policy reason in status/UI;
- policy changes reconcile only affected resources;
- VFS profiles `streaming`, `balanced`, `offline`, `minimal`, and `custom`;
- profiles expand to explicit rclone options and never silently mutate custom
  mounts;
- RAM/free-space-aware recommendations;
- per-mount cache status;
- bounded cache pruning and high/low-water limits;
- previewed cache clear/forget actions with strict ownership checks.

Targeted validation: synthetic policy transition matrix, profile expansion golden
tests, low-space behavior and owned-cache deletion safety.

## 7/16 — RUNTIME-X01: scheduled jobs + local-only rclone RC telemetry

Status: **implemented and qualified by ANDROID-G1**.

Merge of former **JOB-X01 + RC-X01**. Jobs and mount telemetry both integrate
with rclone runtime state, operation progress and the journal.

Deliver managed jobs:

- typed scheduled `sync`, `copy` and read-only `check` jobs;
- daemon scheduler with persisted next-run state;
- charging/unmetered/minimum-battery policy integration;
- one-shot/manual run and safe cancellation;
- operation-journal progress, bytes/rate/ETA when rclone exposes them;
- source/destination validation and destructive-sync preview;
- no credential exposure and no arbitrary rclone argv API.

Deliver RC authority:

- loopback-only per-instance RC endpoints;
- ephemeral per-instance credentials retained backend-side;
- lifecycle-coupled RC startup/teardown;
- typed metrics: transferred bytes, speed, VFS cache, open files, errors, uptime
  and backend-supported stats;
- explicitly allow-listed control operations only; no generic WebUI RC tunnel;
- graceful degradation when a metric is unavailable.

Targeted validation: schedule/reboot recovery, policy blocking, cancellation,
destructive-preview proof, fake-rclone progress streams, endpoint isolation,
auth failure, port collision, metrics parsing and teardown.

## 8/16 — ANDROID-G1: Android/runtime gate

Status: **qualified**.

Separate gate over positions 5-7.

Qualify:

- no namespace mutation before topology/preview proof;
- app-visibility truth is reported per observed namespace class;
- reboot/network loss/storage remount/self-heal convergence;
- policy transitions do not create duplicate rclone processes;
- VFS cache limits remain owned and bounded;
- scheduled jobs survive daemon restart without duplicate execution;
- RC never binds beyond loopback and credentials never cross the typed API.

---

# Wave 3 — diagnostics and root-framework portability

## 9/16 — PLATFORM-X01: diagnostics + root portability + upgrade lifecycle

Status: **implemented and qualified by PLATFORM-G1**.

Merge of former **DIAG-X01 + ROOT-X01 + ROOT-X02**. These all own the module's
platform boundary rather than rclone business logic, and can share one package /
root-manager validation window.

Deliver diagnostics:

- rotating root-owned logs with bounded retention;
- structured lifecycle/policy/namespace/RC events;
- redaction of credentials, tokens, sensitive provider config and unnecessary
  private paths;
- `racctl doctor` checks provider/FUSE/config/kernel/storage/network/namespace/
  root-manager/mount/job readiness;
- PASS/WARN/FAIL plus stable code and next-step guidance;
- `racctl doctor --bundle` deterministic sanitized support archive;
- explicit diagnostic failures instead of empty-success output.

Deliver root-framework capability abstraction:

- Magisk;
- KernelSU / KernelSU Next;
- APatch;
- unknown-compatible module managers through conservative capability discovery;
- core lifecycle uses capabilities rather than manager names wherever possible;
- provider detection remains module-id based and reports unsupported combinations
  truthfully.

Deliver install/update/uninstall lifecycle:

- persistent state preservation across Nexus updates;
- schema migration before daemon startup;
- rollback-safe failed upgrade handling;
- uninstall removes runtime artifacts while requiring explicit choice before
  deleting persistent user configuration/cache;
- module Action integration;
- `webroot/` packaging for managers supporting embedded WebUI;
- file-mode/integrity manifest checks;
- no dependency on modifying NewFuture's provider module.

Targeted validation: redaction/secret-canary corpus, permissions/log rotation,
bundle manifest, root-manager fixtures, missing-capability behavior, upgrade /
downgrade/interruption fixtures, uninstall preservation and package integrity.

## 10/16 — PLATFORM-G1: diagnostics/portability gate

Status: **qualified**.

Separate gate over position 9 plus all platform-facing behavior inherited from
previous waves. The gate audit closed three platform-safety gaps before sealing:
corrupt/missing package integrity now fails before state migration, unknown
module managers advertise only capabilities actually inferred from their layout,
and diagnostic/support files repair to private root ownership and mode.

Qualifies Magisk + KernelSU-family + APatch packaging contracts through manager
fixtures/common module hooks, rollback-safe state migration, permissions,
logging/redaction, failed update recovery, provider-missing behavior and
persistent-state safety. Real-manager/device observations remain evidence-bound
and are not fabricated when a matching privileged host is unavailable.

---

# Wave 4 — WebUI

## WebUI product surface

Final navigation:

```text
Home | Mounts | Jobs | Runtime | Logs | Settings
```

Contextual workspaces provide:

- mount lifecycle/status and health;
- mount configuration/profile/policy editing;
- Android namespace visibility;
- VFS cache state/actions;
- rclone RC metrics;
- sync/copy/check jobs;
- provider readiness and remote-name browsing;
- operation history/progress/cancellation;
- doctor and sanitized support bundle;
- root-manager/transport/runtime settings.

The UI never becomes an rclone credential editor. Remote names may be discovered
through typed provider operations and credential-free remote browsing may feed
validated fields, but `rclone.conf` secret material stays outside the browser
boundary.

## 11/16 — WEB-X01: secure WebUI foundation + standalone and embedded transports

Merge of former **WEB-X01 + WEB-X02 + WEB-X03**. Static security, HTTP auth and
embedded bridge selection form one transport/security boundary and should be
qualified together before feature pages depend on it.

Deliver static WebUI foundation in `module/webroot/`:

- no CDN/network assets;
- restrictive CSP;
- backend-derived text rendered with `textContent`, not `innerHTML`;
- typed operation names only;
- no generic shell, exec, argv, arbitrary-file reader or generic rclone RC API;
- bounded transport output;
- schema/capability/version checks before rendering backend data;
- initial read-only Home dashboard and compatibility banner.

Deliver universal standalone browser mode:

- IPv4 loopback only with ephemeral port;
- cryptographically random one-use bootstrap token;
- short-lived HttpOnly + SameSite=Strict session cookie;
- CSRF token plus exact Origin checks on mutation routes;
- Host validation, no-store, CSP/frame/referrer/MIME headers;
- bounded request/header/body/time limits;
- idle shutdown and owning-process cleanup;
- `/api/v1` routes mapped to the same typed operation registry as CLI;
- `racctl webui start|serve` and structured startup JSON.

Deliver module-manager integration:

- `action.sh` starts/reuses standalone WebUI and opens the one-use Android URL;
- embedded module WebUI uses a manager bridge only when a real compatible bridge
  exists;
- capability-based transport selection, never localhost inference;
- KernelSU/APatch bridge maps fixed typed operations to the native backend;
- standalone HTTP remains fallback and the primary cross-manager path;
- stdout/stderr/error ordering normalized across transports.

Devtool adds a WebUI target and JS syntax/contract validation through the
configured chroot when host Termux has no Node dependency.

Targeted validation: CSP/static-security contracts, JS syntax and typed-client
tests, compatibility mismatch behavior, bootstrap replay rejection, CSRF/origin/
host rejection, idle shutdown, request bounds, typed route coverage, bridge
selection/fallback and Android VIEW-action generation.

## 12/16 — WEB-X02: Home/Mounts/Operations + transactional mount editor

Status: **implemented**.

Merge of former **WEB-X04 + WEB-X05**. Runtime lifecycle actions and mount edits
share the same resource/revision UX and mutation-preview boundary.

Deliver:

- dashboard counts and provider/backend readiness;
- mount cards with desired/observed state and health reason;
- start/stop/restart/reconcile actions;
- live operation journal with persistent progress and terminal details;
- cancel where safe;
- backend state restored after WebUI close/reopen;
- polling/refresh only while relevant views are visible;
- create/edit/disable/delete mount definitions;
- profile selection and advanced supported fields;
- network/battery/storage policy editor;
- backend-authoritative validation;
- preview diff explaining restart/namespace/cache consequences;
- revision/digest-bound apply;
- fresh preview required for destructive or stale mutations;
- only affected mounts reconcile after apply.

Targeted validation: lifecycle typed calls, browser-reopen truth, failure
rendering, journal rendering, stale-preview rejection, invalid/duplicate
mountpoints, profile/custom preservation and rollback UX.

## 13/16 — WEB-X03: Runtime/Jobs/Logs/Doctor/Remotes/Settings convergence

Status: **implemented**.

Merge of former **WEB-X06 + WEB-X07**. These are the remaining operational
workspaces over already-established typed backend contracts.

Deliver Runtime:

- namespace visibility matrix for root/service/shell/Termux/app/user classes;
- readiness/policy blockers;
- self-heal/retry state;
- RC metrics and transfer rate;
- VFS cache size/limits/open files;
- previewed cache clear/forget;
- qualified namespace reconcile/rollback actions.

Deliver Jobs/Diagnostics/Settings:

- scheduled job list/editor/manual run/progress/cancel;
- bounded structured logs with severity and follow mode;
- doctor PASS/WARN/FAIL cards and next steps;
- support-bundle generation/copy/save flow;
- provider/rclone readiness and configured remote **names only**;
- optional credential-free remote path browser feeding validated mount/job
  fields;
- settings for default mount view, accessibility/reduced-motion preference, log
  retention, safe refresh intervals and WebUI idle lifetime;
- root-manager/transport/capability diagnostics;
- responsive phone/tablet layout plus keyboard/accessibility semantics.

Remote browsing carries backend-validated provenance. Manually editing a remote
path invalidates that provenance and any stale preview.

Targeted validation: bounded metrics, no RC credentials, mutation-preview proof,
unsupported-visibility truth, credential canaries, logs/path redaction, job
workflows, remote provenance, accessibility/static checks and settings
persistence.

## 14/16 — WEB-G1: WebUI security/functionality gate

**Gate status: qualified after audit-loop remediation.**

Separate gate over positions 11-13. The audit closed two gaps before sealing:
embedded transport selection now falls back only after capability-proven
standalone availability, and namespace/cache destructive runtime mutations now
require one-use revision/digest-bound preview proofs at the native backend.

Must prove:

- both transports reach the same typed backend authority;
- no arbitrary shell/argv/root RPC exists;
- standalone auth/bootstrap/CSRF/origin/host rules hold;
- credentials and RC secrets never render;
- every mutation requires authoritative validation/preview where specified;
- browser reopen preserves backend operation truth;
- embedded bridge failure falls back cleanly rather than fabricating success;
- all screens function at phone and tablet widths;
- sanitized diagnostic output is bounded and copy/export safe.

WEB-G1 also qualifies responsive phone/tablet layout contracts and keyboard
semantics (modal Escape handling and active navigation accessibility state).

---

# Wave 5 — release qualification

## 15/16 — REL-X01: endurance, failure injection, release packaging and documentation

Merge of former **REL-X01 + REL-X02 + REL-X03**. This is the final
implementation/qualification preparation boundary immediately before the final
seal; combining it keeps all release evidence and release-facing artifacts in
one coherent overlay while preserving the required separate final gate.

Run real-device endurance covering:

- reboot and root-manager restart;
- provider module update/reload;
- Wi-Fi <-> mobile <-> offline transitions;
- doze/screen-off/charging transitions;
- remote outage/auth failure/recovery;
- stale FUSE and killed rclone process;
- daemon crash/restart;
- storage remount and low-space cache pressure;
- WebUI close/reopen and standalone idle expiry;
- Android user/namespace changes where available;
- multiple simultaneous mounts and scheduled jobs.

Run security/failure/state-integrity qualification:

- config/state atomicity under interruption;
- no cross-mount cleanup;
- no world-readable credentials/tokens/logs;
- support-bundle secret canaries;
- WebUI injection/path traversal/request-bound attacks;
- RC loopback confinement;
- operation cancellation/retry safety;
- upgrade/uninstall rollback;
- damaged/missing provider handling;
- package manifest/file modes/integrity.

Deliver release-facing closure:

- versioned migration guide from v0.1.0-dev;
- installation/update/uninstall/recovery documentation;
- WebUI transport explanation for Magisk/KernelSU/APatch;
- namespace support matrix phrased from evidence rather than assumptions;
- mount/job/profile configuration reference;
- doctor/support workflow;
- reproducible module package and checksums;
- changelog/release notes and known limitations.

Evidence records exact device, Android, root-manager, provider and rclone
versions and the exact namespace visibility classes that passed.

## 16/16 — GRAND-G1: final release seal

The final separate gate reruns the authoritative Devtool release workflow against
the actual release artifact and captured device evidence. It verifies every
roadmap promise, security invariant, migration path, package identity,
documentation claim and real-device result. The release is not sealed while any
promise exists only as a placeholder or string-level test.

---

# Compression map — old 31-position plan to this 16-position plan

| New position | Former positions merged | Scope retained |
| --- | --- | --- |
| 1 CORE-X01 | 1 CORE-X01 + 2 CORE-X02 | Go control plane, typed protocol, operation registry |
| 2 LIFE-X01 | 3 CFG-X01 + 4 LIFE-X01 | config v2, preview/apply, authoritative mount lifecycle |
| 3 LIFE-X02 | 5 LIFE-X02 + 6 LIFE-X03 | readiness, self-heal, states, operation journal |
| 4 CORE-G1 | 7 CORE-G1 | lifecycle/configuration gate |
| 5 ANDROID-X01 | 8 NS-X01 + 9 NS-X02 + 10 NS-X03 | discovery, visibility mutation, multi-user qualification |
| 6 POLICY-X01 | 11 POLICY-X01 + 12 VFS-X01 | resource policy, profiles, cache governor |
| 7 RUNTIME-X01 | 13 JOB-X01 + 14 RC-X01 | scheduler/jobs, local RC, live metrics |
| 8 ANDROID-G1 | 15 ANDROID-G1 | Android/runtime gate |
| 9 PLATFORM-X01 | 16 DIAG-X01 + 17 ROOT-X01 + 18 ROOT-X02 | diagnostics, root abstraction, install/update lifecycle |
| 10 PLATFORM-G1 | 19 PLATFORM-G1 | platform gate |
| 11 WEB-X01 | 20 WEB-X01 + 21 WEB-X02 + 22 WEB-X03 | WebUI foundation, standalone security, embedded transport |
| 12 WEB-X02 | 23 WEB-X04 + 24 WEB-X05 | dashboard/mount lifecycle, editor, preview/apply |
| 13 WEB-X03 | 25 WEB-X06 + 26 WEB-X07 | runtime, jobs, logs, doctor, remotes, settings |
| 14 WEB-G1 | 27 WEB-G1 | WebUI gate |
| 15 REL-X01 | 28 REL-X01 + 29 REL-X02 + 30 REL-X03 | endurance, failure/security qualification, release docs/package |
| 16 GRAND-G1 | 31 GRAND-G1 | final release seal |

No former position is omitted.

# Devtool evolution across the compressed roadmap

The current `rclone_nexus` target remains canonical. Add capabilities only
when the owning overlay needs them:

1. **CORE-X01 (1/16)** — Go build/test jobs and architecture-aware module
   packaging.
2. **ANDROID-X01 (5/16)** — device-smoke workflow through Devtool remote/device
   execution rather than ad-hoc `adb`/`su` scripts.
3. **WEB-X01 (11/16)** — `rclone_webui` target with JS syntax/contract tests in
   the configured chroot; host Node must not become a hidden prerequisite.
4. **PLATFORM-X01 (9/16)** — root-manager/package compatibility and update
   lifecycle checks.
5. **REL-X01 (15/16)** — explicit real-device endurance/failure-injection
   workflow and evidence ingestion.
6. **GRAND-G1 (16/16)** — one release workflow consuming package, tests and
   captured device evidence and emitting one truthful final verdict.

Recommended wrapper surface by campaign end:

```text
./devtoolw validate
./devtoolw test
./devtoolw build
./devtoolw webui
./devtoolw device-smoke
./devtoolw release
```

Wrappers remain frontends; canonical work stays in `.devtool.toml` jobs and
workflows.

# Promise ledger

The campaign is complete only when these promises are demonstrably delivered:

| Promise | Owning positions |
| --- | --- |
| Non-invasive NewFuture dependency boundary | all; especially 1, 9 |
| Native control plane + typed/allow-listed protocol | 1 |
| Per-mount lifecycle and independent failure domains | 2 |
| Atomic validated config and affected-only restart | 2 |
| Dependency-aware boot/reconcile | 3 |
| Self-healing and explicit health states | 3 |
| Persistent operation truth / safe cancellation | 3 |
| Android namespace/app visibility truth | 5 |
| Multi-user visibility qualification | 5 |
| Network/battery/storage policies | 6 |
| VFS profiles and bounded cache management | 6 |
| Scheduled sync/copy/check jobs | 7 |
| Local-only RC and live metrics | 7 |
| Doctor, rotating logs and redacted support bundle | 9 |
| Magisk/KernelSU/APatch capability abstraction | 9 |
| Safe install/update/uninstall and persistent-state migration | 9 |
| Secure universal standalone WebUI | 11 |
| Embedded KSU/APatch WebUI compatibility | 11 |
| Mount lifecycle/configuration WebUI | 12 |
| Runtime/jobs/logs/doctor/remotes/settings WebUI | 13 |
| No arbitrary shell/argv/credential exposure from WebUI | 1, 11-14 |
| Real-device endurance and truthful compatibility claims | 15, 16 |
| Security/failure-injection/state-integrity qualification | 15, 16 |
| Reproducible release, checksums and migration docs | 15, 16 |

# Current campaign position

The control/lifecycle, Android/runtime and platform waves are implemented and
qualified by their separate CORE-G1, ANDROID-G1 and PLATFORM-G1 gates.

WEB-X01 is implemented as the secure transport/foundation boundary. Standalone
browser mode is an authenticated ephemeral IPv4-loopback authority with one-use
bootstrap tokens, strict sessions/CSRF/Origin/Host controls and typed `/api/v1`
routes over the existing operation registry. KernelSU/APatch-style embedded mode
is capability-gated and can submit only bounded typed protocol envelopes through
a fixed native bridge entry point.

WEB-X02 is implemented as the first operational UI boundary. Home, Mounts and
Operations are backend-authoritative views; mount lifecycle actions and journal
reconnect use typed operations, and the transactional mount editor never parses
or edits root configuration files directly. Every config apply or rollback
requires a fresh one-use backend preview proof bound to the exact revision and
candidate digest. Preview explains lifecycle, namespace, policy and cache
consequences, while private root-local `args_file` state is preserved server-side
and never exposed to the browser.

WEB-X03 completes the operational WebUI surface. Final top-level navigation is
Home, Mounts, Jobs, Runtime, Logs and Settings. Runtime converges namespace,
readiness, policy, RC and VFS/cache truth; Operations remains contextual runtime
journal truth. Jobs and WebUI settings use fresh one-use preview proofs. Logs and
support export are bounded/redacted, remote discovery exposes configured names and
credential-free paths only, and standalone idle/log-retention preferences are
backend-owned settings rather than browser-local authority.

WEB-G1 qualifies that complete WebUI window after closing embedded-to-standalone
fallback and backend proof-enforcement gaps for namespace/cache mutations. The
browser still has no arbitrary privileged execution surface or secret-bearing RC
proxy, and the gate records responsive phone/tablet plus keyboard semantics.

**Next overlay: GRAND-G1 — full-plan 16/16.** REL-X01 has completed release
qualification preparation and produced the v0.1.0 release-candidate artifact/evidence
contract. The only remaining scope is the separate authoritative final release seal.
