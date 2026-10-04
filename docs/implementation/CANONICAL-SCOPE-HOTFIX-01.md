# CANONICAL-SCOPE-HOTFIX-01 — non-bullet roadmap obligation closure

## Purpose

The post-UX audit found that the canonical compiler treated only `- ` bullets in `RUNTIME_STANDALONE_ROADMAP.md` as runtime-standalone promises. Normative prose and numbered workflows could therefore exist outside the RNX universe even while the ledger claimed to be exact.

This hotfix closes that defect without renumbering any existing promise.

## Result

- Previous canonical maximum: RNX-P500.
- Newly numbered normative obligations: RNX-P501..RNX-P514.
- Current canonical maximum: **RNX-P514**.
- Runtime-standalone source-family count: **236 = 222 direct bullets + 14 supplemental promises**.

The supplemental promises cover two SOURCE-X01 invariants, eleven MIGRATE-X01 workflow/package invariants and one UX-X01 CLI/API-parity invariant.

## Compiler behavior

`check_canonical_scope.py` now:

1. derives the maximum/count from the ledger rather than hardcoding 500;
2. verifies contiguous RNX-P001..max identity;
3. extracts every direct bullet under roadmap positions;
4. extracts every non-bullet prose, numbered-list and fenced-code clause under roadmap positions;
5. requires the non-bullet inventory to match `canonical-roadmap-obligation-dispositions.json` exactly;
6. requires every numbered roadmap obligation to map to canonical promises;
7. verifies direct runtime-standalone ledger text exactly matches the direct roadmap bullets;
8. verifies supplemental runtime-standalone promises are exactly those declared by `new-promise` dispositions;
9. fails when new prose/numbered/code clauses are added without disposition.

## Honest reopened obligation

RNX-P508 (`start only selected Nexus-defined mounts`) is `PARTIALLY_ADOPTED`. Existing migration selection/import behavior is real, but the prior MIGRATE-X01 gate did not execute final selected-mount activation. The hotfix therefore deliberately invalidates the old 35-promise MIGRATE seal instead of automatically promoting the newly discovered requirement.

All other appended promises are bound to already-existing executable production evidence. Subsequent hotfix work can requalify P508 separately.
