# SOURCE-X02 — Android source-build automation

SOURCE-X02 adds a reproducible Android/arm64 build path for runtime sources whose published release assets are unsuitable. It does not create a second runtime publication authority: a successful build is verified, converted into the same immutable SOURCE-X01 resolution form, and imported through the existing RUNTIME-X02 qualifier/store.

## Build authority

`.github/workflows/runtime-source-build.yml` is the CI entry point. Scheduled runs resolve the latest stable built-in `bclone` source through the SOURCE-X01 resolver and then check out the returned full commit SHA. Manual dispatch accepts an arbitrary GitHub `OWNER/REPO` and branch/tag/commit, resolves that mutable input once to a full commit, and checks out only that immutable SHA before building.

The workflow pins the Go toolchain and Android NDK package, targets `ubuntu-24.04`, and uses the same Android arm64 recipe family used by upstream rclone: `GOOS=android`, `GOARCH=arm64`, `CGO_ENABLED=1`, the NDK `aarch64-linux-android<API>-clang` compiler, the `android` build tag, `-trimpath`, and lld-based CGO linking. The builder refuses a dirty checkout or a checkout whose `HEAD` differs from the resolved commit.

The scheduled job intentionally resolves latest bclone dynamically. No bclone release/version string is hardcoded into SOURCE-X02.

## Build bundle and provenance

`scripts/dev/runtime_source_build.py build` publishes a bundle only after the compiler exits successfully. The bundle is exactly:

- the executable Android arm64 runtime binary;
- `provenance.json`;
- `SHA256SUMS` binding both the binary and provenance manifest.

The provenance records source ID, engine, repository, requested ref, exact source commit, Go version, NDK version, compiler path/version, API level, GOOS/GOARCH/ABI, CGO state, build tags, trimpath state, build/ld flags, binary name, size and SHA-256.

A failed build never receives a complete manifest/SHA file pair, so partial CI output cannot be imported as a valid result.

## Runtime import

`racctl runtime source verify-build BUNDLE_DIR` is the local verification boundary. `racctl runtime source import-build BUNDLE_DIR` performs the same verification, persists a SOURCE-X01 `source-build` resolution bound to the registered source/repository/exact commit/digest, then calls the canonical immutable runtime qualification pipeline.

The verifier requires:

- a complete schema-v1 provenance manifest;
- a full 40-hex source commit;
- exact SHA-256 agreement among `SHA256SUMS`, `provenance.json` and the binary;
- Android/arm64-v8a + cgo + `android` tag + trimpath provenance;
- an NDK arm64 compiler target matching the declared API;
- actual ELF64 little-endian `EM_AARCH64` bytes;
- actual `/system/bin/linker64` `PT_INTERP` identity.

The final ELF checks prevent a Linux arm64 executable from being accepted merely because a manifest claims `GOOS=android`.

## Failure policy

SOURCE-X02 fails closed for mutable/unpinned result identity, missing/partial artifacts, manifest hash mismatch, binary digest/size mismatch, wrong ABI, non-Android ELF linkage, source/repository identity mismatch, and source bytes changed after verification. Those failures occur before the runtime can be published into the immutable runtime store.

## HOTFIX-03 — published build-result authority

Verified Android bundles are additionally published by the canonical workflow under immutable prerelease tag `runtime-build-v1-<source-id>-<upstream-commit>`. Publication is idempotent: an existing tag must contain byte-identical `runtime-source-build.tar` or CI fails. Production downloads are safe-extracted, re-run `VerifyBundle`, bind source ID/repository/engine/exact upstream commit, then persist a normal `source-build` resolution before entering the runtime store.


## Native ARM64 Termux build driver

SOURCE-X02 keeps the official pinned Android NDK as the ABI/sysroot authority.
Canonical CI continues to execute the NDK's own `aarch64-linux-android<api>-clang`
on its supported Linux x86_64 runner. On an ARM64 Termux device, official NDK
Linux host executables may be present but unrunnable because they are x86_64.
For rooted/device qualification only, Nexus may use a runnable native Termux
`clang` driver with `--target=aarch64-linux-android21` and the pinned NDK
`toolchains/llvm/prebuilt/<host>/sysroot`. The mode is accepted only after a
real compile+link probe emits an Android AArch64 ELF with `/system/bin/linker64`.
Provenance records `ndk_host`, `ndk_sysroot`, `compiler_mode`, and
`compiler_target`, and the production bundle verifier rejects mismatched target
or sysroot claims. This does not convert an arbitrary host clang build into a
SOURCE-X02 artifact; the NDK version/sysroot and final Android ELF identity
remain mandatory.
