# PLATFORM-X01 — diagnostics, root portability and upgrade lifecycle

Full-plan position **9/16**. This implementation merges the roadmap's former
DIAG-X01, ROOT-X01 and ROOT-X02 platform boundary without pulling WebUI business
logic forward.

## Delivered

- Native structured doctor with PASS/WARN/FAIL codes and actionable guidance.
- Deterministic sanitized support bundles with bounded log tails, secret/path
  redaction, per-entry hashes and root-only file modes.
- Rotating Nexus event/runtime logs and structured operation/health transitions.
- Capability-driven root-manager detection for Magisk, KernelSU, KernelSU Next,
  APatch and conservative unknown-compatible managers.
- Read-only upgrade compatibility check during install, rollback-safe state
  migration before daemon startup, and fail-closed integrity verification.
- Explicit purge-on-uninstall arming; default uninstall preserves persistent
  configuration/cache while releasing Nexus-owned mounts/namespace binds.
- Deterministic package integrity manifest covering packaged file hash, size and
  mode, including the native `racctl` binary.
- `webroot/` platform substrate packaged without any shell/argv bridge. The real
  WebUI transport and application remain owned by WEB-X01 and later overlays.
- Provider module remains read-only and is never patched or replaced.

## Validation

`./devtoolw platform-x01` owns this boundary: root-manager fixtures, secret
canaries, bundle determinism, log rotation, migration failure rollback,
uninstall preservation/purge, module hook audit, compatibility tests,
reproducible Android backend build and flashable-package integrity verification.
