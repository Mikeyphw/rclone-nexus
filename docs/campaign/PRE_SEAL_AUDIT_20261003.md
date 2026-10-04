# Rclone Nexus pre-seal architectural audit — 2026-10-03

> **2026-10-04 canonical-scope correction:** this audit originally compiled only direct roadmap bullets and therefore reported RNX-P001..RNX-P500. The canonical compiler hotfix subsequently classified every non-bullet RUNTIME-STANDALONE roadmap clause and appended 14 genuinely missing promises. The current authority is RNX-P001..RNX-P514; the P500 counts below are retained only as historical audit-time facts.

> **2026-10-04 selected-mount follow-up:** RNX-P508 was deliberately reopened by the canonical-scope correction and is now behaviorally closed by MIGRATE-X01-HOTFIX-01. The proof discovers two provider mounts, selects/imports one, starts only that reviewed Nexus mount at finalization, and proves the other remains outside Nexus authority.

Base production snapshot: `f1868be` (`GRAND-G1: close provider CLI qualification on Android (v3)`).

## Verdict

The existing GRAND-G1 definition is **not a valid current final seal**. Current source contains obligations added after the original roadmap and after the historical 24-row GRAND-G1 summary. A current final gate must therefore fail closed until the merged campaign is fully production-adopted and requalified.

## Canonical merged promise universe

At the time of this 2026-10-03 audit, the then-current machine-readable authority spanned **RNX-P001..RNX-P500**:

- RNX-P001..P229 — original detailed roadmap requirements (229);
- RNX-P230..P236 — install-stack amendments (7);
- RNX-P237..P257 — guided mount remediation (21);
- RNX-P258..P278 — device runtime/WebUI remediation (21);
- RNX-P279..P500 — RUNTIME-STANDALONE roadmap requirements (222).

The former 24-row final-seal table remains a historical summary only. It is not the promise count for current HEAD.

## Confirmed seal defect

Before this hotfix, the release/GRAND-G1 workflow did not execute the guided-mount remediation checker or the device-runtime/WebUI remediation checker, and there was no canonical merged promise ledger binding those amendments into the final seal. Consequently those contracts could regress while the old seal machinery still reasoned only about the original scope.

The hotfix adds explicit executable workflow nodes for both remediation families, a canonical-scope audit, binds the legacy final-seal policy to the merged ledger, and changes `grand_g1_gate.py` so an active merged campaign with open mandatory promises invalidates the legacy seal rather than silently reusing it.

## Historical obligations and disposition

The install-stack amendment family is explicitly carried as RNX-P230..P236. RNX-P230..P235 are recorded as production-adopted from the already-applied install remediation; RNX-P236 is `EXPORTED` to Devtool transaction authority rather than falsely claimed as repository-owned behavior.

The full 21 guided-mount promises and 21 device-runtime/WebUI promises are explicitly carried as RNX-P237..P278 and map to their executable remediation audits. Older roadmap requirements remain present as RNX-P001..P229 with `PARTIALLY_ADOPTED` status until fresh production-path qualification under the merged campaign is performed. No historical family is silently dropped.

## Authority/adoption audit findings

At the audit baseline, the old GRAND-G1 paperwork did not prove the current merged scope. Production/runtime truth therefore keeps the campaign open. The RUNTIME-STANDALONE design seed also supersedes the old architectural assumption that `/data/adb/modules/rclone` is the normal runtime authority; that migration must be proven through new production entry points rather than by relabeling provider discovery.

No current final qualification is claimed by this hotfix. Its purpose is to make scope/evidence truth fail closed before standalone implementation begins.

## Mandatory pre-seal questions — current answers

1. Audit-time exact range: RNX-P001..RNX-P500; superseded by the 2026-10-04 RNX-P001..RNX-P514 compiler correction.
2. Post-roadmap amendments: install-stack (7), guided mount (21), device runtime/WebUI (21), standalone runtime (222).
3. Historical obligations remain: yes; original 229 and incident/remediation families are retained.
4. Explicit disposition: yes in the canonical ledger; older original items remain partial, not silently green.
5. Competing authorities: standalone runtime audit required; legacy provider/PATH authority is not accepted as canonical managed authority.
6. New models with few call sites: must be re-audited per standalone overlay; model existence alone is not qualification.
7. Synthetic success in gates: legacy seal cannot substitute summary rows for the 500-item merged scope.
8. Provider labels vs providers: real-device provider qualification remains separately required at the eventual final gate.
9. Named but unpersisted attachments/receipts: eventual final gate must resolve physical evidence; not closed by this hotfix.
10. Unresolvable evidence: production-adopted canonical entries fail the scope audit when evidence cannot resolve.
11. Stale evidence hashes: must be rebound by the eventual final seal after all remediation.
12. CLI bypasses: standalone campaign must trace `racctl`/wrapper production ingress before qualification.
13. `devtoolw` bypasses: release workflows are explicitly part of the merged evidence boundary.
14. Direct APIs bypasses: to be checked per standalone position/gate.
15. Remote/federated bypasses: not claimed by current repository seal without real provider evidence.
16. Destructive bypasses: cleanup authority remains a mandatory later audit item.
17. Repository-controlled trust/secrets: no new trust claim is introduced by this hotfix.
18. Execution vs testcase failure truth: remains a mandatory final-gate invariant.
19. Mutable legacy stores: original roadmap stores remain partial until requalified/retired.
20. Fresh-process reconstruction: not yet a campaign-wide qualified invariant.
21. End-to-end chain: not yet final-qualified.
22. Negative/adversarial coverage: required per new overlay and gate.
23. Later campaign changed old evidence: yes; this is why legacy GRAND-G1 is invalidated.
24. Nonexistent evidence strings: canonical-scope audit rejects unresolved evidence for production-adopted promises.
25. Operation never ran: current final seal must not pass merely from summary metadata; the campaign remains open.

## Hotfix validation expectation

`python3 scripts/dev/grand_g1_gate.py --source-only` **must fail** while mandatory canonical promises remain open. A passing legacy GRAND-G1 at this point is itself a regression.
