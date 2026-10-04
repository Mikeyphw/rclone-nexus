# UPDATE-X01 HOTFIX-02 — Independent acquisition / qualification

## Gap closed

`qualify_automatically=false` previously still entered `runtimestore.Import`, which ran the full qualifier before UPDATE-X01 inspected the policy flag. The operation then reported `candidate-failed` despite having already qualified the candidate.

## Production correction

- `runtimestore.Acquire` owns immutable byte/provenance acquisition and writes a `pending` qualification manifest.
- `runtimestore.Import` remains backward-compatible acquire+qualify by calling `Acquire` then `Test`.
- `runtimesource.AcquireResolution` exposes the acquisition-only source boundary.
- UPDATE-X01 uses `AcquireResolution`, returns durable `acquired` success when automatic qualification is disabled, and invokes `runtimestore.Test` only when qualification is enabled.
- staging remains impossible unless policy validation enables both acquisition and qualification.

## Behavioral proof

`TestAcquireWithoutAutomaticQualificationStopsAtAcquired` runs a real local source through UPDATE-X01 with `acquire=true`, `qualify=false`, `stage=false`, then proves the candidate exists, remains `qualification.state=pending`, and no active/staged runtime authority was created. `TestAcquirePublishesPendingCandidateWithoutRunningQualifier` independently proves the store boundary.


## Validator portability hardening

The acquire-only regression deliberately uses the already-built Go test executable as immutable input bytes. It does not spawn a nested compiler because acquisition must not depend on runtime qualification semantics. The artifact gate also propagates subprocess exit codes without emitting Python `CalledProcessError` tracebacks, keeping validation output safe for terminal/TUI renderers while preserving the original failing command output.

## Sealed RUNTIME-X02 invariant

The historical X02 gate now distinguishes the new acquisition boundary from the normal import contract: `Acquire` must publish explicit `qualification.state=pending`, `Import` must call `Test`, and `Test` remains the only boundary that invokes the canonical qualifier. The acquire-only regression is therefore not treated as an import bypass.
