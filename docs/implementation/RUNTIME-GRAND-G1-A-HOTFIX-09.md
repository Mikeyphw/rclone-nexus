# RUNTIME-GRAND-G1-A HOTFIX-09 — Android CGO liblog linkage

## Finding

The native-Termux SOURCE-X02 path reached a real Go/CGO Android arm64 link but failed on `__android_log_vprint`. Go's Android cgo support references Android logging symbols, while the native clang driver did not automatically add `liblog`.

## Remediation

- Treat Android `liblog` as a required SOURCE-X02 CGO system-library dependency.
- Add `-llog` to the Android CGO link environment for all SOURCE-X02 builds.
- Record `android_system_libraries: ["log"]` in build provenance.
- Require native-Termux build bundles to carry explicit liblog linkage provenance.
- Strengthen the native clang toolchain probe to compile+link a tiny `<android/log.h>` program with `-llog`, so this dependency is proven before expensive source builds begin.
- Keep the pinned NDK sysroot/resource-dir/compiler-rt/libunwind authority unchanged.

This hotfix does not weaken the real-build requirement or substitute a mock.
