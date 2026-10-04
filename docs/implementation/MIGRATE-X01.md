# MIGRATE-X01 — NewFuture migration + standalone packaging

## Position and canonical scope

MIGRATE-X01 is full-plan position **9 of 11**. The merged position-9 universe is
35 promises, not only the 14 runtime-standalone additions:

- `RNX-P106..RNX-P126` — diagnostics, root-manager portability, upgrade/uninstall
  safety and standalone package obligations inherited from the original roadmap.
- `RNX-P435..RNX-P448` — NewFuture provider detection, migration and adversarial
  authority-switch obligations.

All 35 are qualified by `migrate-x01-audit`; static file/function presence is not
sufficient evidence.

## Migration authority

`internal/migration` owns a durable, serialized migration state machine. It never
sources or evaluates provider configuration and never writes under the legacy
provider module. Detection is read-only and records provider presence/enabled
state, provider runtime path/version, exact `rclone.conf` SHA-256, provider-owned
processes, observed mounts and typed sync/copy job candidates.

The migration flow is deliberately two-phase:

1. `migration preview` inventories provider state and requires explicit mount/job
   selections. Nothing is imported implicitly.
2. `migration apply` consumes a preview proof, revalidates the provider snapshot,
   quiesces only provider-owned processes, validates the provider config with the
   active Nexus runtime, copies it byte-for-byte, and imports reviewed mounts/jobs
   **disabled**.
3. State stops at `AWAITING_PROVIDER_DISABLE`. Nexus does not uninstall, disable,
   replace or mutate `/data/adb/modules/rclone`.
4. The user/root manager explicitly disables or removes the provider module.
5. `migration finalize-preview` proves there is no competing provider lifecycle;
   `migration finalize` consumes that second proof and enables only the reviewed
   Nexus mounts/jobs.

Both mutation phases are serialized by `migration.lock`. The durable state and
pre-migration backups live under `$RNEXUS_STATE_DIR/migration/`.

## Recovery and fail-closed behavior

Daemon startup calls migration recovery before opening the control socket.
Interrupted PREPARED/QUIESCING/APPLYING/FINALIZING transactions restore the exact
pre-migration Nexus config/mount/job state. `AWAITING_PROVIDER_DISABLE` prevents
normal daemon startup until the operator completes or rolls back the handoff.
After COMPLETED, any reactivated provider lifecycle creates a durable `CONFLICT`
and Nexus fails closed rather than permitting two authorities.

The implementation explicitly covers provider disappearance, encrypted config
rejection, provider processes refusing stop, duplicate mount destinations,
existing-job conflicts, interruption/reboot, provider reactivation, and failure
of the final authority switch after config copy.

## Standalone package

The normal `install` workflow remains Nexus-only. `install-stack` is an explicit
legacy/bootstrap workflow, not a runtime dependency of the standalone package.
The package contract continues to require `webroot/`, integrity/mode manifests,
root-manager portability and persistent-state lifecycle rules, and forbids a
provider module payload or mutations of NewFuture-owned files.

## Executable evidence

`migrate-x01-audit`:

- runs migration/control/CLI Go tests and adversarial migration regressions;
- compiles `racctl` and exercises read-only inspect, explicit preview, proof-bound
  apply, the provider-disable boundary, finalization and durable evidence against
  an isolated NewFuture-compatible provider fixture;
- hashes the provider fixture before/after Nexus apply to prove non-mutation;
- runs diagnostics/doctor/root-manager/platform/integrity tests for P106..P126;
- runs install-stack tests, the module contract and the real package contract.

Next position: **UX-X01 — WebUI/CLI runtime-source-update-migration UX**.
