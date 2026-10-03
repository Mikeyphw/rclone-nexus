
### GRAND-G1 runtime/WebUI v2 promise closure

The device-runtime remediation now exports the NewFuture provider environment across all installed entry points, qualifies CLI flags against the exact selected provider executable, treats deterministic provider/config/argv failures as non-retryable without consuming restart budget, keeps transient network failures retryable, sanitizes startup diagnostics, and disables terminal retry controls in the WebUI.
# Changelog

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
