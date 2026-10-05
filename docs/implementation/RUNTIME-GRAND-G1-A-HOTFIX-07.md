# RUNTIME-GRAND-G1-A HOTFIX-07 — human-readable qualification feedback

This remediation makes the G1-A device qualification usable as an operator-facing tool rather than a CI-only harness.

## Behavior

- Default progress output now explains **what** each phase/step is proving and **why** it matters.
- Reused private evidence reports the validation reason and exact evidence path.
- Failures can carry structured `Problem / Blocks / Why / Expected / Observed / Command / Evidence / Next` context.
- `--verbose` adds command/observation diagnostics without making terse CI tokens the only source of truth.
- Release-device namespace queries retain exit/stderr/JSON parsing diagnostics instead of collapsing failed commands to `{}`.

## RNX-P482 namespace contract correction

`internal/namespace.Inspection.AchievedClasses` is serialized with `omitempty`. A valid `service_only` inspection can therefore omit `achieved_classes` when the set is empty. The Python release-device consumer previously required the key unconditionally and reported only `namespace visibility evidence is malformed: <mount>`.

The consumer now requires:

- a non-empty `claim`;
- a `visibility` array;
- `achieved_classes` to be an array **if present**.

This aligns the consumer with the production Go schema and preserves real namespace/app-visibility proof for RNX-P482.

## Evidence reuse

The hotfix deliberately does not modify the standalone RUNTIME-G1 or SOURCE-G1 harness source files. Existing expensive private evidence for those scopes remains eligible for normal source/device/hash validation and reuse. The GRAND-G1 composite is refreshed because its wrapper/release consumer changed.
