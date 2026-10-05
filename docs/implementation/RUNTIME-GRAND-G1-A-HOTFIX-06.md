# RUNTIME-GRAND-G1-A HOTFIX-06 — native Termux clang with pinned NDK sysroot

## Finding
The final device matrix found an installed Android NDK whose official Linux
compiler was `linux-x86_64`. On ARM64 Termux it was launched through qemu and
failed before execution because the x86_64 glibc loader was unavailable. The
old harness treated that as a hard blocker even though the NDK sysroot/target
libraries were usable by the device's native clang.

## Remediation
- Prefer the NDK host compiler whenever it is runnable.
- Otherwise, on a device with native clang, prove that clang can compile and
  link `aarch64-linux-android21` against the exact installed NDK sysroot.
- Accept the fallback only when the probe emits Android/AArch64 ELF bytes with
  `/system/bin/linker64`.
- Pass the proven compiler mode into the real SOURCE-X02 build for bclone and
  official rclone.
- Record NDK host/sysroot, compiler mode and compiler target in provenance.
- Extend the production build verifier so the alternate driver cannot weaken
  Android target or NDK-sysroot binding.
- Advance SOURCE-G1/G1-A evidence versions so prior environment-skip evidence
  is not reusable.

Canonical CI remains unchanged: it uses the pinned NDK compiler on its supported
Ubuntu x86_64 runner.
