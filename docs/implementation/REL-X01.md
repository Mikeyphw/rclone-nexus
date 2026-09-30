# REL-X01 — release qualification preparation

REL-X01 is full-plan position 15/16 and the final implementation overlay before GRAND-G1.

It promotes the module to v0.1.0, adds deterministic release ZIP/checksum/manifest generation, explicit targeted failure injection, a real Android device-evidence capture/record/validation contract, and release-facing installation/migration/configuration/compatibility/recovery/release-note documentation.

Desktop/sandbox validation does **not** fabricate reboot, doze, root-manager restart, provider-update, namespace-user, network/storage transition, or long-running simultaneous-work evidence. Those cases are recorded on the qualification Android device. A completed evidence file must have no failed or pending cases; a skipped case requires an explicit reason. GRAND-G1 owns enforcement of completed evidence and the final release verdict.

Release artifacts are byte-reproducible from identical release sources and consist of `dist/rclone-nexus-v0.1.0.zip`, `dist/SHA256SUMS`, and `dist/release-manifest.json`.
