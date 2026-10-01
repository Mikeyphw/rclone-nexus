# GRAND-G1 — authoritative final release seal

GRAND-G1 is full-plan position **16/16**, the separate final gate after REL-X01. It owns the executable decision of whether Rclone Nexus v0.1.0 may be sealed; the roadmap is not considered sealed merely because the overlay source exists.

The canonical `release` workflow reruns repository/module contracts, Go and Python tests, every prior milestone audit, WebUI JavaScript/security contracts in the configured `androidos` chroot, bounded failure injection, REL-X01 qualification, reproducible artifact generation, package integrity and release-document qualification. Completed private real-device evidence is a mandatory ancestor of the final gate.

## Executable real-device evidence

GRAND-G1 v3 closes the assertion-only evidence loophole. `scripts/dev/release_device_qualification.py` uses schema v3 and exposes `capture`, `status`, `run`, `resume` and `validate`; there is no free-form `record ... pass` command.

`capture` auto-discovers configured mounts through `config.snapshot` and captures exact Android/root-manager/provider/rclone identity, doctor state and per-mount namespace evidence. Each endurance case stores a chained set of machine observations. A PASS is accepted only when the case-specific verifier can derive the promised transition from those observations.

Externally disruptive Android actions are checkpointed rather than silently automated: reboot, manager/provider reload, Wi-Fi/mobile/offline, doze/charging, remote outage/auth, storage pressure, Android-user switching and scheduled-job execution produce an `awaiting-action` instruction and are verified on `resume`. Nexus-owned perturbations are executable: killed-rclone/stale-FUSE recovery, `racd` crash/restart and standalone WebUI idle/reopen.

The eleven generally applicable endurance scenarios require machine-proven PASS results. `android_user_namespace_change` alone may be skipped, and only when the harness itself observes exactly one Android user. Legacy schema-v1/v2 JSON and hand-written PASS/skip records are rejected.

## Full-roadmap closure

`release/final-seal-policy.json` maps all 24 summary promises to executable workflow ancestors. `release/roadmap-requirements.json` expands that into all 228 position-level roadmap bullets plus the GRAND-G1 narrative requirement (**229 requirements**). The final gate verifies exact text coverage and executable ancestry so prose-only claims cannot seal the release.

Real-device evidence remains gitignored. During Devtool artifact transactions, validation follows `DEVTOOL_TRANSACTION_PRIMARY_REPO_ROOT` to the primary checkout rather than fabricating/copying private device evidence into an isolated worktree.

A successful seal emits `dist/rclone-nexus-v0.1.0.zip`, `dist/SHA256SUMS`, `dist/release-manifest.json` and `dist/release-verdict.json`. The verdict binds artifact/source identity to the evidence digest and proof counts without copying device fingerprint or provider credentials into the distributable package.

## Transaction-clean apply qualification

The `grand-g1-source` apply-time workflow must leave no generated build/release drift in the isolated transaction. Release and package checks build `racctl` through a temporary prebuilt path, and the terminal `validation-output-cleanup` node restores tracked generated outputs from the transaction Git baseline while deleting only known untracked release outputs. This deliberately preserves any dirty state in the primary checkout because Devtool owns that preservation boundary. The post-commit `release` workflow remains unchanged and retains its actual release outputs.

### Scheduler restart hardening (v7)

GRAND-G1 also hardens the scheduled-job restart boundary found by authoritative Go validation. Scheduler cancellation stops discovery/dispatch but no longer cancels an already-dispatched typed `job.run`; that operation owns its own cancellable lifecycle through the control engine. This guarantees the scheduled run can durably advance `next_run` and reach a terminal journal state instead of becoming replayable solely because the scheduler loop restarted. Regressions cover both restart non-replay and cancellation during an in-flight scheduled run.

## Bootstrap/install remediation

GRAND-G1 also provides the missing first-install bridge required before real-device qualification. `./devtoolw install` builds/validates the Nexus module and installs only `rclone_nexus` through the detected manager while requiring an existing provider. `./devtoolw install-stack` preserves an existing NewFuture provider, but if module id `rclone` is absent it retrieves a digest-bearing latest release asset, verifies that the ZIP is actually module id `rclone`, installs the provider through the manager, and then installs Nexus. No workflow writes directly into `/data/adb/modules`.

Manager-native installation is explicit: Magisk uses `magisk --install-module`, KernelSU and KernelSU Next use `ksud module install`, and APatch uses `apd module install`. Unknown-compatible managers fail closed. Existing provider replacement is never implicit; direct helper use requires `--replace-provider`. `./devtoolw install-verify` is the post-reboot authority and verifies provider module/binary presence, Nexus/racctl presence, and live `racctl version`, provider and health surfaces.
