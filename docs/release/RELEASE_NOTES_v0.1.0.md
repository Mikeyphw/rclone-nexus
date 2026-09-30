# Rclone Nexus v0.1.0 release notes

Rclone Nexus v0.1.0 is the first release-qualified companion module for NewFuture's Android rclone/FUSE provider. It adds a native typed control plane, per-mount lifecycle and self-healing, Android namespace visibility management, resource/VFS/cache policy, scheduled sync/copy/check jobs, local-only authenticated rclone RC telemetry, diagnostics/support bundles, root-manager capability abstraction, safe state migration, and a secure WebUI.

## Security model

The WebUI has no arbitrary shell, argv, file-reader or generic rclone-RC endpoint. Standalone mode is authenticated on ephemeral IPv4 loopback with one-use bootstrap tokens, short-lived sessions, Origin/Host/CSRF enforcement and bounded payloads. Embedded KernelSU/APatch transport is capability-gated and reuses the same typed backend registry. Destructive configuration, namespace and cache mutations require one-use backend preview proofs.

## Release artifacts

REL-X01 produces `rclone-nexus-v0.1.0.zip`, `SHA256SUMS`, and `release-manifest.json`. Packaging is byte-reproducible from the same release source and embeds an integrity manifest covering packaged file hash, size and mode.

## Known limitations

- Rclone Nexus depends on the separately installed NewFuture provider and does not repair provider-specific failures.
- Ordinary-app FUSE visibility varies with Android/OEM namespace topology; only evidence-observed visibility classes should be treated as qualified.
- Multi-user/secondary-user behavior must be qualified on a device that actually exposes those users/namespaces.
- Network/battery observations rely on Android facilities available to the root environment; unknown evidence fails closed for restrictive policies.
- The WebUI intentionally does not expose provider credentials or arbitrary rclone commands.
- Real reboot, doze, root-manager restart, provider-update and long-duration network/storage transitions require device evidence and are not simulated into a pass by desktop validation.
