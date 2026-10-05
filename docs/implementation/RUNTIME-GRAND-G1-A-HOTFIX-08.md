# RUNTIME-GRAND-G1-A HOTFIX-08 — native Termux clang + pinned NDK runtime resources

## Finding

G1-A's Termux-native SOURCE-X02 fallback correctly avoided executing the NDK's x86_64 host compiler and correctly bound native clang to the pinned NDK sysroot, but native clang still used its own compiler resource directory. On ARM64 Termux that caused Android links to request `libunwind.a` from the host clang runtime layout and fail even though the pinned NDK already contained the correct Android/AArch64 compiler-rt and unwind libraries.

## Remediation

For `native-clang-ndk-sysroot` builds Nexus now discovers the pinned NDK Clang resource directory containing both the Android AArch64 compiler-rt builtins and `libunwind.a`, passes `-resource-dir=<ndk-resource-dir>` to compile/link probes and CGO builds, and records the resource directory plus SHA-256 identities of those runtime libraries in build provenance.

The production bundle verifier fails closed unless native-clang provenance binds the declared NDK host, sysroot, Clang resource directory, compiler target, compiler-rt digest and libunwind digest. Official SOURCE-X02 CI remains on the NDK prebuilt compiler path.

## G1-A effect

The real-device probe still requires a successful linked Android/AArch64 ELF before declaring the native Termux toolchain runnable. No device promise is advanced by source-only validation; this only removes the incorrect host-runtime dependency blocking the real bclone/rclone builds.
