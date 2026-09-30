# REL-X01 — release qualification preparation

REL-X01 is full-plan position **15/16** and the final implementation/qualification-preparation boundary before GRAND-G1. It promotes the module to v0.1.0, adds deterministic release ZIP/checksum/manifest generation, targeted failure injection, release documentation, and the real-device qualification authority consumed by the final seal.

The original REL-X01 evidence surface was passive capture plus free-form result recording. GRAND-G1's final audit proved that insufficient for the roadmap promise to **run** real-device endurance. The same REL-X01 boundary is therefore consumed through the schema-v3 executable harness in the final tree: `capture` establishes real platform/mount provenance, `run` starts one named endurance scenario, `resume` verifies externally-driven transitions, and `validate` recomputes every case predicate from chained observations. Assertion-only PASS records are not release evidence.

The harness auto-discovers configured mounts from the typed configuration snapshot. It captures exact Android, Nexus, root-manager, provider-module and rclone identity, doctor state, and per-mount namespace claims/classes. It never bundles or edits provider credentials.

Externally disruptive actions remain explicit human/device actions rather than hidden automation. Reboot, root-manager/provider reload, Wi-Fi/mobile/offline, doze/charging, remote outage/auth recovery, storage remount/pressure, Android-user switching and scheduled-job waiting use before/after checkpoints. Nexus-owned rclone death, `racd` crash/restart and standalone WebUI idle/reopen are induced and restored by the harness from machine-observed owned identities.

GRAND-G1 requires the eleven generally applicable scenarios to PASS with valid machine proof. `android_user_namespace_change` may be skipped only when the harness itself observes a single-user device. Private device evidence remains gitignored and excluded from the reproducible release source digest.
