# Canonical merged campaign scope

Campaign: `RUNTIME-STANDALONE`
Canonical promise range: **RNX-P001..RNX-P514**.

The machine-readable authority is `release/canonical-promise-ledger.json`. The roadmap-to-ledger compiler is `scripts/dev/check_canonical_scope.py`; non-bullet roadmap clauses are dispositioned in `release/canonical-roadmap-obligation-dispositions.json`. The former 24-row GRAND-G1 table is a historical summary only and cannot be used as the current campaign promise count.

## Source families

| Family | Count | Range |
| --- | ---: | --- |
| Original detailed roadmap | 229 | RNX-P001..RNX-P229 |
| Install-stack amendments | 7 | RNX-P230..RNX-P236 |
| Guided mount remediation | 21 | RNX-P237..RNX-P257 |
| Device runtime/WebUI remediation | 21 | RNX-P258..RNX-P278 |
| Runtime-standalone roadmap | 236 | RNX-P279..RNX-P514 |

The runtime-standalone family consists of **222 direct position bullets plus 14 previously unnumbered normative obligations**. Existing IDs are never renumbered; the supplemental closure is appended as RNX-P501..RNX-P514 and mapped back to the exact roadmap prose/numbered clauses.

## Supplemental non-bullet closure

- RNX-P501..P502 — dynamic bclone latest resolution and immutable mutable-ref provenance persistence.
- RNX-P503..P513 — positive NewFuture migration workflow, sync/job handoff, durable migration evidence and standalone-default packaging.
- RNX-P514 — WebUI/CLI runtime/source/update/migration authority parity.

`RNX-P508` is intentionally `PARTIALLY_ADOPTED`: selection/import authority exists, but the newly canonical wording requires a fresh executable proof that final authority switch starts **only** explicitly selected Nexus mounts. The scope compiler therefore reopens the prior MIGRATE-X01 seal instead of laundering the newly discovered obligation as already qualified.

Every non-bullet clause inside a roadmap position—including numbered steps, prose, goals, wrappers and fenced examples/state machines—must have an explicit disposition. A new or changed clause without one makes `canonical-scope-audit` fail. Guidance/illustrative text is classified explicitly rather than silently ignored.

## Install-stack amendments

1. Devtool-native install, install-stack, install-status and install-verify remain supported while standalone migration is incomplete.
2. Root-manager staging semantics distinguish staged success from post-reboot installed success.
3. Post-reboot verification proves the installed module rather than only installer output.
4. Installed integrity distinguishes runtime-installed files from installer-only archive files.
5. Provider layout verification resolves the intended provider-owned binary rather than an unrelated PATH executable.
6. Install verification does not allow accidental PATH shadowing to stand in for the selected provider/runtime.
7. Devtool transaction handling preserves user dirty tracked state; this is exported to Devtool's transaction authority and is not claimed as locally implemented by this repository.

## Seal disposition

The legacy GRAND-G1 seal definition is invalid for current HEAD once this merged scope exists. It may be audited as historical evidence, but it must not produce a current `sealed` verdict while mandatory RUNTIME-STANDALONE promises remain open. The current final seal must be authored last after standalone remediation and production adoption.
