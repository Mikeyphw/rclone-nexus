# Governing Devtool Campaign Protocol

This protocol is authoritative for the next chat.

## Core principle

The job is **not** to make the campaign ledger green. The job is to make every claimed architectural invariant true in current production code paths, then prove that truth with executable evidence.

Treat any distinction between “model/type/helper exists” and “production actually uses it” as critical.

## Mandatory rules

### 1. Canonical scope comes first

Before implementing or sealing anything:

1. Identify the current canonical campaign scope.
2. Enumerate every applicable promise:
   - merged campaign promises;
   - amendments;
   - remediation-added promises;
   - inherited source promises;
   - historical audit findings;
   - regression contracts;
   - exported obligations from earlier gates;
   - cross-scope obligations.
3. Determine the **exact maximum current promise number**.
4. Compare the full universe with what implementation/gates enumerate.
5. A gate that proves fewer promises than current merged scope contains is invalid.

### 2. Never equate existence with adoption

For every architecture/canonical abstraction distinguish:

- type/model exists;
- unit tests pass;
- helper API exists;
- gate can construct it;
- one consumer uses it;
- all intended production ingress paths use it;
- prior competing authority is retired or reduced to projection/compatibility lowering.

For each claimed authority answer:

- who creates it;
- who consumes it;
- normal CLI forced through it?;
- `devtoolw` forced through it?;
- direct/internal supported APIs forced through it?;
- workflow/validation/roadmap/remote/federation/Android/test/artifact/UI paths forced through it where applicable?;
- another independently mutable/executable authority exists?;
- callers can bypass it?

If competing authority remains, classify partial unless explicitly compatibility-only/projection-only.

### 3. Authority inversion must be real

If promise is `canonical model -> runtime/generated surface`, do not implement `legacy/runtime surface -> scrape/introspect -> canonical-looking model` and call that convergence.

### 4. Gates must exercise production entry points

Final gates must prefer real user ingress: `devtool`, `devtoolw`, workflow execution, validation, roadmap/gate execution, remote/SSH, federation, Android, artifact application and UI/control paths. Synthetic object construction is supplementary only.

### 5. Real providers must be real

Provider qualification requires actual provider execution/placement/capability negotiation/event transport/reconnect/control/receipt/cleanup. Labels are not qualification.

### 6. Plans/snapshots/receipts/projections must correspond to reality

Verify operation happened, files exist, identities/hashes match bytes, environment/provider actually executed, receipt is post-execution, attachments exist.

### 7. Evidence must resolve

Evidence references must resolve to executable nodes, durable receipts, source invariants checked by executable audit, real runs/attachments, or specifically identified authoritative reports. Fail on missing/stale/symbolic/malformed/unrelated references.

### 8. Evidence binding must survive changes

Changes to bound files invalidate prior seals. Current HEAD must match every stored evidence hash before closure.

### 9. Historical obligations must not disappear

Explicitly disposition every old audit/regression/incident/roadmap/export as implemented, superseded, exported, retired, blocked, partial or unimplemented.

### 10. Cross-scope promises require real cross-scope integration

Do not accept lookalike dataclasses or projections in place of the actual authority from the other scope.

### 11. Search duplicate authorities explicitly

For this Rclone Nexus campaign this especially means searching for multiple:

- runtime selectors;
- provider locators;
- config resolvers;
- mount lifecycle engines;
- retry engines;
- schedulers/jobs authorities;
- WebUI vs shell vs daemon control paths;
- update/activation state stores;
- destructive cleanup paths;
- boot-time autorun paths.

### 12. Negative/adversarial tests are mandatory

For this campaign include at least:

- malformed/truncated binary;
- wrong ABI;
- Linux-arm64 but incompatible Android binary;
- stale active-runtime pointer;
- missing runtime bytes;
- poisoned PATH;
- poisoned `RCLONE_CONFIG`/provider env;
- unsupported CLI flag;
- CLI global-vs-command help mismatch;
- failed startup before PID stabilizes;
- transient network error;
- nonretryable config/provider error;
- corrupted runtime manifest;
- fake/incorrect hash/provenance;
- malicious archive traversal/symlink;
- update candidate disappears mid-activation;
- reboot during staged activation;
- rollback failure;
- both NewFuture and Nexus attempting automount;
- parent sync + Nexus job collision;
- invalid/missing evidence references;
- secrets in stderr/logs/details;
- zero-operation/synthetic-success gates.

### 13. Counts preserve truth

Do not fake testcase counts to convey process/gate failure. Keep execution outcome, testcase outcome, applicability, gate verdict, severity and policy status distinct.

### 14. Trust must be verified

Hashes/provenance/signatures must be verified against actual bytes. Repository-controlled state cannot self-authorize trust. Secrets must not leak to argv/logs/events/public state.

### 15. Cleanup has one destructive authority

All deletion/prune/move/GC paths must revalidate canonical ownership and references before mutation. Fail closed if protection authorities are unreadable.

### 16. Durable runtime/update state must be empirically proven

If a canonical runtime store/activation history is introduced, run real operations, prove durable unique identities, physical artifacts, restart/rebuild behavior and absence of parallel authoritative stores.

### 17. Reuse must skip real work

If runtime downloads/builds/qualification reuse is claimed, instrument spawn/download/build boundaries and prove reused candidates skip real work while input changes invalidate correctly.

### 18. Final gates are authored last

The final seal must enumerate every current promise and inherited obligation, verify evidence resolution/hashes, exercise real production paths, negative cases, competing-authority retirement and repository consistency.

### 19. Honest qualification states

Use states such as:

- `IMPLEMENTED_AND_PRODUCTION_ADOPTED`
- `IMPLEMENTED_MODEL_ONLY`
- `PARTIALLY_ADOPTED`
- `SIMULATED_ONLY`
- `DEFERRED`
- `EXPORTED`
- `SUPERSEDED`
- `BLOCKED_BY_ENVIRONMENT`
- `UNIMPLEMENTED`

### 20. Mandatory pre-seal questions

The final gate must answer, with evidence, all 25 questions from the user’s governing protocol: exact promise range, amendments, historical obligations, duplicate authorities, weak call sites, synthetic proofs, fake provider proofs, nonexistent attachments, unresolved/stale evidence, bypass paths, cleanup/trust/counts/durable state, real E2E chain, adversarial cases, changed evidence, and the two crucial tests:

- Would the gate fail if evidence strings were replaced with nonexistent references?
- Would it still pass if the underlying operation never actually ran?

If the second answer is yes or first is no, gate is invalid.

### 21. Overlay reporting

Every overlay report must state:

- promises delivered;
- model vs production adoption;
- production ingress changed;
- duplicate authorities removed;
- remaining gaps;
- targeted validation;
- next overlay;
- overlays remaining before next scope;
- next major scope;
- gate-window position;
- full-plan position.

### 22. Gate audit loop

At every G1/G2/G3-style gate:

1. audit overlays since previous gate;
2. reread merged scope;
3. inspect current source;
4. trace each promise into production;
5. reproduce negative cases;
6. search duplicate authorities/bypasses;
7. validate evidence/hashes;
8. fold gaps into remediation window;
9. repeat until no unowned gap;
10. only then qualify.

### 23. Final rule

Ask: “If I ignored campaign paperwork and inspected current production from scratch, is this architecture actually controlling execution?”

Source/runtime truth wins.
