# ANDROID-G1 — Android/runtime qualification gate

Status: **qualified**.

Roadmap position: **8/16**. Gate window: **ANDROID-X01 → POLICY-X01 → RUNTIME-X01**.

ANDROID-G1 is a gate/remediation overlay, not a feature overlay. It audits the
Android namespace visibility, resource policy/VFS cache governance, scheduled
job runtime, and local-only RC authority delivered in positions 5-7.

## Gate promises

The gate requires all of the following to remain true together:

- namespace mutation never occurs before a fresh topology/ownership preview is
  qualified under the lifecycle/namespace locks;
- visibility claims remain evidence-backed per discovered namespace class and
  Android user, with no universal ordinary-app claim;
- zygote/storage namespace churn, network loss/recovery and lifecycle
  reconciliation converge without unsafe unmounts or duplicate rclone
  processes;
- policy blocks do not consume restart budget or destroy a valid running VFS
  mount, and repeated allow/block/allow transitions remain process-idempotent;
- VFS profiles remain explicit opt-in while custom mounts remain unchanged;
- cache pruning/clear/forget remains beneath the Nexus-owned cache root,
  symlink-safe, bounded and stopped-mount-only;
- scheduled jobs persist next-run state before launch, survive daemon restart
  without replaying the same claimed occurrence, and serialize configuration
  revision updates across processes;
- destructive `sync` definitions are previewable before confirmation but cannot
  be persisted until `confirm_destructive=true` is explicit;
- job endpoints remain typed and cannot smuggle arbitrary rclone argv or
  on-the-fly backend credentials;
- rclone RC remains authenticated and loopback-only, private `args_file`
  entries cannot override Nexus RC flags, and credentials never cross the typed
  API;
- Android/runtime persistent state survives module replacement and uninstall
  simulation;
- reproducible Android arm64 backend and flashable package contracts remain
  green after the complete window.

## Audit-loop remediation

The gate found and closed two RUNTIME-X01 gaps before qualification:

1. **Destructive-sync preview vs approval.** The job validator accepted a
   `requireApproval` parameter but ignored it, which made an unconfirmed `sync`
   fail during preview. ANDROID-G1 makes preview non-mutating and informative:
   it surfaces the destructive job, while registry apply still rejects it until
   the candidate explicitly sets `confirm_destructive=true`.
2. **Concurrent job-registry stale-write race.** Two clients could preview the
   same job-registry revision and race through apply, allowing last-writer-wins
   despite the revision contract. ANDROID-G1 serializes registry apply with a
   cross-process `flock` and re-reads/revalidates revision plus digest while the
   lock is held. Exactly one contender can advance a given revision.

The gate also adds regressions proving repeated policy reconciliation does not
replace a valid process, daemon scheduler restart does not replay a durably
claimed run, and typed RC metric responses never contain backend credentials.

## Authoritative validation

`./devtoolw android-g1` is the canonical gate workflow. It combines:

- shell syntax and module contract checks;
- the complete Go suite and Python integration suite;
- the ANDROID-G1 static/state audit;
- read-only Android namespace observation through the existing device-smoke
  surface (controlled propagation remains explicitly opt-in);
- namespace ownership/rollback/churn tests;
- policy transition and process-idempotence tests;
- VFS profile/cache ownership and bounded-pruning tests;
- scheduler persistence/restart/deduplication and destructive-preview tests;
- RC loopback/auth/isolation/credential-redaction tests;
- reproducible Android arm64 backend build;
- deterministic flashable package contract.

The gate closes only with zero unresolved findings.

## Next boundary

**PLATFORM-X01 — diagnostics + root portability + upgrade lifecycle (9/16).**
This begins the diagnostics/root-framework portability scope after the separate
Android/runtime gate.
