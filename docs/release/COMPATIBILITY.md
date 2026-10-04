# Rclone Nexus v0.1.0 compatibility and evidence matrix

Compatibility claims are capability- and evidence-based. The release does not infer app visibility merely because a root shell can see a FUSE mount.

| Area | v0.1.0 contract |
| --- | --- |
| Managed runtime | Nexus owns the canonical runtime/config selection through its immutable qualified runtime store and durable activation state. PATH, `RCLONE_CONFIG`, provider state, and `runtime/active/bin/rclone` cannot override managed selection. |
| External/provider compatibility | NewFuture's rclone/FUSE module is optional and is used only for explicit external compatibility or migration/bootstrap. Nexus does not patch it or treat it as managed authority. |
| Runtime qualification | Candidate bytes must pass architecture, Android execution, RC runtime, real FUSE smoke-mount, signal termination, process ownership, and post-qualification hash checks before activation. |
| Magisk | Module hooks supported when detected; standalone authenticated loopback WebUI is universal. |
| KernelSU / KernelSU Next | Module hooks supported when detected; embedded WebUI is used only when a compatible manager bridge is actually present. |
| APatch | Module hooks supported when detected; embedded bridge is capability-gated with standalone fallback. |
| Unknown compatible managers | A modules root proves only module-layout compatibility; unobserved service/action/uninstall/WebUI capabilities remain false. |
| Android namespace visibility | Root/service/shell/zygote/Termux/app visibility is reported from mount-namespace evidence. Propagation is opt-in, same-path, ownership-tracked and requalified after churn. |
| Multi-user Android | Discovery records distinct users/namespaces when observable. A user/namespace class not exercised on the release device is not claimed as qualified. |

The release device evidence records the exact manufacturer/model, Android SDK/release/fingerprint, kernel, root-manager capabilities/version, canonical runtime-authority identity and rclone version, plus optional provider compatibility identity when present and required per-mount namespace inspection results. GRAND-G1 consumes schema-v3 executable endurance evidence. PASS is derived from chained machine observations and case-specific transition predicates; a user-authored result string is not evidence. Only Android user/namespace change may be machine-skipped when the qualification device exposes exactly one user.

### NewFuture migration compatibility

Migration recognizes the NewFuture module id/path and its `conf/rclone.conf`,
`conf/sync` and `conf/copy` inputs as read-only compatibility sources. Provider
job options that cannot be represented by Nexus's typed job model are reported
but not imported. Existing provider mounts are discovered from provider-owned
processes and must be selected explicitly. A provider that disappears, refuses
to stop, or becomes active again after standalone finalization fails closed.
