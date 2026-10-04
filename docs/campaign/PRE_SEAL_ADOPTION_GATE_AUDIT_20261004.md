# RUNTIME pre-seal adoption/gate audit — 2026-10-04

## Scope

This is a remediation/audit overlay after SOURCE/UPDATE HOTFIX-03 v4 and UX/SOURCE-POLICY HOTFIX-04. It is **not** RUNTIME-GRAND-G1 and emits no final seal.

It owns the remaining handoff items 3–7: cross-gate behavioral proof, fresh legacy-adoption classification, current final-gate design, historical 16/16 documentation truth, and SOURCE-X02 CI dependency immutability.

## Audit-loop findings and remediation

### Cross-gate behavioral proof

The shared Runtime Manager source/update journey is permanently required by SOURCE-X01, UPDATE-X01 and UX-X01. The pre-seal audit additionally executes the control-plane round trip, invalid source/channel rejection, SOURCE-X02 published-build replay, acquisition-without-qualification semantics, and Runtime Manager JavaScript contract. SOURCE-X02 still proves that pinned/build-required GitHub resolutions enter the canonical build-result acquisition path.

### Gate-topology drift found by the audit loop

Two stale gate assumptions were exposed only after the newer SOURCE/UPDATE architecture was exercised end to end:

- **SOURCE-G1** still required UPDATE-X01 to call `runtimesource.ImportResolution` directly. HOTFIX-03 intentionally centralized immutable acquisition and SOURCE-X02 published-build selection in `internal/runtimeacquire`. SOURCE-G1 now proves that current topology explicitly: UPDATE-X01 must use `runtimeacquire.AcquireResolution`, retain independent `runtimestore.Test` qualification and runtimeactivation stage/activate/rollback authority, while `runtimeacquire` must route build-required GitHub resolutions through `runtimebuild.AcquirePublished` and ordinary resolutions through `runtimesource.AcquireResolution`.
- SOURCE-G1's behavioral matrix still named the predecessor latest-release regression `TestLatestStableResolvesToImmutableCommitAndAssetIDAndPersists`; the current regression is `TestLatestStableResolvesToImmutableCommitAndBuildAuthorityAndPersists`, reflecting the SOURCE-X02 build-authority bridge. The gate was updated to require the current behavior rather than a retired symbol name.

The `source-g1-source-audit` Devtool job now includes `internal/runtimeacquire/**` in its declared inputs, so changes to the canonical acquisition bridge invalidate/re-run the gate rather than escaping its fingerprint.

### Historical adoption split

The prior merged ledger left 198 original-roadmap promises as `PARTIALLY_ADOPTED`. Treating all 198 alike was too coarse.

Fresh current-source qualification yields:

- **146** legacy promises whose evidence is source/runtime executable only: requalified and promoted to `IMPLEMENTED_AND_PRODUCTION_ADOPTED`;
- **51** legacy promises whose original evidence requires real `device-evidence`: remain `PARTIALLY_ADOPTED` until current-device qualification;
- **RNX-P229**: remains `PARTIALLY_ADOPTED` because it is the historical final-seal requirement itself.

No device-dependent promise is green merely because its source-side authority passes.

### Current final authority

A latent defect was found: the predecessor `GRAND-G1` code refused the current campaign only while canonical promises were open. It could therefore become seal-eligible again after later remediation reached zero open items.

That path is now permanently fenced. `GRAND-G1` is historical-only for `RUNTIME-STANDALONE`, even if the ledger later becomes fully adopted. `release/runtime-grand-g1-policy.json` defines the current position-11 authority around **RNX-P001..RNX-P514**, including exact real-device promises P467..P489 and final-gate invariants P490..P500.

### Documentation truth

`docs/ROADMAP.md` is explicitly historical. README points to `docs/campaign/RUNTIME_STANDALONE_ROADMAP.md` as the active 11-position campaign. Historical 16/16 headings remain as evidence, but current-status text may not claim that the active campaign is complete or sealed.

### SOURCE-X02 CI immutability

GitHub action dependencies are pinned to full release commits rather than mutable major tags:

- actions/checkout v7.0.1 → `3d3c42e5aac5ba805825da76410c181273ba90b1`
- actions/setup-go v7.0.0 → `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e`
- actions/upload-artifact v7.0.0 → `bbbca2ddaa5d8feaa63e36b76fdaad77386f024f`

Go, NDK and Android API remain version-pinned. GitHub-hosted runner image revision is recorded in SOURCE-X02 build provenance (`RUNNER_OS`, `RUNNER_ARCH`, `ImageOS`, `ImageVersion`) rather than silently treated as immutable.

## Remaining blockers after this audit

This overlay deliberately leaves the current final position open:

- 51 legacy device-dependent promises need fresh current-device evidence;
- RNX-P229 needs the current final seal rather than historical GRAND-G1;
- RNX-P467..P489 require the RUNTIME-GRAND-G1 real-device matrix;
- RNX-P490..P500 are final-gate invariants and remain `UNIMPLEMENTED` until that gate is authored/executed last.

The next major scope is therefore **RUNTIME-GRAND-G1**, but only after this pre-seal audit applies successfully.
