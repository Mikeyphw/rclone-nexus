# SOURCE-G1 — Source/update supply-chain authority gate

SOURCE-G1 seals the SOURCE-X01, SOURCE-X02 and UPDATE-X01 window only when the
production path works end to end. Static source tokens, registered providers, or
a green synthetic provider fixture are not sufficient evidence.

## Behavioral authority

The gate must execute on rooted Android and prove all of the following through
`racctl`, the same production ingress used by users and the daemon:

1. resolve the real external `bclone`, official `rclone`, and NewFuture sources;
2. bind each resolution to repository ID, release ID/tag, peeled commit SHA, and
   numeric release asset identity;
3. select NewFuture's real `magisk-rclone_arm64-v8a.zip` Android asset;
4. download the selected immutable asset through the production runtime store;
5. validate any provider SHA-256, validate the archive, extract the runtime, and
   pass the real Android execution + FUSE qualification contract;
6. stage the qualified candidate without changing the currently executing mount;
7. explicitly activate the staged runtime and prove `/proc/<pid>/exe` resolves
   to the staged binary digest;
8. one-click rollback and prove the prior runtime bytes execute again;
9. fail closed for wrong hashes, malformed archives, path traversal, symlink
   archives, and a disappeared/offline source without changing active/staged
   authority; offline acquisition must remain retryable;
10. run the SOURCE-X02 NDK builder and production bundle verifier when an Android
    NDK arm64 compiler is actually runnable in the validation environment. A
    host-incompatible NDK prebuilt that merely exists on disk is recorded as an
    environment limitation, not misclassified as supported.

The private evidence document is generated during validation, audited while its
root-owned immutable references still exist, and removed before Devtool creates
the Git commit. It is gitignored and excluded from release source identity.

## Seal

SOURCE-G1 cumulatively seals RNX-P374..RNX-P434 (61 promises). Earlier X01/X02
and UPDATE gates remain executable ancestors, but SOURCE-G1 adds the required
real external acquisition and live-execution proof that those implementation
overlays intentionally deferred.

## v2 fixture hardening

The rooted gate provisions the same `[runtimeg1]` local backend used by the sealed RUNTIME-G1 mount definition and preflights that remote through the currently active production runtime before invoking `mountctl start`. This prevents a fixture-name mismatch from being misdiagnosed as a runtime/FUSE failure.

## v3 real historical-to-latest update proof

The gate no longer assumes the device's already-installed runtime differs from the
current NewFuture release. It first resolves NewFuture latest through the production
registry, enumerates prior stable release tags only as candidates, then re-resolves
the selected historical tag through the production `pinned-release` resolver. The
historical Android asset is imported and FUSE-qualified through
`runtime source import-resolution`, activated as baseline A, and proven live through
the production mount path. Only then does `runtime update check newfuture` resolve
latest and it must stage distinct runtime bytes. Activation must execute those latest
bytes and rollback must restore the historical baseline bytes.

This makes the transition proof independent of whatever runtime happened to be
installed before validation and prevents an already-current installation from being
misclassified as a broken updater.

## v4 runnable-toolchain qualification

SOURCE-G1 no longer treats an installed NDK directory as proof that Android source
builds are supported by the current host. The gate probes the exact
`aarch64-linux-android21-clang --version` executable before declaring the build
proof supported. If the compiler cannot execute (for example an x86_64 NDK host
prebuilt under native Termux/aarch64 without a usable x86_64 loader), evidence
records the toolchain as present-but-unrunnable and the optional local NDK build
proof is skipped. If the compiler probe succeeds, the real SOURCE-X02 build remains
mandatory and any subsequent build or verifier failure still fails the gate. The
external source/update/FUSE/activation/rollback proof is never skipped.

## v5 canonical resolution evidence path

The source registry persists immutable resolution authority through `internal/paths.Paths`, under `runtime/sources/resolutions` inside the Nexus state root. SOURCE-G1 must snapshot those exact production files; it must never reconstruct a parallel `runtime-sources` path in the harness. Latest source resolutions are snapshotted immediately after resolve and the historical pinned baseline immediately after its production import, then later device evidence verifies those immutable snapshots semantically against the captured source identities.


## Canonical position-8 closure

The merged campaign scope includes nine SOURCE-G1-owned `Must exercise` promises,
RNX-P426..RNX-P434. They are sealed only by the same mandatory rooted/network
validator that proves live source resolution, real acquisition/build where the host
supports it, archive/hash binding, runtime qualification, staging, live activation,
rollback, adversarial rejection, and durable evidence resolving to actual bytes and
execution. SOURCE-G1 therefore seals 61 cumulative promises, not 52.
