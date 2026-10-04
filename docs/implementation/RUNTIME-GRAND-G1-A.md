# RUNTIME-GRAND-G1-A — resumable real-device qualification

**Campaign position:** 11 of 11, implementation half A. **Seal status:** OPEN.

G1-A implements, but does not satisfy, RNX-P467..RNX-P489. Those promises move from `UNIMPLEMENTED` to `PARTIALLY_ADOPTED` only. G1-B is the sole final-seal authority.

The private rooted-device harness composes the retained RUNTIME-G1, SOURCE-G1 and release endurance authorities and adds the missing production journeys: exact latest bclone/rclone GitHub resolution and Android-NDK builds; `runtime source import-build`; live bclone activation, bclone→official-rclone→bclone process-hash switches; failed-candidate rejection; `ACTIVE_PENDING_VERIFY` crash recovery; encrypted config plus support-bundle redaction; rooted MIGRATE-X01 inspect/preview/apply/external-disable/finalize; typed `provider.browse` against a real configured remote; and next-reboot staged-runtime activation with config persistence.

The harness is resumable because several device cases require an actual reboot, root-manager/radio/storage/user transition or scheduled-job interval. No source-only validator may promote these promises to adopted.

G1-A also narrows existing RUNTIME-G1/SOURCE-G1 device evidence source bindings to executable production/device behavior. Governance-only files are instead bound by the G1-B final source digest; this prevents the act of installing the final gate from invalidating already-captured physical observations.

### HOTFIX-01 — source identity and resumable capture

The SOURCE-G1 device harness now validates immutable identity according to the production source kind. Build-backed GitHub sources such as bclone are commit/build-authority bound and do not invent a release asset; download-backed sources still require concrete asset identity. Composite capture also reuses previously successful private device evidence only after full source/device-bound verification, so a later-stage retry does not repeat already-qualified rooted journeys.
