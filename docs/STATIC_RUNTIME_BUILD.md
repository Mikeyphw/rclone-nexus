# Static runtime build

Rclone Nexus is a singular-runtime module. The flashable ZIP contains exactly one rclone-family executable:

```text
system/bin/rclone
```

That executable is the only supported runtime authority. It may contain rclone or bclone bytes, but the choice is made at package time. Replacing it means building and flashing a new module ZIP; Nexus has no live runtime import, activation, candidate store, source registry, staged update, or runtime rollback path.

## Build with a prebuilt runtime

```sh
RNEXUS_RCLONE_PREBUILT=/path/to/rclone-or-bclone \
  python3 scripts/dev/package_module.py
```

The packager copies the supplied bytes into the ZIP as `system/bin/rclone`, marks the entry executable, and records it in `integrity.manifest.json`.

A developer may instead place the runtime in the module tree before packaging:

```sh
mkdir -p module/system/bin
cp /path/to/rclone-or-bclone module/system/bin/rclone
chmod 0755 module/system/bin/rclone
python3 scripts/dev/package_module.py
```

Release artifact generation deliberately requires an explicit `RNEXUS_RCLONE_PREBUILT`; it never silently manufactures or discovers a release runtime from PATH/provider state.

## Runtime behavior

At boot, `service.sh` verifies the installed module payload and checks:

```sh
racctl runtime status --json --require-operational
```

The canonical executable is:

```text
/data/adb/modules/rclone_nexus/system/bin/rclone
```

Provider presence, PATH, old runtime state files, and compatibility environment variables do not redirect managed execution.

The supported `racctl runtime` commands are inspection-only:

```text
status
executable
config
```

The typed backend similarly exposes `runtime.status` only. The WebUI projects that status as Bundled Runtime information instead of a Runtime Manager.
