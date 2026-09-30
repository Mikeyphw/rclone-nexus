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
