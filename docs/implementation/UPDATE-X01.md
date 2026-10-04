# UPDATE-X01 — Runtime update manager

UPDATE-X01 owns runtime update detection, acquisition, qualification, staging,
activation timing, rollback, and bounded immutable-runtime retention. It extends
SOURCE-X01/SOURCE-X02 and RUNTIME-X02/X03; it does not introduce another source,
qualifier, activation, or cleanup authority.

## Default policy

The persisted/default policy is deliberately conservative for a rooted personal
device:

- source: `bclone`;
- automatic checks: on;
- automatic acquire/download: on;
- automatic qualification: on;
- automatic staging of a passing candidate: on;
- activation: `next-reboot` (or explicit CLI/WebUI action);
- immediate active-mount restart: off;
- check interval: 360 minutes;
- unprotected runtime history retained: 2 candidates.

The daemon owns periodic checks. A check may resolve, download/build-result
consume, qualify, and stage, but the default path never changes the live
runtime. `module/service.sh` promotes a staged candidate only at the next boot,
after interrupted-activation recovery and before normal boot reconciliation.

## Canonical authorities

- source identity/resolution: `internal/runtimesource`;
- download/archive snapshot + static/runtime qualification: `internal/runtimestore`;
- staged/current/previous identities and activation/rollback: `internal/runtimeactivation` + `internal/runtimestate`;
- update policy/check state: `internal/runtimeupdate`;
- runtime history deletion: `runtimestore.GarbageCollect` only.

CLI and WebUI use the same typed engine operations:

- `runtime.update.status`
- `runtime.update.check`
- `runtime.update.activate`
- `runtime.update.rollback`
- `runtime.update.gc`
- `runtime.update.policy`
- `runtime.update.policy.apply`

The corresponding CLI is `racctl runtime update ...`.

## Release-asset handling

Release ZIPs are not treated as executable bytes. The immutable downloaded
artifact is hashed first, then ZIP structure is validated without following
links. Absolute/traversal paths, symlinks, non-regular members, oversized
members, malformed ZIPs, and ambiguous executable selection fail closed. The
selected executable receives a distinct binary SHA-256 and then enters the
existing runtime qualifier.

The manifest therefore preserves both `archive_sha256` and `binary_sha256`.
Expected source hashes bind the downloaded artifact, not a post-extraction
file.

## Failure semantics

Interrupted/offline acquisition publishes no candidate and records a retryable
update state. Failed or unqualified candidates are never staged and therefore
cannot replace the active runtime. A missing staged candidate is surfaced and
cleared before boot activation. Activation-time real mount failure is handled
by the already sealed RUNTIME-X03/G1 transactional rollback authority.

URLs persisted into provenance/update errors are sanitized; URL userinfo,
queries and fragments are not update-state evidence.

## Runtime retention

GC protects active, previous, staged and in-flight candidate identities. Only
verified immutable store directories outside that protected set can be deleted,
and only through the canonical runtime-store GC function. Symlink/non-directory
entries are never followed as cleanup targets.

## HOTFIX-02 — independent acquire / qualify stages

The update policy stages are now behaviorally independent. `AcquireAutomatically` materializes immutable bytes and provenance into the runtime store with qualification state `pending`. Only when `QualifyAutomatically` is true does UPDATE-X01 invoke `runtimestore.Test`; only a qualified result may reach staging. An acquire-only policy (`acquire=true`, `qualify=false`, `stage=false`) ends successfully as `acquired` rather than qualifying behind the policy's back and then reporting failure.
