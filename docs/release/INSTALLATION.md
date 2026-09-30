# Rclone Nexus v0.1.0 — installation, update, uninstall and recovery

## Install

Install `rclone-nexus-v0.1.0.zip` with a supported root-module manager after installing NewFuture's rclone/FUSE provider. Rclone Nexus does not bundle rclone or FUSE and does not modify the provider module. Reboot after first installation so `post-fs-data` can verify package integrity, migrate persistent state, and create the private platform-ready marker before `racd` starts.

## Update

Updates are in-place module replacements. Persistent user state under `/data/adb/rclone-nexus` is retained. The new package is integrity-checked before state migration. Unsupported future schemas, missing integrity metadata, or migration failure stop startup rather than partially running the new daemon.

Before updating, create a support bundle and preserve a copy of the persistent state directory if you need an external recovery point. Do not copy RC credentials or WebUI session state; those are volatile and regenerated.

## Uninstall

Normal uninstall stops Nexus-owned mounts, releases Nexus-owned namespace binds, removes volatile runtime state, and preserves persistent configuration/cache data. The NewFuture provider is left untouched.

Destructive persistent-state removal is one-shot and explicit:

```sh
su -c 'rclone-nexus platform purge-on-uninstall enable'
```

Verify the marker before uninstall. Do not enable it when you intend to reinstall or preserve configuration.

## Recovery

If startup is blocked, run:

```sh
su -c 'rclone-nexus platform verify-integrity'
su -c 'rclone-doctor'
su -c 'rclone-doctor --bundle'
```

A failed package-integrity check should be resolved by reinstalling the exact release package. A provider failure should be resolved in the provider module; do not patch files inside `/data/adb/modules/rclone` from Nexus. Migration failure preserves the prior persistent state and must be resolved before forcing startup.
