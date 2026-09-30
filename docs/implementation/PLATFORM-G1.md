# PLATFORM-G1 — diagnostics and portability qualification gate

Full-plan position **10/16**. This is a separate qualification gate over
PLATFORM-X01 and inherited platform-facing behavior; it adds no WebUI product
scope.

## Audit findings remediated before seal

1. **Integrity-before-migration ordering.** `post-fs-data` previously migrated
   persistent state before verifying the newly installed module. A corrupt or
   incomplete update could therefore touch schema state before being rejected.
   Integrity now fails closed first; migration runs only after the package
   verifies. The installer also verifies integrity after checking upgrade-state
   compatibility, and a missing integrity manifest is a hard failure on this
   command path.
2. **Unknown-manager capability overclaim.** A generic `/data/adb/modules`
   layout previously implied service, post-fs-data, action and uninstall hooks.
   Those are not proven by the layout alone. Unknown-compatible managers now
   expose only the common module-layout capability (plus update staging when
   independently observed); embedded WebUI and lifecycle hooks remain false.
3. **Diagnostic file ownership.** Existing diagnostic/support files with loose
   modes or foreign ownership could retain those attributes when reused. Nexus
   now repairs event logs, rotated diagnostics and deterministic support bundles
   to mode `0600`, and to `root:root` whenever running as root.

## Qualification

The gate verifies:

- full Go and Python compatibility suites;
- PLATFORM-X01 secret/path redaction and deterministic support bundles;
- root-manager fixtures for Magisk, KernelSU/KernelSU Next, APatch and
  conservative unknown-compatible managers;
- fail-closed missing/corrupt integrity manifests;
- `verify-integrity -> migrate -> platform-ready` boot ordering with a portable
  lifecycle fixture, including proof that failed integrity never invokes
  migration;
- rollback-safe platform-state migration and newer-schema rejection;
- default uninstall state preservation plus explicit one-shot purge;
- provider-module non-mutation;
- static pre-WebUI substrate without a shell/HTTP privilege bridge;
- reproducible Android arm64 backend and flashable package integrity contract.

Real root-manager/device qualification remains truthful: manager-specific
runtime claims are made only when matching privileged evidence is available;
portable fixtures qualify package/hook contracts without inventing device
results.
