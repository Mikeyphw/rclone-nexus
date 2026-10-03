# RUNTIME-STANDALONE G1 — runtime authority qualification gate

RUNTIME-G1 is the adversarial gate over X01–X03. The canonical scope remains RNX-P001..RNX-P500; Position 4 is RNX-P366..RNX-P373. This gate also closes the six X02 obligations that could not be proven without real rooted Android/FUSE execution: RNX-P330, P335, P336, P337, P338 and P341.

## Qualification rule

The overlay is authored so its Git commit is permitted only after the **real rooted-Android/FUSE capture and full gate pass on the target checkout**. A host/source-only audit is supplementary evidence and must never create the G1 commit by itself. The canonical ledger therefore records the state that is valid after successful artifact validation; a failed or unavailable real-device gate leaves the overlay uncommitted and the prior X03 repository state authoritative.

After a successful G1 application, RNX-P366..RNX-P373 and the six inherited X02 device obligations are `IMPLEMENTED_AND_PRODUCTION_ADOPTED`. GRAND-G1 remains open because later SOURCE/CONFIG/LIFECYCLE/WEB/RELEASE positions are still nonterminal.

## Canonical authority direction

Managed production flow is:

`activation-v1.json → immutable runtimes/<runtime-id>/rclone + manifest → runtimeauth → provider facade/control/CLI/daemon/WebUI/boot/mount lifecycle`

`runtime/active/bin/rclone` is projection-only after activation state exists. PATH, `RCLONE_CONFIG`, and NewFuture provider state are compatibility inputs only in explicit external mode. The provider facade delegates executable/config selection to `runtimeauth`; it does not select independently.

Release/install qualification follows the same authority direction. A providerless managed Nexus is valid when the canonical runtime authority is operational. Exact provider identity is optional compatibility evidence for migration/external deployments, never a prerequisite for managed qualification.

## Source/adversarial audit

`scripts/dev/runtime_standalone_g1_gate.py --source-only`:

- re-reads the RNX-P001..RNX-P500 canonical scope and the Position 4 ledger bindings;
- reruns X01, X02 and X03 gates against current source;
- verifies the release/install surfaces no longer make the legacy provider a managed-mode authority;
- checks the Devtool wrapper/workflow/final-seal graph requires the G1 source audit and full physical-evidence audit;
- verifies private G1 evidence is excluded from reproducible source identity;
- exercises evidence-validator negative cases and providerless release-qualification regressions.

This source pass alone is not G1 qualification.

## Real-device gate

`scripts/dev/runtime_standalone_g1_device.py capture` requires rooted Android. It builds `racctl` from the current source and creates an isolated root-owned qualification workspace under `/data/adb/rclone-nexus/qualification/`.

Using real candidate bytes and production entry points it proves:

1. two immutable runtime candidates with **different executable SHA-256 values** import and pass the actual Android/FUSE qualifier;
2. candidate A activates with the legacy provider absent;
3. a real Nexus-owned FUSE mount starts and `/proc/<pid>/exe` hashes to the selected immutable runtime;
4. poisoned PATH/provider state and poisoned `runtime/active/bin/rclone` cannot redirect managed execution;
5. candidate B activation restarts the mount under B and `/proc/<pid>/exe` proves the process switched to B's distinct bytes;
6. rollback returns the mount to A;
7. a forged `qualified=true` manifest is requalified and rejected;
8. process loss followed by the production `service.sh` boot path restores the desired mount under the rolled-back runtime; the local-backend fixture is explicitly `network_mode=offline-allowed`, so this proof exercises runtime recovery rather than incidental device connectivity policy;
9. CLI, daemon RPC and WebUI bridge all report the same active runtime;
10. immutable binaries, manifests, activation state, transaction receipts, mount config and the gate-built `racctl` physically exist and are SHA-256 bound;
11. the runtime manifest contains passing Android execution, RC, FUSE smoke-mount, signal, process-ownership and post-qualification-hash checks;
12. a separately built static Linux/arm64 rclone-shaped probe really executes on Android but is rejected because it cannot produce the real FUSE mount, explicitly closing RNX-P341 instead of laundering it through a positive candidate.

The qualifier does not require a NewFuture `fusermount3` helper before attempting the rooted smoke mount. If cleanup needs an unmount helper it may use an explicitly available helper, otherwise rooted `/system/bin/umount`; the optional external provider is not a managed qualification prerequisite.

The harness stops only its isolated mount and daemon when finished. The qualification workspace is retained so the gate can resolve the referenced evidence.


### v16 boot-recovery correction

The v14/v15 failure was not sufficient to establish boot-reconcile timing as the root cause. Their G1 fixture combined `require_network=false` with `network_mode=any`; the typed `network_mode` is authoritative and normalizes that mount back to network-required. Activation and rollback use the runtime-transition reconcile path and therefore can succeed before the production boot supervisor first enforces readiness/resource policy. v16 removes that confounder and the service-loop workaround, uses `offline-allowed` for the local backend fixture, requires policy/health evidence, and emits health/policy/desired-state diagnostics if boot recovery still fails.

### v17 validation-lifecycle correction

v16's first device attempt did not reach rooted/FUSE capture because the broad Go regression suite hit a scheduler-test cleanup race: `RunJobScheduler` intentionally lets an already-dispatched job finish after scheduler cancellation, while `TestSchedulerDoesNotDuplicateDueJob` waited only a fixed 80 ms before its `TempDir` was eligible for removal. On slower Android/Termux execution the detached run could still touch `state/run/jobs`, causing `TempDir RemoveAll` to fail with `directory not empty`. v17 keeps production scheduler semantics unchanged and makes the tests synchronize on scheduler exit plus durable job/journal completion instead of wall-clock luck.

## Evidence binding and negative proof

`release/evidence/runtime-g1-device-qualification.json` is private and gitignored. It binds the exact production source surface by SHA-256 plus a canonical source digest, records the pre-commit parent HEAD used by Devtool's validate-before-commit transaction, and binds the built `racctl` bytes, Android identity, immutable runtime bytes/manifests, transaction state/receipts and real process evidence. The full gate re-hashes every referenced root-owned file and fails if any bound source file changes.

The gate also mutates a copy of the evidence to prove that a nonexistent evidence path fails even after recomputing the document digest, and that an asserted operation with no real process fails. Thus replacing evidence strings with nonexistent references or skipping the underlying operation cannot still pass the gate.

The authoritative target workflow is:

```sh
./devtoolw runtime-standalone-g1
```

The Devtool overlay validator executes the source audit, broad Go/Python/module regressions, real-device capture and full G1 gate before Git commit. Because Devtool validates before committing, the capture accepts only the exact known G1 overlay dirty paths and rejects any unrelated repository change; the bound source hashes/digest must still match when the full gate consumes the evidence. If the real device qualification cannot be proven, validation fails and the G1 commit must not be created.
### v18 immutable physical-evidence correction

v17 proved the complete boot-recovery flow, including `RUNNING` supervisor health and converged daemon/WebUI runtime identity, but then invalidated its own physical evidence: it hashed the live `desired/runtime-g1.json` while the desired state was `running`, and cleanup correctly used `mountctl stop`, rewriting the same file to `stopped` before validation re-hashed it. Live health and log files were vulnerable to the same mutation. v18 snapshots mutable production state/log bytes under the isolated qualification workspace before cleanup, records each snapshot's production `source_path`, and requires the snapshots themselves to prove desired `running`, health `RUNNING`, the recovered active runtime, and the `offline-allowed` fixture policy. Immutable runtime binaries/manifests and completed transaction receipts remain referenced directly.
### v19 private-evidence lifecycle correction

The successful v18 device qualification exposed a transaction-hygiene gap: Devtool committed `release/evidence/runtime-g1-device-qualification.json` even though the gate contract marks it private, gitignored and excluded from release source identity. v19 makes that contract executable. The source audit rejects a tracked private G1 evidence path; the gate validator removes the generated evidence on every exit after it has been consumed; and the canonical full G1/release workflows capture fresh evidence immediately before the G1 audit and run validation-output cleanup immediately afterward. No later workflow is allowed to depend on a tracked or stale copy of rooted-device evidence.
### v20 exact capture-ownership correction

v19's pre-capture cleanliness guard mixed the evidence binding set with a manually maintained list of overlay-owned paths. That had two defects: it omitted the newly modified `scripts/dev/cleanup_validation_outputs.py`, causing a false failure before device capture, and it permitted dirty edits to evidence-bound files that the overlay itself did not modify. v20 defines the exact G1 overlay-owned filesystem surface separately from evidence bindings. Device capture accepts only those overlay-owned paths and rejects unrelated edits, including edits to bound-but-unmodified production sources.

