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

## Reopened obligation and follow-up closure

CANONICAL-SCOPE-HOTFIX-01 deliberately reopened RNX-P508 (`start only selected Nexus-defined mounts`) because the prior MIGRATE-X01 gate had not executed final selected-mount activation. That was the correct fail-closed state at discovery time.

MIGRATE-X01-HOTFIX-01 subsequently closes RNX-P508 with direct production-path evidence: two provider mounts are discovered, only one is explicitly selected and imported, finalization starts that selected Nexus mount, and the unselected provider mount remains outside the Nexus registry/authority. The canonical compiler remains responsible for the P001..P514 universe; the follow-up hotfix supplies the missing behavioral proof rather than weakening the compiler.
