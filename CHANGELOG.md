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
