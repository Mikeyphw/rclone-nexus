# Static runtime build

Rclone Nexus is now a static-runtime module. The flashable ZIP contains exactly one rclone-family binary:

```text
system/bin/rclone
```

That binary is the only supported runtime authority. It may be copied from a NewFuture rclone artifact or from a separate bclone build, but the choice is made at package time. Replacing the runtime means building and flashing a new module ZIP.

## Build with a prebuilt runtime

```sh
RNEXUS_RCLONE_PREBUILT=/path/to/rclone-or-bclone \
  python3 scripts/dev/package_module.py
```

The packager copies the supplied file into the ZIP as `system/bin/rclone`, records it in `integrity.manifest.json`, and marks it executable.

You may also place the runtime manually before packaging:

```sh
mkdir -p module/system/bin
cp /path/to/rclone-or-bclone module/system/bin/rclone
chmod 0755 module/system/bin/rclone
python3 scripts/dev/package_module.py
```

## Runtime behavior

At boot, `service.sh` checks `racctl runtime status --require-operational` and then starts the daemon. It does not recover runtime activation, promote staged runtime updates, or roll back a runtime candidate.

The supported `racctl runtime` commands are:

```text
status
executable
config
```

Runtime import, runtime source registry, live activation, rollback, Runtime Manager, and runtime update commands are intentionally unsupported in the static-runtime product line. They are not exposed through the CLI runtime command surface or the typed control/RPC capabilities. The module remains the mount/config/WebUI/control layer; the runtime binary is just part of the module payload.
