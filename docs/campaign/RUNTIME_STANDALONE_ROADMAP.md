# Full Detailed Roadmap — `RUNTIME-STANDALONE`

## Important status note

This roadmap is a **design seed**, not yet the canonical numbered promise ledger.

Before implementation, the new chat must merge it with current source reality and all historical obligations, then assign exact promise numbers and exact overlay/full-plan counts. Do not assume these labels are final merely because they are written here.

## Campaign intent

Convert Rclone Nexus from a companion to a standalone runtime/control plane with replaceable rclone-compatible engines, qualified updates, safe migration, and one lifecycle authority.

The provisional compressed plan is **8 implementation overlays + 3 gates = 11 positions**.

---

## Position 1 — RUNTIME-X01: Canonical runtime ownership

### Goal
Establish one canonical runtime/config/lifecycle authority and remove hard architectural dependence on `/data/adb/modules/rclone` in managed mode.

### Deliverables

- explicit runtime mode model: `managed`, `external`, `migration-required`;
- canonical Nexus state/config/runtime roots;
- one production runtime resolver used by daemon, CLI, WebUI, supervisor, jobs, diagnostics and install verification;
- one canonical `rclone.conf` resolver for managed mode;
- external-provider compatibility adapter clearly marked noncanonical;
- migration detection when a legacy provider exists;
- boot path refuses ambiguous dual-authority state;
- source documentation of authority direction.

### Production adoption requirements

Trace and converge at least:

- module `service.sh` / boot entry;
- `racctl` normal invocation;
- daemon/control engine;
- mount lifecycle;
- supervisor;
- jobs;
- readiness/policy;
- diagnostics;
- WebUI API/control paths;
- install/install-verify;
- release qualification.

### Duplicate authorities to remove/reduce

- provider-dir runtime lookup as independent default authority;
- arbitrary PATH-first runtime selection;
- shell and Go config resolution divergence;
- parent automount as an independent lifecycle authority;
- parent autosync as an independent jobs authority.

### Negative evidence

- managed mode with provider removed still starts correctly;
- external mode missing executable fails closed;
- both Nexus + parent automount detected => ambiguity error/migration state, not silent double mount;
- poisoned PATH cannot override managed runtime;
- poisoned `RCLONE_CONFIG` cannot redirect managed config unless explicit supported override policy allows it.

---

## Position 2 — RUNTIME-X02: Runtime store + qualification

### Goal
Make rclone-compatible binaries first-class immutable candidates rather than package-fixed dependencies.

### Suggested store

```text
/data/adb/rclone-nexus/runtimes/<runtime-id>/
├── rclone
└── manifest.json
```

### Required import sources

- local file;
- explicit executable path (advanced unmanaged mode);
- URL;
- GitHub repository + release/tag/asset;
- built candidate from source;
- NewFuture-derived candidate.

### Manifest/provenance fields

At minimum:

- schema version;
- runtime ID;
- engine (`rclone`, `bclone`, custom);
- version output;
- source type;
- repository;
- resolved release/tag/commit;
- source asset URL/name when applicable;
- archive SHA-256;
- binary SHA-256;
- architecture/OS/ELF metadata;
- import/build timestamp;
- qualifier version;
- qualification result/evidence.

### Qualification boundary

Every candidate must be tested equivalently regardless of brand/source:

- executable and valid ELF;
- arm64 where device requires it;
- Android execution compatibility;
- `version` works;
- config support;
- `mount` command exists;
- generated global + command flags are supported;
- RC support;
- temporary local FUSE smoke mount;
- signal/termination behavior;
- process ownership capture;
- config remote enumeration;
- diagnostics sanitization.

### Adversarial cases

- plain Linux arm64 binary that executes but cannot satisfy Android/FUSE contract;
- truncated ELF;
- wrong architecture;
- fake version output;
- unsupported global flag;
- command-specific/global-help split;
- binary disappears during qualification;
- executable path replaced between hash and launch (TOCTOU).

---

## Position 3 — RUNTIME-X03: Transactional activation + rollback

### Goal
Switch runtime engines safely without reinstalling Nexus.

### State machine

```text
QUALIFIED
 -> STAGED
 -> QUIESCING
 -> ACTIVE_PENDING_VERIFY
 -> ACTIVE
```

Failure path:

```text
ACTIVE_PENDING_VERIFY
 -> ROLLBACK
 -> previous runtime
 -> RECOVERED / DEGRADED_RECOVERED
```

### Requirements

- active/previous runtime identities persisted durably;
- stop/quiesce only Nexus-owned mounts;
- no binary overwrite underneath active process;
- activation verifies candidate bytes/hash again;
- restart desired mounts after activation;
- automatic rollback on failed startup/qualification;
- preserve previous runtime until new candidate proves stable;
- reboot-safe staged activation;
- crash recovery from every transition state;
- CLI + WebUI use same activation authority.

### Commands envisioned

```text
racctl runtime list
racctl runtime inspect <id>
racctl runtime test <id>
racctl runtime activate <id>
racctl runtime rollback
```

### Negative cases

- crash between quiesce and pointer update;
- crash after pointer update before verify;
- runtime file removed after staging;
- previous runtime missing;
- one mount refuses to stop;
- new binary starts but fails RC contract;
- rollback restart partially fails.

---

## Position 4 — RUNTIME-G1: Runtime authority gate

### Gate purpose
Adversarially prove X01-X03 against production entry points.

### Gate must prove

- exactly one runtime authority in managed mode;
- provider module absent yet Nexus operates;
- CLI, daemon, WebUI and boot all select the same runtime;
- arbitrary PATH/provider state cannot override managed selection;
- real candidate import → qualify → activate → mount → rollback flow;
- rollback survives process restart/reboot simulation where practical;
- evidence refs resolve to actual binaries/manifests/logs/receipts;
- no synthetic runtime “qualified=true” object can bypass real checks.

Audit loop repeats until no adoption gap.

---

## Position 5 — SOURCE-X01: Source registry + latest/pinned semantics

### Goal
Make source selection convenient and deterministic.

### First-class sources

- latest stable `BenjiThatFoxGuy/bclone`;
- official rclone;
- NewFuture-derived Android runtime;
- arbitrary GitHub owner/repo;
- URL;
- local binary;
- local/custom source build.

### Channel semantics

- latest stable;
- pinned release;
- pinned commit;
- manual only.

### bclone requirement

The implementation must dynamically resolve latest stable bclone at check time. Never hardcode `v1.75.3`; that was merely the current release discussed.

### Required provenance

Resolve mutable refs (`latest`, tag) to immutable commit/assets before qualification and persist the resolution.

### Negative cases

- latest endpoint changes between resolve/download;
- tag retargeting;
- release missing expected asset;
- wrong repository owner;
- release prerelease/draft when stable requested;
- poisoned redirect/domain.

---

## Position 6 — SOURCE-X02: Android source-build automation

### Goal
Build latest bclone/rclone as Android arm64 when release assets are unsuitable.

### Build system

Prefer a reproducible CI builder (e.g. GitHub Actions) using Android NDK because user’s primary Termux host is arm64 and official NDK host tools are commonly x86_64-oriented.

### Requirements

- configurable repository/ref;
- resolve exact commit;
- build Android arm64 using the project’s proper Android build recipe;
- record Go version, NDK version, compiler, flags, source commit;
- produce binary + provenance manifest + SHA-256;
- feed output into the same Nexus runtime qualification pipeline;
- manual dispatch for arbitrary branch/tag/commit;
- latest bclone scheduled check/build.

### Security/negative cases

- unpinned mutable ref cannot be treated as reproducible result;
- failed or partial CI artifact rejected;
- provenance manifest hash mismatch;
- artifact built for wrong ABI;
- build says Android but ELF/imported runtime proves otherwise.

---

## Position 7 — UPDATE-X01: Runtime update manager

### Goal
Detect, qualify and stage updates without blindly altering a rooted device.

### Policies

Suggested knobs:

```text
check automatically
resolve latest
build/download automatically
qualify automatically
stage passing candidate automatically
activate automatically (optional)
restart active mounts automatically (optional)
```

Recommended default for personal use:

- check: on;
- build/download: on;
- qualify: on;
- stage: on;
- activate: at next reboot or explicit action;
- immediate active-mount restart: off.

### Requirements

- current, staged and previous runtime identities;
- no hot swap beneath active process;
- update status visible in CLI/WebUI;
- failed candidate never replaces active runtime;
- one-click rollback;
- bounded retained runtime history/GC through canonical cleanup authority;
- update checks do not leak credentials;
- network failures remain retryable without corrupting state.

### Negative cases

- download interrupted;
- malformed archive;
- hash mismatch;
- malicious path traversal/symlink;
- staged candidate removed;
- update check offline;
- source disappears;
- new candidate passes static checks but fails real mount activation.

---

## Position 8 — SOURCE-G1: Source/update supply-chain gate

### Must exercise

- actual source resolution;
- real download/build where environment supports it;
- archive validation;
- exact SHA/hash binding;
- binary qualification;
- staged update;
- activation + rollback;
- adversarial malformed/mismatched artifacts;
- durable evidence that resolves to actual bytes and execution.

Simulated GitHub/provider labels are not enough for final qualification.

---

## Position 9 — MIGRATE-X01: NewFuture migration + standalone packaging

### Goal
Move existing installations to Nexus-managed standalone state without losing configuration or leaving competing authorities.

### Migration detection

Detect at least:

- `/data/adb/modules/rclone` provider presence;
- provider `rclone.conf`;
- existing mounts;
- sync/copy jobs;
- parent WebUI/service activity;
- provider runtime version/paths.

### Migration wizard/workflow

1. inspect capabilities/state;
2. validate/import `rclone.conf` into Nexus durable config;
3. preserve encrypted config exactly;
4. discover existing provider-managed mounts/jobs;
5. offer import/review;
6. quiesce provider-owned lifecycle;
7. verify Nexus managed runtime/config;
8. start only selected Nexus-defined mounts;
9. record migration evidence;
10. report old module as no longer required, but do not silently uninstall it.

### Sync/job migration

If parent `conf/sync` / `conf/copy` or equivalent exists, import into Nexus Jobs only after semantic validation. Disable competing provider scheduler before Nexus begins executing imported jobs.

### Standalone package

Normal installation should require only `rclone_nexus`. `install-stack` should become migration/legacy behavior rather than the default, subject to canonical-scope audit.

### Negative cases

- provider disappears mid-migration;
- encrypted config fails with selected runtime;
- parent mount refuses stop;
- duplicate destination path;
- imported job conflicts with existing Nexus job;
- migration interrupted/rebooted;
- parent becomes active again after update;
- config copy succeeded but authority switch failed => rollback/fail closed.

---

## Position 10 — UX-X01: Runtime Manager WebUI + CLI

### WebUI Runtime page

Cards/flows should expose:

- active engine/version/source;
- Nexus qualification status;
- selected FUSE runtime;
- update channel/policy;
- available runtimes;
- import local binary;
- GitHub repository/source selection;
- test compatibility;
- activate;
- rollback;
- migration status;
- provenance details;
- structured errors/recovery actions.

### Example

```text
ACTIVE ENGINE
bclone <latest resolved version>
BenjiThatFoxGuy/bclone
Android ARM64
✓ CLI
✓ FUSE
✓ Nexus qualified
[Change]
```

### State-aware controls

- no Activate for unqualified candidate;
- no retry action for deterministic terminal incompatibility;
- rollback only if previous runtime is actually present and qualified;
- migration actions disabled when no provider or no applicable state;
- safe-area/mobile behavior preserved.

### CLI/API parity

WebUI must call the same canonical runtime/update/migration authority used by `racctl`, not a parallel implementation.

---

## Position 11 — RUNTIME-GRAND-G1: Standalone device/release seal

### Real-device qualification cases

At minimum:

- boot with NewFuture absent;
- latest bclone resolve/build/import/qualification;
- activate bclone;
- mount real configured remote;
- switch bclone -> official/custom rclone;
- switch back;
- failed candidate qualification;
- failed activation rollback;
- reboot with staged candidate;
- config persistence;
- encrypted config;
- FUSE persistence;
- remote browse;
- mount start/restart;
- Wi-Fi/mobile/offline recovery;
- namespace/app visibility;
- scheduled job;
- runtime update detection;
- migration from existing provider;
- old provider absent afterward;
- simultaneous mounts/jobs;
- logs/diagnostics redaction;
- crash recovery during runtime transition.

### Core invariant

At every point:

```text
number_of_active_lifecycle_authorities == 1
```

### Gate requirements

- authored after all remediation;
- exact current merged promise universe;
- historical obligations dispositioned;
- all evidence references resolve;
- evidence hashes bind current source/runtime;
- real production ingress exercised;
- negative cases executed;
- no synthetic “qualified” objects accepted;
- no dual mount/job authorities;
- runtime history/state reconstructable from canonical durable state;
- final release seal invalidated/rebuilt if any bound code/evidence changes.

---

## After RUNTIME-GRAND-G1

Only after standalone-runtime qualification succeeds:

1. refresh original GRAND-G1 real-device evidence against final current HEAD;
2. rerun adversarial gate audit loop;
3. run authoritative `./devtoolw release` seal;
4. ensure release evidence ancestry and hashes point to the final runtime architecture.
