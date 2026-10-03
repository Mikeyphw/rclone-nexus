# RUNTIME-STANDALONE X03 — Transactional activation + rollback

## Position

- Campaign: `RUNTIME-STANDALONE`
- Canonical promise range: `RNX-P349..RNX-P365` (17 promises)
- Gate-window position: implementation overlay 3 of 3 before `RUNTIME-G1`
- Full-plan position: 3 of 11
- Next overlay: `RUNTIME-G1 — Runtime authority gate`

## Canonical activation authority

X03 makes `/data/adb/rclone-nexus/runtime/activation-v1.json` the durable selector for managed runtime execution. The selected executable is always resolved from the immutable X02 store using the state file's runtime ID and SHA-256. `/data/adb/rclone-nexus/runtime/active/bin/rclone` is retained only as a compatibility projection and cannot redirect `runtimeauth` after activation state exists.

The transaction authority is `internal/runtimeactivation`. CLI and WebUI do not implement switching separately: both invoke the same typed control operations registered in `internal/control`:

```text
runtime.candidates
runtime.activation.status
runtime.activate
runtime.rollback
runtime.recover
```

The CLI exposes those operations as `racctl runtime activation-status`, `activate`, `rollback`, and `recover`. The Runtime Manager uses the same operation IDs through the normal WebUI control client.

## Durable state machine

Activation persists every transition atomically and writes a transaction receipt under `/data/adb/rclone-nexus/runtime/transactions/<transaction-id>.json`:

```text
STAGED
  -> QUIESCING
  -> ACTIVE_PENDING_VERIFY
  -> ACTIVE
```

Failure after staging enters:

```text
ROLLBACK
  -> RECOVERED
  -> or DEGRADED_RECOVERED when recovery itself is incomplete
```

The state records active, previous, candidate and staged runtime IDs plus the corresponding byte digests. Transition receipts record physical evidence paths, outcome, error and transition history. Runtime state and receipts are written with temporary-file + fsync + rename durability.

## Activation transaction

A candidate is not trusted because a manifest says `qualified=true`. The controller resolves the X02 manifest, requires a qualified state, reruns the X02 production qualifier immediately before switching, then inspects the immutable candidate again. After Nexus-owned mounts are quiesced, it re-verifies the candidate bytes once more before publishing the active identity.

Activation then:

1. serializes on the runtime activation lock;
2. recovers any interrupted earlier transaction;
3. converts a pre-X03 managed executable into a qualified immutable store entry when needed for rollback;
4. persists `STAGED` with the candidate identity/digest;
5. snapshots desired mounts;
6. persists `QUIESCING` and stops only mounts whose Nexus ownership is proven, without changing desired state;
7. verifies the staged candidate again;
8. atomically updates only the compatibility projection and persists `ACTIVE_PENDING_VERIFY` with the candidate as the canonical active identity;
9. reconciles desired mounts through the transition-only runtime path;
10. verifies every desired mount is running and Nexus-owned;
11. persists `ACTIVE`, retaining the old runtime as `previous`.

No candidate binary is overwritten in place. The active projection is changed by symlink rename, while execution uses the immutable store path. An already-open executable inode therefore cannot be changed underneath a process.

## Rollback and recovery

Startup/RC-contract/reconcile failure invokes rollback automatically. Rollback resolves the previous runtime ID and digest from durable state, verifies the immutable bytes, restores the projection, reconciles desired mounts and records `RECOVERED`. If the previous runtime is unavailable or one of the restored mounts fails, the outcome is `DEGRADED_RECOVERED` and the failure remains explicit.

Boot runs `racctl runtime recover` before `runtime status --require-operational`. Recovery behavior is phase-specific:

- `STAGED`: no switch is assumed; return to stable state and retain the candidate for retry;
- `QUIESCING`: restore/reconcile the previous runtime;
- `ACTIVE_PENDING_VERIFY`: never assume the candidate became stable; restore the previous runtime;
- `ROLLBACK`: finish restoration of the previous runtime.

Normal mount start/stop/restart/reconcile and configuration publication fail closed during a runtime transition. Only the activation controller can call the transition-specific reconcile/executable path.

## Evidence and qualification boundary

X03 has executable tests for successful state/receipt persistence and every required failure/recovery edge, including quiesce refusal, candidate disappearance, startup/RC failure, missing previous runtime, partial rollback restart, and both crash windows. `runtimeauth` tests prove the durable state wins over a poisoned active projection and that normal callers cannot execute through an in-flight transition.

The X03 audit also builds and executes the real `racctl` ingress. On a non-Android validation host it proves an unqualified imported candidate cannot be activated and cannot manufacture activation state. Successful real-device candidate activation/mount/rollback remains the responsibility of the next `RUNTIME-G1` gate; X03 does not convert the six X02 `BLOCKED_BY_ENVIRONMENT` promises into qualified device evidence.
