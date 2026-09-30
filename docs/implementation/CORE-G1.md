# CORE-G1 — control-plane/lifecycle gate

Status: **qualified**.

Roadmap position: **4/16**. Gate window: **CORE-X01 → LIFE-X01 → LIFE-X02**.

CORE-G1 is a gate/remediation overlay, not a feature overlay. It audits the
native control plane, configuration migration, lifecycle ownership, readiness,
self-healing and persistent operation truth delivered in positions 1-3.

## Gate promises

The gate requires all of the following to remain true together:

- v0.1 `mounts.d/*.conf` configuration migrates semantically losslessly into
  the revisioned v2 registry, including private `args_file` preservation;
- repeated start/stop/reconcile cycles converge without stale ownership or
  duplicate managed processes;
- PID reuse, missing process records and stale runtime state cannot authorize
  signals/unmounts outside a Nexus-owned mount boundary;
- explicit desired state wins over a reconcile decision made before the
  per-mount lock is acquired;
- persistent config/desired/health/operation state survives module replacement
  and uninstall simulation;
- typed-operation allow-listing, response/event bounds, private-path handling,
  redaction and safe cancellation remain intact;
- the deterministic arm64 backend and flashable module continue to satisfy the
  module/package contracts without bundling rclone or FUSE.

## Audit-loop remediation

The gate audit found and closed two ownership/race gaps before qualification:

1. **Unmount authority on stale/missing identity.** `stopUnlocked` previously
   called the unmount helper even when the process record was absent or could
   represent PID reuse. CORE-G1 now authorizes unmount only when runtime
   observation proves a matching Nexus config/process record and a rclone/FUSE
   mount. A live PID with a different `/proc` start time is explicitly not
   ownership evidence; a dead matching Nexus record may still authorize cleanup
   of the stale mount it created.
2. **Stale desired-state decisions during reconciliation.** Reconcile paths
   previously decided start/stop before acquiring the per-mount lock. CORE-G1
   now re-reads desired and observed state under the lock, and supervisor helper
   operations refuse to cross a newer explicit start/stop decision. Health is
   refreshed after lock-protected mutations instead of publishing stale state.

## Authoritative gate validation

`./devtoolw core-g1` is the canonical repository gate workflow. It combines:

- shell syntax and module contract checks;
- the complete Go suite and Python compatibility/integration suite;
- repeated CORE-G1 lifecycle/ownership cycles in the canonical Go suite;
- legacy migration, stale-revision, atomic-write and rollback proofs;
- control/protocol/journal/daemon security and redaction tests;
- static prohibition of `eval`, arbitrary `sh -c`, `killall` and `pkill` in
  product code;
- module-replacement/uninstall simulation with persistent-state hash canaries;
- reproducible Android arm64 backend build;
- deterministic flashable package contract.

The gate is closed only when all of those checks pass with zero unresolved
findings.

## Next boundary

**ANDROID-X01 — namespace discovery, propagation and app-visibility
qualification (5/16).** This begins the Android visibility/policy/VFS/runtime
scope after the separate CORE-G1 lifecycle gate.
