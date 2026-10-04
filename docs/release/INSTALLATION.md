# Rclone Nexus v0.1.0 — installation, update, uninstall and recovery

## Install

Install `rclone-nexus-v0.1.0.zip` with a supported root-module manager. **Managed mode is standalone and does not require `/data/adb/modules/rclone`.** Rclone Nexus does not bundle a preselected rclone/FUSE runtime in the module ZIP; instead, managed mode imports a candidate into the Nexus-owned immutable runtime store, qualifies the exact bytes on the device, and activates only a qualified runtime.

A NewFuture `rclone` provider remains optional for migration and explicit `external` compatibility mode. Nexus never mutates that module or treats it as managed-mode authority.

Reboot after first installation so `post-fs-data` can verify package integrity, migrate persistent state, and create the private platform-ready marker. Then import and activate a runtime before expecting managed mounts to become operational. `./devtoolw install-verify` requires the canonical runtime authority to report operational after reboot; provider information, when present, is compatibility evidence only.

`./devtoolw install-stack` is the explicit legacy/external bootstrap path. It can fetch and verify NewFuture's provider when missing, but using it does not transfer managed-runtime authority to that provider.

## Update

Updates are in-place module replacements. Persistent user state under `/data/adb/rclone-nexus` is retained, including the immutable runtime store and activation state. The new package is integrity-checked before state migration. Unsupported future schemas, missing integrity metadata, or migration failure stop startup rather than partially running the new daemon.

Before updating, create a support bundle and preserve a copy of the persistent state directory if you need an external recovery point. Do not copy RC credentials or WebUI session state; those are volatile and regenerated.

## Uninstall

Normal uninstall stops Nexus-owned mounts, releases Nexus-owned namespace binds, removes volatile runtime state, and preserves persistent configuration/cache data. Any optional NewFuture provider is left untouched.

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

A failed package-integrity check should be resolved by reinstalling the exact release package. A managed-runtime failure should be resolved through the runtime import/qualification/activation surfaces rather than by patching compatibility projections. An external-provider failure belongs to the provider only when explicit external compatibility mode is selected; Nexus must not patch files inside `/data/adb/modules/rclone`. Migration failure preserves prior persistent state and must be resolved before forcing startup.

### Standalone migration from NewFuture rclone

The normal Nexus package does not require or install NewFuture's provider module.
For an existing provider install, use `racctl migration inspect/preview/apply` to
copy validated configuration and reviewed definitions into Nexus. Nexus stops at
`AWAITING_PROVIDER_DISABLE`; disable/remove the old provider explicitly with your
root manager, then use `migration finalize-preview` and `migration finalize`.
Nexus does not silently uninstall or mutate `/data/adb/modules/rclone`.
