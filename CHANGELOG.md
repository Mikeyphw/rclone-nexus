# Changelog

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
