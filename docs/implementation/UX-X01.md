# UX-X01 — Runtime Manager WebUI + CLI

## Canonical scope

- Full-plan position: **10 of 11**
- Merged position-10 promises: **RNX-P127..RNX-P136 + RNX-P449..RNX-P466**
- Promise count: **28**
- Previous sealed position: MIGRATE-X01 at `c6d46bd`
- Next: `RUNTIME-GRAND-G1 — standalone device/release seal`

## Production authority

UX-X01 does not add a browser-side runtime model. `internal/runtimemanager` projects the existing runtime, immutable candidate store, activation transaction, source registry/resolutions, update manager and migration state into one typed `runtime.manager` query. The projection also owns action availability and reasons so clients cannot enable Activate, Rollback, Retry or migration actions from browser guesses.

`racctl runtime manager` consumes that same typed operation. The WebUI continues to use the authenticated embedded/loopback typed bridge and calls canonical operations for every mutation.

## Runtime Manager surface

The Runtime page now exposes:

- active engine/version/source and immutable provenance;
- qualification state and selected FUSE-qualified runtime;
- update source/channel/activation policy and staged/previous identities;
- available immutable runtimes, compatibility testing and qualified activation;
- source resolution, persisted immutable provenance and exact-resolution import;
- local binary import through the same runtime qualifier;
- custom GitHub release source registration/selection;
- NewFuture migration detection, explicit mount/job selection, proof-bound apply/finalize and rollback;
- structured errors with recovery actions only when the backend marks them retryable.

## State-aware safety

- unqualified candidates cannot activate;
- rollback requires the previous runtime to still exist and remain qualified;
- deterministic terminal update failures expose no retry action;
- migration review is unavailable without an applicable legacy provider/state;
- final migration authority switch is unavailable until the provider is explicitly disabled;
- no provider mount/job is selected implicitly;
- missing backend action data fails closed in the browser;
- safe-area and phone/tablet layouts remain part of the WebUI contract.

## Requalified inherited UX

RNX-P127..P136 are requalified through the existing WebUI/platform gates for mount lifecycle/health, config/profile/policy editing, namespace visibility, VFS cache actions, RC metrics, jobs, provider readiness/browsing, operation progress/cancellation, doctor/support bundles and root-manager/runtime settings.

## Validation

`ux-x01-audit` runs:

- MIGRATE-X01 predecessor assertions at progression-safe position 10;
- Runtime Manager Go action-policy tests;
- control/CLI/WebUI package tests;
- WEB-G1 and platform UX audits;
- Runtime Manager JavaScript/state contract;
- full position-10 canonical promise/evidence checks;
- a compiled `racctl runtime manager` capability/registry proof.


## UX/source-policy remediation

Runtime Manager now consumes backend-projected `source_choices` generated from the canonical `runtimesource` source-kind/channel policy. The browser no longer advertises a universal list of channels: GitHub sources expose pinned commits only when SOURCE-X02 build authority exists, NewFuture remains release-asset based, URL/local sources are manual-only, and source-build outputs expose only pinned-commit/manual semantics. Pinned choices enable and require a ref only when the source does not already carry one.

The update-policy editor now exposes `restart_active_mounts_automatically` explicitly. Immediate activation therefore has a visible, persistable control matching UPDATE-X01 validation rather than an impossible browser state. Browser policy assembly preserves non-edited canonical fields such as acquire/qualify while carrying the restart flag through `runtime.update.policy.apply`.

SOURCE-X01, UPDATE-X01 and UX-X01 gates now share an executable control-plane journey proving policy apply -> Runtime Manager projection and source capability projection -> typed source-resolution rejection for impossible combinations.
