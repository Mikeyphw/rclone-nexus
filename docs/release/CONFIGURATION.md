# Rclone Nexus v0.1.0 configuration reference

## Mounts

Mount definitions are typed data in the revisioned registry. Each mount has a remote, mountpoint, desired state and optional resource settings. Mutations use preview/apply with a revision, digest and one-use proof. Duplicate or overlapping mountpoints are rejected. Private `args_file` state is preserved server-side and cannot be supplied through browser payloads.

## VFS profiles

Named VFS profiles are `streaming`, `balanced`, `offline`, and `minimal`; `custom` preserves existing explicit behavior. Profiles are opt-in. Policy-only edits do not restart a healthy mount, while VFS-affecting edits are restart-sensitive.

## Resource policy

Mount/job policy can require `any`, `wifi`, `unmetered`, or `offline-allowed` networking and can gate work on charging, minimum battery, cache free space, boot settle, and network settle. Unknown metering fails closed for an `unmetered` requirement. A running valid VFS mount is retained through an outage when safe.

## Cache

Cache operations are ownership-bounded. Status/prune are typed operations; destructive clear/forget require a fresh preview proof. Automatic pruning runs only while the mount is stopped and converges from the high-water mark toward the configured low-water target.

## Jobs

Managed jobs support only `sync`, `copy`, and read-only `check`. Endpoints cannot contain rclone on-the-fly backend syntax or option-like argv. Destructive `sync` must be previewed and explicitly confirmed before publication. The scheduler persists `next_run` before launch and holds a per-job lock to prevent duplicate due execution after restart.

## WebUI settings

The WebUI settings resource is revisioned and backend-owned. The browser never edits root files directly. Standalone sessions, loopback RC credentials and other volatile secrets are not configuration and are regenerated.
