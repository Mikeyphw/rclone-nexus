# RUNTIME-GRAND-G1-A HOTFIX-04 — canonical fusermount3 authority

**Campaign position:** 11/11, G1-A remediation. **Seal:** still open.

The clean providerless install exposed a real standalone defect: runtime acquisition preserved the selected rclone/bclone executable but qualification depended on `fusermount3` being present in ambient `PATH` or an old NewFuture provider module. That made the chosen main runtime source accidentally control FUSE helper availability.

The corrected invariant is:

- `fusermount3` always comes from `NewFuture/rclone-fuse3-magisk`;
- the main runtime source is independent (`bclone`, official/custom rclone, NewFuture or SOURCE-X02 build);
- Nexus downloads the current immutable NewFuture Android asset, verifies its archive digest when GitHub exposes one, extracts exactly one `fusermount3`, checks ELF/arm64 identity, content-addresses the helper and records immutable release/asset/hash provenance;
- qualification proves `fuse_helper_authority` before any Android FUSE smoke;
- managed runtime launches prepend only the Nexus-owned helper directory to `PATH`;
- managed mode does not silently borrow a helper from `/data/adb/modules/rclone` or host PATH;
- compatibility/migration mode may still use a legacy helper solely while operating on legacy provider state.

Existing RUNTIME-G1 and SOURCE-G1 device evidence is intentionally invalidated by harness-version/source-binding changes and must be recaptured before G1-B.

## Harness API closure (v3)

The provider-independent helper refactor also removes the retired `discover_fuse_helper()` call from SOURCE-G1 and GRAND-G1 automatic journeys. SOURCE-G1 now captures the Nexus-owned NewFuture helper and its immutable manifest as physical evidence rather than staging a donor helper.
