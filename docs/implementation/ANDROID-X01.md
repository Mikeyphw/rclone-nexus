# ANDROID-X01 — namespace discovery, propagation and app-visibility qualification

Campaign position: **5/16**. This overlay merges the former **NS-X01 + NS-X02 + NS-X03** boundary exactly as defined by the compressed roadmap.

## Delivered promises

- Parses Linux/Android `/proc/<pid>/mountinfo` strictly, including escaped fields, optional propagation metadata and storage-layout capabilities.
- Enumerates distinct mount namespaces by namespace identity rather than PID alone and classifies observed service/root/shell/zygote/Termux/ordinary-app membership.
- Derives Android user IDs from Linux UIDs and reports primary/secondary-user visibility independently.
- Exposes evidence-backed `namespace.inspect`, `namespace.preview`, `namespace.apply`, `namespace.rollback.preview`, `namespace.rollback`, and `namespace.reconcile` typed operations.
- Adds `racctl namespace inspect|preview|apply|rollback|reconcile` and the same surface beneath `rclone-nexus namespace ...`.
- Never claims universal app visibility. Results distinguish service-only, partial, no-app-evidence, and `observed_all_discovered_app_namespaces` states.
- Uses a capability-selected `same-path-bind-v1` adapter only for Android storage boundaries and only when the source mount is provably Nexus-owned.
- Revalidates target mount-namespace identity immediately before `setns`; PID reuse cannot redirect a mutation into a different namespace. `setns` runs on a disposable locked OS thread so a failed namespace restore cannot contaminate the daemon scheduler.
- Records the exact target mount ID plus device/root/fs signature for every Nexus-created bind. Rollback refuses to unmount when that ownership signature changes.
- Requires root plus effective `CAP_SYS_ADMIN`, rejects symlink/non-directory target mountpoints and Android storage-root binds, and fails closed when target topology is truncated, app-relevant namespaces are unavailable, or no qualified strategy exists.
- Applies multiple namespace binds transactionally; bind failure, cancellation, post-bind persistence failure, or verification failure releases only binds created in that transaction and restores the exact previous persistent intent/state.
- Persists app-visible intent and per-namespace ownership under `/data/adb/rclone-nexus/namespace` so zygote/app namespace churn can be reconciled after reboot or storage remount.
- Supervisor reconciliation reapplies visibility to newly observed namespaces and prunes markers for namespaces that no longer exist.
- Lifecycle stop/stale-repair paths suspend Nexus-owned app binds before tearing down the source mount, while preserving app-visible intent for a later restart.
- Configuration apply/rollback suspends visibility for affected mounts before lifecycle restarts and reconciles it afterward; deleted mounts have namespace intent removed.
- Adds a Devtool `device-smoke` workflow. Its default path is read-only and truthful; controlled propagation is explicit through `RNEXUS_DEVICE_SMOKE_MOUNT` plus `RNEXUS_DEVICE_SMOKE_APPLY=1` in a privileged Devtool execution destination. The script never launches `adb` or `su` itself.

## Evidence classes

The namespace backend reports observations, not assumptions. Per mount it records:

- service namespace visibility;
- distinct accessible/unreadable namespace identities;
- root/shell/zygote/app/Termux classes observed in each namespace;
- Android user IDs represented by app namespaces;
- accessible vs visible app namespace counts per user;
- the exact visibility claim justified by those observations.

`observed_all_discovered_app_namespaces` means every app namespace Nexus actually discovered was readable and contained the mount at inspection time. It is deliberately **not** phrased as universal compatibility with every Android build/app/storage mode.

## Targeted validation

- strict mountinfo fixtures covering emulated storage, runtime, pass-through and user-primary layouts;
- escaped mountpoint decoding and propagation optional fields;
- fuzz seed/error parsing with no panic path;
- multi-user and Termux/app namespace classification fixtures;
- partial-vs-qualified visibility evidence tests, including shell-only non-qualification and topology-truncation fail-closed behavior;
- injected second-bind failure and mid-transaction cancellation proving bind rollback and exact state restoration;
- injected ownership mismatch proving rollback retains its marker and refuses to unmount;
- target namespace PID/identity revalidation in the production adapter;
- full existing Go/Python compatibility suite;
- reproducible Android arm64 backend build and flashable package contract;
- optional real-device `./devtoolw device-smoke` evidence path.

## Deliberately deferred

Network/metered/charging/storage resource policy, VFS profiles/cache governance, scheduled sync/copy/check jobs and rclone RC telemetry remain positions **6/16** and **7/16**.
