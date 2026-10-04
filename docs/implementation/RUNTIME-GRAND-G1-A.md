# RUNTIME-GRAND-G1-A — resumable real-device qualification

**Campaign position:** 11 of 11, implementation half A. **Seal status:** OPEN.

G1-A implements, but does not satisfy, RNX-P467..RNX-P489. Those promises move from `UNIMPLEMENTED` to `PARTIALLY_ADOPTED` only. G1-B is the sole final-seal authority.

The private rooted-device harness composes the retained RUNTIME-G1, SOURCE-G1 and release endurance authorities and adds the missing production journeys: exact latest bclone/rclone GitHub resolution and Android-NDK builds; `runtime source import-build`; live bclone activation, bclone→official-rclone→bclone process-hash switches; failed-candidate rejection; `ACTIVE_PENDING_VERIFY` crash recovery; encrypted config plus support-bundle redaction; rooted MIGRATE-X01 inspect/preview/apply/external-disable/finalize; typed `provider.browse` against a real configured remote; and next-reboot staged-runtime activation with config persistence.

The harness is resumable because several device cases require an actual reboot, root-manager/radio/storage/user transition or scheduled-job interval. No source-only validator may promote these promises to adopted.

G1-A also narrows existing RUNTIME-G1/SOURCE-G1 device evidence source bindings to executable production/device behavior. Governance-only files are instead bound by the G1-B final source digest; this prevents the act of installing the final gate from invalidating already-captured physical observations.

### HOTFIX-01 — source identity and resumable capture

The SOURCE-G1 device harness now validates immutable identity according to the production source kind. Build-backed GitHub sources such as bclone are commit/build-authority bound and do not invent a release asset; download-backed sources still require concrete asset identity. Composite capture also reuses previously successful private device evidence only after full source/device-bound verification, so a later-stage retry does not repeat already-qualified rooted journeys.

### HOTFIX-02 — root-owned release metadata observation

- Final release-device qualification queries Nexus/root-manager/runtime-authority metadata through root first, so a successful privilege-limited Termux view cannot be mistaken for canonical `/data/adb` state.
- Root-manager version detection probes canonical manager-owned binaries under `/data/adb` before PATH aliases.
- Release-device harness identity advances to v4; older v3 evidence is stale and must be recaptured, while independent RUNTIME-G1/SOURCE-G1 private evidence remains reusable when still valid.
- Metadata readiness failures now identify the exact missing root-manager, Nexus, or runtime-authority condition instead of one generic message.

### HOTFIX-03 — source-bound racctl for release qualification

Release-device qualification now executes the exact `gate-racctl` binary built and physically hash-bound by RUNTIME-G1. It no longer accepts an arbitrary PATH/installed `racctl` as final authority, preventing stale installed CLI versions from returning an empty or outdated runtime-authority projection. The composite harness advances to v3 while valid RUNTIME-G1 and SOURCE-G1 evidence remains independently reusable.
