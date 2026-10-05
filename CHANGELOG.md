## RUNTIME-GRAND-G1-A HOTFIX-06 — native Termux clang + pinned NDK sysroot
## RUNTIME-GRAND-G1-A HOTFIX-07 — qualification feedback + namespace schema alignment

- Added human-readable G1-A phase/step purposes, evidence reuse reasons, and structured actionable failure diagnostics.
- Added `--verbose` command/observation diagnostics while keeping useful `Why` context on by default.
- Fixed RNX-P482 release-device validation to accept omitted empty `achieved_classes`, matching the Go `omitempty` namespace schema.
- Namespace query failures now retain the attempted binary, privilege mode, exit status, stdout/stderr, expected schema, evidence path, and next action instead of collapsing to `malformed`.
- Kept standalone RUNTIME-G1/SOURCE-G1 harness source stable so valid expensive underlying evidence remains reusable.


- fixed final-device SOURCE-X02 builds on ARM64 Termux when the installed official NDK exposes only an unrunnable `linux-x86_64` compiler;
- added a fail-closed native clang fallback that must compile+link a real Android arm64 probe against the pinned NDK sysroot before it is considered supported;
- extended SOURCE-X02 provenance and verification with NDK host/sysroot plus compiler mode/target;
- kept canonical CI on the official pinned NDK compiler while allowing device qualification to use the pinned NDK data with a native driver;
- advanced SOURCE-G1 and G1-A evidence versions so earlier `NDK present but unrunnable` evidence is stale.


## RUNTIME-GRAND-G1-A HOTFIX-05 — GitHub rate-limit resilient bootstrap

- Added shared authenticated GitHub REST transport with redacted rate-limit diagnostics.
- Added optional Nexus-owned `config/github.token` (0600) plus environment-token support.
- Applied the same auth/rate-limit boundary to source metadata, runtime asset downloads, and NewFuture `fusermount3` acquisition.
- Added `racctl runtime update retry` to retry the exact persisted immutable resolution/candidate without a fresh metadata lookup.
- Made GitHub 403/429 rate limits explicitly retryable rather than generic permanent failures.
## 2026-10-04 — RUNTIME-GRAND-G1-A canonical NewFuture fusermount3 hotfix
- RUNTIME-GRAND-G1-A HOTFIX-04 v3: close stale SOURCE-G1/GRAND-G1 `discover_fuse_helper()` calls after canonical NewFuture helper takeover; SOURCE-G1 now evidence-binds the Nexus-owned helper and manifest.

- Made `fusermount3` a provider-independent Nexus runtime dependency sourced canonically from `NewFuture/rclone-fuse3-magisk`, regardless of whether the selected main runtime is bclone, official/custom rclone, NewFuture-derived, or SOURCE-X02-built.
- Added immutable release/asset/archive/helper provenance and content-addressed Nexus-owned helper publication.
- Injected the managed helper path into qualification and production mount processes; managed mode no longer falls back to legacy provider/PATH helpers.
- Invalidated donor-based RUNTIME-G1/SOURCE-G1/composite device evidence and strengthened G1-A proof to bind the NewFuture helper manifest and bytes.

## 2026-10-04 — RUNTIME-GRAND-G1-A source-bound release racctl hotfix

- Final release-device qualification now uses the exact current `gate-racctl` binary built, hashed, and retained by RUNTIME-G1 instead of an arbitrary PATH/installed CLI.
- Reject stale/missing gate-racctl evidence before release metadata capture.
- Advance the composite G1-A harness to v3 without invalidating independently valid RUNTIME-G1/SOURCE-G1 physical evidence.


## 2026-10-04 — RUNTIME-GRAND-G1-A root-owned release metadata hotfix

- Fixed final device qualification to read root-owned Nexus/root-manager/runtime-authority metadata as root before accepting a Termux user-shell view.
- Added canonical `/data/adb` root-manager CLI probes for version evidence and precise readiness diagnostics.
- Advanced release-device qualification evidence to harness v4 so stale pre-fix metadata cannot satisfy the final device gate.
## 2026-10-04 — RUNTIME-GRAND-G1-A resumable device qualification

- Implemented the position-11 rooted/device qualification matrix without sealing the campaign.
- Added real latest bclone/rclone Android builds, live runtime switching, rooted migration, encrypted-config/support-bundle redaction, crash recovery, configured-remote browsing and staged-next-reboot proof.
- Marked RNX-P467..RNX-P489 honestly `PARTIALLY_ADOPTED`; G1-B remains the only final-seal authority.
- Refined RUNTIME-G1/SOURCE-G1 private evidence bindings so final governance-only gate changes do not stale physical device observations.

## 2026-10-04 — WebUI-X/MMRL home-screen shortcut metadata

- Added minimal WebUI-X `config.json` metadata for `rclone_nexus` with a packaged 512×512 shortcut icon, allowing compatible WebUI-X/MMRL hosts to expose their native create-shortcut flow.
- Reused the shortcut icon as the WebUI favicon and made module/package/final-WebUI qualification require the shortcut metadata.
- Kept shortcut creation host-owned: no new generic JavaScript/native privilege bridge or bindhosts-specific shortcut interface was introduced.

## RUNTIME pre-seal adoption/gate audit

- freshly requalified 146 legacy source-only roadmap promises on current merged production paths while leaving 51 device-dependent promises and RNX-P229 explicitly pending;
- introduced the P514-aware RUNTIME-GRAND-G1 policy and permanently fenced the predecessor GRAND-G1 gate from becoming current again;
- reconciled active roadmap documentation so historical 16/16 wording cannot be mistaken for current seal state;
- pinned SOURCE-X02 GitHub Actions dependencies to full release commit SHAs and retained CI runner-image identity in build provenance;
- added a dedicated pre-seal audit workflow that validates canonical scope, adoption classification, current final-gate design, cross-gate source/update/UX behavioral proof, documentation truth, and source-build CI immutability.
- repaired SOURCE-G1 gate topology drift so it proves the post-HOTFIX-03 `runtimeacquire` authority and current build-authority regression instead of requiring the retired direct `ImportResolution` call graph.

## UX/SOURCE-POLICY HOTFIX-04 — Runtime Manager source/update semantic closure

- expose and persist `restart_active_mounts_automatically` so immediate activation is actually configurable from Runtime Manager;
- replace the universal source-channel selector with backend-projected source-kind capabilities;
- fail closed on impossible source/channel combinations before network resolution;
- add behavioral UI-model/control/source/update round-trip proof to SOURCE-X01, UPDATE-X01 and UX-X01 gates.

## SOURCE/UPDATE HOTFIX-03 — automatic Android build-result bridge and pinned-commit import

- make durable SOURCE-X02 build publication Termux-safe by atomically renaming the verified same-directory temporary file instead of requiring `link(2)`/hard-link permission;
- Persist verified SOURCE-X02 build bytes into durable content-addressed Nexus source state so canonical `source-build` resolutions remain replayable after download cleanup.
- publish verified SOURCE-X02 Android bundles as immutable commit-keyed prerelease assets;
- add `runtimeacquire` as the shared CLI/control/updater acquisition authority;
- make built-in bclone require the verified Android build rather than a Linux release asset;
- route GitHub pinned commits through SOURCE-X02 build authority and make missing builds retryable;
- verify published tar safety, provenance, commit, repository, engine, hashes and Android ELF before runtime-store publication.

## UPDATE-X01 HOTFIX-02 — independent acquisition/qualification policy semantics

- split immutable runtime acquisition from qualification in the runtime store while preserving `runtime import` as acquire+qualify;
- make `qualify_automatically=false` stop successfully at durable `acquired` state without invoking the qualifier or staging/activation;
- add regressions proving an acquisition-only candidate remains `qualification.state=pending` and cannot mutate activation authority.

## MIGRATE-X01 selected-mount finalization hotfix

- close RNX-P508 with direct finalization behavior rather than ledger inheritance;
- add a two-provider-mount migration regression proving exactly one reviewed mount is imported disabled, started through the production Nexus lifecycle at finalization, and the unselected mount remains not-configured;
- strengthen the compiled `racctl migration` gate to exercise the same selected-vs-unselected final authority switch and durable evidence binding;
- remove RNX-P508 from the canonical scope reopened set without changing the RNX-P001..RNX-P514 universe.

## Canonical scope hotfix — complete non-bullet roadmap universe

- replace the hardcoded RNX-P001..P500 assumption with a contiguous dynamic canonical range;
- disposition every non-bullet RUNTIME-STANDALONE roadmap clause, including numbered workflows and fenced invariants/examples;
- append RNX-P501..RNX-P514 for 14 genuinely missing normative obligations without renumbering historical promises;
- make future unclassified prose/numbered/code roadmap additions fail canonical-scope validation;
- reopen RNX-P508 as PARTIALLY_ADOPTED rather than falsely inheriting the old MIGRATE-X01 seal.



## UX-X01 — Runtime Manager WebUI + CLI

- Added a canonical `runtime.manager` projection across runtime/source/update/migration authority with backend-owned action availability.
- Added typed compatibility test, source register/resolve/import-resolution/local-import operations for WebUI/CLI parity.
- Expanded the Runtime page with active provenance, qualification/FUSE status, update policy/staging, source resolution/import, available runtimes, migration review/finalization, and structured recovery actions.
- State-aware controls now fail closed for unqualified activation, terminal retry, unavailable rollback and inapplicable migration actions.
- Requalified merged position 10: RNX-P127..P136 and RNX-P449..P466 (28 promises).

## SOURCE-G1 canonical closure

- Correct the SOURCE-G1 cumulative seal from RNX-P374..P425 (52) to RNX-P374..P434 (61).
- Promote RNX-P426..P434 only through the mandatory rooted/network behavioral validator already proving those obligations.
- Add explicit gate-owned promise-to-device/test mappings so later audits cannot regress to a truncated position-8 scope.
## SOURCE-G1 v5 — canonical resolution-state evidence paths

- Fix rooted SOURCE-G1 evidence capture to use the production `Paths.Normalize()` layout: `$RNEXUS_STATE_DIR/runtime/sources/resolutions/<resolution-id>.json`.
- Snapshot bclone/rclone/NewFuture latest resolutions immediately after production resolution and the historical pinned NewFuture resolution immediately after import, before later update/rollback/adversarial state transitions.
- Add regressions that reject the former invented `$RNEXUS_STATE_DIR/runtime-sources/resolutions/` path and malformed resolution IDs.
- Bump the private SOURCE-G1 evidence harness to v4; behavioral seal semantics and the RNX-P374..RNX-P425 promise universe are unchanged.

## SOURCE-G1 v4 — runnable NDK host qualification

- distinguish an NDK compiler that merely exists on disk from one that is actually runnable in the current validation environment;
- probe the exact `aarch64-linux-android21-clang --version` executable before making the real SOURCE-X02 local build proof mandatory;
- record installed-but-unrunnable host prebuilts (such as `linux-x86_64` under native Termux/aarch64 without a usable x86_64 loader) as an explicit environment limitation rather than a supply-chain failure;
- keep the real build fail-closed once the compiler probe succeeds, while leaving the mandatory external resolution/download/qualification/stage/activation/rollback proof unchanged.

## SOURCE-G1 v3 — real historical-to-latest transition proof

- replace the device-dependent installed-runtime baseline with a real prior stable NewFuture release resolved through the production `pinned-release` authority;
- import and Android/FUSE-qualify that historical asset through `runtime source import-resolution`, activate it, and prove its bytes through the production mount process;
- require the update manager to resolve/acquire current NewFuture latest and stage genuinely distinct bytes before activation, then prove byte-level activation and rollback to the historical baseline;
- persist and physically verify the historical baseline resolution alongside the canonical latest bclone/rclone/NewFuture resolution snapshots.

## SOURCE-G1 v2 — rooted mount fixture authority correction

- Correct the SOURCE-G1 isolated rclone config to define the `runtimeg1` local backend actually referenced by the production mount definition.
- Add an active-runtime `lsf runtimeg1:` preflight that must observe the gate proof file before `mountctl start`, so fixture/config divergence fails explicitly before mount lifecycle proof.
- Add regression coverage for the canonical remote name, production-runtime preflight invocation, and fail-closed missing-remote behavior.

## UPDATE-X01 runtime update manager

- added persisted safe update defaults (automatic check/acquire/qualify/stage, next-reboot or explicit activation, immediate active-mount restart off);
- added daemon-owned update scheduling, CLI/WebUI status/actions, staged next-boot activation, one-click rollback, and protected runtime-history GC;
- taught runtime import to hash and safely extract ZIP release assets while rejecting interrupted/malformed/hash-mismatched/traversal/symlink/ambiguous inputs;
- made offline/network failures retryable without corrupting activation state and sanitized URL credentials/query material from update-state errors.

# Changelog
- SOURCE-X02: added reproducible Android arm64 source-build automation, immutable commit/toolchain provenance, hash-bound build bundles, ELF/ABI verification, scheduled latest-bclone CI, manual arbitrary-ref dispatch, and canonical source-resolution/runtime-store import.

## SOURCE-X01 source registry + latest/pinned semantics

- add the root-owned runtime source registry with immutable builtin `bclone`, official `rclone`, and NewFuture definitions plus arbitrary GitHub, URL, local-binary, and source-build entries;
- add typed `latest-stable`, `pinned-release`, `pinned-commit`, and `manual-only` channel semantics, with dynamic bclone latest resolution rather than a hardcoded version;
- persist GitHub repository ID, release ID, peeled commit SHA, numeric asset ID/API URL, registry revision, source-spec digest, and available asset SHA-256 before qualification;
- make persisted resolution import reuse the X02 runtime store while enforcing expected hashes before candidate publication and restricting redirects to the selected/trusted origin;
- reject latest changes/tag retargeting as changes to future resolution identity instead of silently rewriting a prior resolution, plus missing assets, wrong repositories, stable-channel prerelease/draft responses, poisoned redirects, and mutable refs presented as pinned commits;
- make the sealed RUNTIME-G1 source audit remain valid at later campaign positions instead of hardcoding active position 4.

## RUNTIME-STANDALONE G1 v18 immutable device-evidence snapshots

- fix the v17 rooted-device evidence self-invalidation: mutable production desired-state, supervisor health, mount/service logs and boot-service artifacts are snapshotted before cleanup instead of being referenced at live paths that cleanup can legitimately mutate;
- require every mutable evidence reference to resolve under the gate-owned immutable `qualification/.../evidence/snapshot/` tree and retain its originating production `source_path`;
- bind the physical boot snapshots to `desired=running`, supervisor `state=RUNNING`, the recovered active runtime ID, and the `offline-allowed` fixture policy before the isolated mount/daemon are stopped;
- add regressions that reject direct references to mutable live-state paths or mutable snapshots without an origin binding.

# Changelog

## RUNTIME-STANDALONE G1 v17 scheduler-test lifecycle determinism

- replace the scheduler regression's fixed post-timeout sleep with explicit scheduler shutdown and durable job/journal completion checks, so detached scheduled work cannot race `testing.T.TempDir` cleanup on slower Android/Termux hosts;
- make the restart and cancellation scheduler regressions join the scheduler lifetime before advancing, preserving the production contract that dispatched jobs outlive scheduler cancellation while removing test-owned filesystem races;
- retain the v16 boot-policy correction unchanged: the rooted G1 fixture remains `network_mode=offline-allowed`, production `service.sh` keeps its single canonical reconcile path, and real rooted Android/FUSE qualification remains mandatory.

## RUNTIME-STANDALONE G1 v16 boot-policy correction

- correct the real-device G1 local-backend fixture from contradictory `require_network=false` + `network_mode=any` to authoritative `network_mode=offline-allowed`;
- remove the v15 service-loop/timing workaround from the replacement overlay so production boot continues to use the canonical daemon supervisor + native boot reconcile rather than repeated shell retries;
- bind successful boot proof to `RUNNING` supervisor health, offline-allowed policy, desired-state and health files, and emit those diagnostics on failure.


## RUNTIME-STANDALONE G1 v9 device-helper qualification fix

- stage the real `fusermount3` helper into the isolated Nexus-owned qualification module and put only that module helper path ahead of Android system paths during G1 device proof, so rclone can perform the real FUSE mount without relying on live legacy provider/PATH state.

## RUNTIME-STANDALONE G1 runtime authority gate

- adversarially qualify X01-X03 through real rooted-Android production entry points before the gate commit is allowed;
- make release/install qualification consume the canonical runtime authority and reduce NewFuture provider identity to optional compatibility evidence;
- prove providerless managed mount operation, PATH/provider/projection poison resistance, CLI/daemon/WebUI/boot convergence, distinct-byte activation and rollback, process-loss boot recovery, physical evidence resolution, and synthetic-qualification rejection;
- close the six X02 rooted-Android/FUSE obligations only from real device evidence, including an arm64 Linux probe that executes on Android but fails the FUSE contract;
- remove the qualifier's unconditional fusermount3 precondition so rooted managed qualification can operate without a legacy provider helper, while retaining safe helper/umount cleanup fallbacks;
- keep GRAND-G1 open for the later SOURCE/CONFIG/LIFECYCLE/WEB/RELEASE positions.

1 runtime/WebUI v2 promise closure

The device-runtime remediation now exports the NewFuture provider environment across all installed entry points, qualifies CLI flags against the exact selected provider executable, treats deterministic provider/config/argv failures as non-retryable without consuming restart budget, keeps transient network failures retryable, sanitizes startup diagnostics, and disables terminal retry controls in the WebUI.
# Changelog

## RUNTIME-STANDALONE X03 transactional activation + rollback

- make durable activation state the canonical managed-runtime selector while reducing `runtime/active/bin/rclone` to a compatibility projection;
- add atomic `STAGED -> QUIESCING -> ACTIVE_PENDING_VERIFY -> ACTIVE` transactions with durable receipts, active/previous identities and byte hashes;
- quiesce only proven Nexus-owned mounts, preserve desired state, requalify/re-hash candidate bytes before switching, and restart/verify desired mounts afterward;
- automatically roll back startup/RC/reconcile failures and classify incomplete rollback as `DEGRADED_RECOVERED`;
- recover interrupted STAGED/QUIESCING/PENDING/ROLLBACK states at boot before runtime readiness;
- route CLI and WebUI activation, rollback and recovery through the same typed control-engine operations and fail normal runtime/config mutation closed during a transition.


## RUNTIME-STANDALONE X02 runtime store + qualification

- add the Nexus-owned immutable `/data/adb/rclone-nexus/runtimes/<runtime-id>` candidate store with manifests binding engine, provenance, source/archive hash, binary hash, ELF metadata, import time and qualifier evidence;
- add one `racctl runtime import` authority for local files, explicit executable paths, URLs, explicit GitHub release assets, source-build outputs and NewFuture-derived candidates, plus `list`, `inspect` and `test`;
- snapshot bytes before qualification and launch all executable probes through a pinned open descriptor so source/path replacement cannot redirect qualification;
- enforce ELF/architecture/version/config/mount/generated-flag contracts and implement real Android root/FUSE + RC + process-identity + SIGTERM smoke qualification;
- add adversarial coverage for truncated/wrong-arch/fake-version/unsupported-flag/help-split/disappearing/replaced candidates and secret-bearing diagnostics;
- keep six real-device promises `BLOCKED_BY_ENVIRONMENT` until rooted Android/FUSE evidence exists instead of synthesizing qualification.


## RUNTIME-STANDALONE X01 canonical runtime ownership

- establish explicit `managed`, `external`, and `migration-required` runtime modes with Nexus-owned runtime/config roots and one Go resolver;
- make boot, racctl/control, mount lifecycle, supervisor/readiness, jobs, diagnostics, WebUI, install verification, and release qualification consume that authority;
- reduce provider/PATH/config discovery to explicit external compatibility lowering and fail managed mode closed when an enabled legacy provider creates ambiguous lifecycle/job ownership;
- add adversarial evidence for provider removal, missing external runtime, dual authority, poisoned PATH, and poisoned `RCLONE_CONFIG`;
- bind the new campaign position to a dedicated `runtime-standalone-x01` Devtool workflow rather than reusing the historical jobs/RC `runtime-x01` name.


## v0.1.0

- GRAND-G1 full-roadmap audit now machine-covers all 229 detailed requirements, requires exact root-manager/provider versions and observed mount namespace evidence, and forbids skipping the 11 generally applicable endurance scenarios. — 2026-09-30

- Added native typed `racctl`/`racd` control plane and persistent operation journal.
- Added transactional mount configuration, independent lifecycle, readiness, self-healing and restart budgets.
- Added Android namespace discovery/owned propagation, resource policy, VFS profiles and bounded cache management.
- Added managed sync/copy/check scheduling and authenticated local-only rclone RC metrics.
- Added doctor, rotating redacted logs, deterministic support bundles and Magisk/KernelSU/APatch capability handling.
- Added integrity-first upgrade lifecycle and explicit uninstall purge semantics.
- Added secure standalone and embedded WebUI transports plus Mounts, Jobs, Runtime, Logs, Doctor, Remotes and Settings workflows.
- Added release qualification, failure-injection harness, real-device evidence schema, reproducible v0.1.0 module packaging and checksums.
- Added the GRAND-G1 authoritative final seal: complete device evidence, all prior gate audits, chroot WebUI contracts, package/source identity, and every roadmap promise now converge on one fail-closed release verdict.

## GRAND-G1 v6 transaction-clean qualification

- Release qualification now builds `racctl` through a temporary prebuilt path instead of mutating the tracked Android build cache.
- The apply-time GRAND-G1 source workflow ends with an explicit generated-output cleanup that restores tracked outputs to the isolated transaction baseline and removes untracked release outputs.
- This preserves Devtool primary-checkout dirty paths (including an intentional deletion of `build/android/arm64-v8a/racctl`) without weakening release/package validation.

## GRAND-G1 v7 scheduler restart convergence

- scheduled jobs already dispatched by `racd` are no longer cancelled when only the scheduler lifetime ends;
- scheduler restart therefore cannot cancel a due run before its durable `next_run` claim and accidentally make the same occurrence replayable;
- add a regression proving scheduler cancellation during an in-flight scheduled run still reaches a durable successful terminal state and operation-journal completion.

## WebUI guided mount creation remediation

- replace the flat mount form with progressive Source, Destination, Performance, Behaviour and Advanced sections plus a human-readable Review/Create step;
- add non-mutating `config.validate` with multi-issue, field/category/severity/suggestion error payloads while retaining `config.preview` + one-use proof as the only mutation authority;
- integrate configured remote discovery/browsing and connection testing directly into mount creation without exposing provider credentials;
- surface resource-aware VFS profile recommendations, mountpoint suggestions/conflict feedback, policy explanations, preview expiry countdown, apply progress and partial-success recovery actions;
- protect dirty forms from backdrop/Escape loss, improve mobile full-screen/sticky actions and modal keyboard focus, and give deletion its own explicit review semantics;
- restore the cache package accidentally deleted by the preceding `fix` commit, byte-for-byte from its parent, so the production tree compiles before this remediation is validated.

## GRAND-G1 install/bootstrap remediation

- add Devtool-native `install`, `install-stack`, `install-status`, and `install-verify` workflows for the actual NewFuture provider + Nexus two-module stack;
- install modules only through detected Magisk/KernelSU/KernelSU Next/APatch manager CLIs, never by copying into `/data/adb/modules`;
- preserve an already-installed third-party provider by default and require an explicit provider replacement request;
- when bootstrapping a missing provider, require GitHub-provided SHA-256 asset metadata and verify root-level `module.prop` id `rclone` before manager installation;
- verify staged/active module identity, reboot requirement, provider binary, Nexus `racctl`, and post-reboot runtime surfaces.

## WebUI guided mount creation promise-closure remediation v2

A post-implementation promise audit tightened the guided mount editor so the delivered UX matches the full design contract rather than only the initial functional surface. Structured validation issues now identify the affected mount as well as the field, preventing errors from other registry entries from being attached to the mount currently being edited. Destination readiness no longer claims a location is available until backend validation has completed.

The source step now surfaces provider rclone/FUSE/config readiness, configured-remote identity, and explicit path reachability results. Device VFS recommendations display observed RAM/cache-free context; named profiles synchronize their effective values into the candidate and disable raw custom controls while the profile owns those settings. Review now includes auto-start, Android visibility, read/write mode, remote probing and resource policy, plus an explicit destructive/non-destructive safety summary and clearer proof-expiry guidance.

Dirty-form protection now survives asynchronous helper loading without re-baselining user edits, nested confirmation focus is trapped correctly, and partial lifecycle failures expose direct Retry start, Edit mount, View operations and View logs recovery actions. The WebUI validation contracts were expanded to prove these closure requirements.

## GRAND-G1 device runtime + mobile diagnostics remediation

- remove the obsolete `--rc-no-open-browser` launch argument and preflight generated mount flags against the selected provider's actual `rclone mount --help` contract before process start;
- make NewFuture `system/vendor/bin` binaries authoritative ahead of unrelated PATH tools in both Go and shell discovery, while inheriting the provider-owned environment/config contract;
- preserve startup exit, bounded diagnostic detail, category/stage/retryability and exit code from lifecycle through supervisor/control/WebUI instead of collapsing terminal CLI failures into `REMOTE_OFFLINE` or generic `operation_failed`;
- prevent non-retryable startup failures from consuming restart budget, while retaining bounded retry/backoff for transient network/remote failures;
- let `offline-allowed` mounts cold-start from cache while locally offline even with remote probing configured, and add IPv6/VPN/active-interface network fallbacks;
- parse real rclone timestamp/severity prefixes, collapse CLI-help floods into bounded expandable diagnostics, and add source/severity/search filtering;
- gate namespace visibility controls on an actually live Nexus-owned source mount, make lifecycle controls state-aware, collapse the healthy compatibility banner, and add Android safe-area plus scroll/snap mobile navigation handling.

### GRAND-G1 device runtime/WebUI remediation v3

- Make the provider CLI qualification fixture Android/Termux hermetic when PATH is intentionally cleared.
- Qualify generated mount argv against both command-local and global rclone help instead of incorrectly treating `mount --help` as the complete CLI contract.

## SOURCE-G1 — source/update supply-chain authority gate

- Added a real rooted-Android/network qualification gate that resolves live GitHub release identities, acquires and qualifies an external Android runtime, stages it without hot-swapping an active mount, activates it, and proves one-click rollback by live process hashes.
- Added production-path negative qualification for SHA-256 mismatch, malformed ZIP, traversal/symlink archives, and disappeared/offline sources with authority-preservation and retryability checks.
- Made NewFuture a genuinely importable first-class source by selecting `magisk-rclone_arm64-v8a.zip` instead of stopping at metadata resolution.
- Added optional real SOURCE-X02 NDK compile proof when an Android NDK arm64 compiler is present in the qualification environment.
- SOURCE-G1 private evidence is gitignored, excluded from release identity, audited before cleanup, and never committed.

## MIGRATE-X01 — NewFuture migration + standalone packaging

- Added durable, proof-bound NewFuture migration authority with read-only provider
  discovery, exact config preservation, reviewed disabled imports, explicit
  provider-disable handoff, transactional finalization and rollback/recovery.
- Added detection for provider runtime/version, active service/WebUI/mounts and
  NewFuture sync/copy definitions without sourcing provider configuration.
- Added `racctl migration` inspect/status/preview/apply/finalize/rollback/recover
  production ingress and daemon-start migration recovery.
- Kept normal installation providerless; legacy `install-stack` remains explicit.
- Converged all 35 canonical position-9 promises (`RNX-P106..P126` and
  `RNX-P435..P448`) under executable `migrate-x01-audit` evidence.

## 2026-10-04 — RUNTIME-GRAND-G1-A source identity/resume hotfix

- Fixed SOURCE-G1 real-source qualification to honor the canonical distinction between build-backed and download-backed GitHub sources: bclone binds immutable repository/release/commit plus SOURCE-X02 build authority and intentionally has no release asset, while rclone/NewFuture continue to require concrete immutable download assets where applicable.
- Made the composite G1-A capture resumable across later-stage failures by validating and reusing still-current private RUNTIME-G1/SOURCE-G1/release-device evidence instead of rerunning successful rooted/device qualification.

- RUNTIME-GRAND-G1-A HOTFIX-08: bind native Termux clang to the pinned NDK Clang resource directory as well as the NDK sysroot, so Android compiler-rt/libunwind are resolved from the NDK rather than the Termux host toolchain; record resource-dir and runtime-library digests in SOURCE-X02 provenance.
