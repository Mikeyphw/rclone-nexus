# Runtime inputs native build contract

Status: current build-time authority.

Rclone Nexus keeps a singular immutable runtime at install time while making runtime acquisition a first-class repository workflow.

## Invariants

1. A finished module contains exactly one rclone-family runtime at `system/bin/rclone`.
2. Runtime provider selection is build-time only: `newfuture`, `bclone`, or explicit advanced `prebuilt`.
3. `newfuture` obtains rclone from `NewFuture/rclone-fuse3-magisk`.
4. `bclone` resolves `BenjiThatFoxGuy/bclone` to an immutable stable release commit and builds Android/arm64 with the repository-qualified NDK toolchain.
5. `system/vendor/bin/fusermount3` is always sourced from `NewFuture/rclone-fuse3-magisk`, independent of runtime provider.
6. NewFuture `libfuse*.so*` payload needed by the helper travels with the helper.
7. Runtime/provider provenance and FUSE-helper provenance are recorded independently in `runtime.provenance.json` and bound by `integrity.manifest.json`.
8. No build provider becomes a live on-device source/activation authority.

## DevTool graph

`rclone_runtime_inputs#newfuture`:

```text
contract -> materialize NewFuture -> verify
```

`rclone_runtime_inputs#bclone`:

```text
contract -> resolve immutable bclone source -> Android build -> verify
                     \-> NewFuture helper acquisition --------/
```

The main `package`, `package-bclone`, `release`, and `release-bclone` workflows depend on the corresponding cross-target runtime-input workflow before package mutation.

Atomic overlay validation uses only hermetic/offline contract tests. GitHub acquisition and NDK compilation occur after the overlay is installed, when a runtime/package/release workflow is explicitly invoked.
