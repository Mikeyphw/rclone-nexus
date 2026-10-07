# Static runtime build

Rclone Nexus is a singular-runtime module. Every flashable ZIP owns one rclone-family executable and one FUSE helper authority:

```text
system/bin/rclone
system/vendor/bin/fusermount3
```

The `rclone` entry may contain either NewFuture rclone or source-built bclone bytes. The choice is made at build time and cannot be changed on-device. `fusermount3` is different: **it is always sourced from the official `NewFuture/rclone-fuse3-magisk` arm64 module artifact**, regardless of which rclone-family runtime is selected. NewFuture `libfuse*.so*` payloads required by that helper are carried with it.

Runtime replacement means building and flashing another Nexus ZIP. Nexus has no live runtime import, activation, candidate store, source registry, staged update, or runtime rollback path.

## Native DevTool workflows

NewFuture rclone is the default build provider:

```sh
./devtoolw runtime-newfuture
./devtoolw build-newfuture
```

`./devtoolw build` is equivalent to the NewFuture package path.

To build bclone for Android/arm64 from its canonical source repository and package it with the NewFuture FUSE helper:

```sh
./devtoolw runtime-bclone
./devtoolw build-bclone
```

The bclone workflow resolves a stable `BenjiThatFoxGuy/bclone` release tag to an immutable commit, clones that exact source, and builds it with the repository's qualified Android NDK path. The output is still installed as `system/bin/rclone`, which is the Nexus runtime ABI.

Release qualification has matching workflows:

```sh
./devtoolw release-newfuture
./devtoolw release-bclone
```

The release workflow materializes the selected runtime once and uses the same immutable input directory for reproducible package generation.

## Direct script interface

The provider can also be selected directly:

```sh
python3 scripts/dev/runtime_inputs.py materialize \
  --provider newfuture \
  --output-dir build/runtime/newfuture

python3 scripts/dev/runtime_inputs.py materialize \
  --provider bclone \
  --output-dir build/runtime/bclone
```

Packaging consumes a materialized directory:

```sh
python3 scripts/dev/package_module.py \
  --runtime-dir build/runtime/newfuture
```

or lets the packager materialize the provider itself:

```sh
python3 scripts/dev/package_module.py --runtime-provider newfuture
python3 scripts/dev/package_module.py --runtime-provider bclone
```

`runtime.provenance.json` is placed in the module ZIP and records both runtime provenance and the independent NewFuture FUSE-helper provenance. `integrity.manifest.json` binds the runtime, helper, helper libraries, and provenance bytes.

## Advanced/offline overrides

`RNEXUS_RCLONE_PREBUILT` remains an explicit advanced override for the rclone-family executable. It is not required for normal NewFuture or bclone builds:

```sh
RNEXUS_RCLONE_PREBUILT=/path/to/rclone-or-bclone \
python3 scripts/dev/package_module.py --runtime-provider prebuilt
```

Even in `prebuilt` mode, `fusermount3` still comes from NewFuture. For an offline build, provide an official NewFuture arm64 module ZIP:

```sh
RNEXUS_NEWFUTURE_ARCHIVE=/path/to/magisk-rclone_arm64-v8a.zip \
RNEXUS_RCLONE_PREBUILT=/path/to/rclone-or-bclone \
python3 scripts/dev/package_module.py --runtime-provider prebuilt
```

Optional pins are available through `RNEXUS_NEWFUTURE_TAG` and `RNEXUS_BCLONE_REF`. GitHub authentication may be supplied through `RNEXUS_GITHUB_TOKEN`, `GITHUB_TOKEN`, `GH_TOKEN`, or an authenticated `gh` CLI.

## Runtime behavior

At boot, `service.sh` verifies the installed package and checks:

```sh
racctl runtime status --json --require-operational
```

Managed execution always resolves:

```text
/data/adb/modules/rclone_nexus/system/bin/rclone
```

Managed mount helper resolution prefers:

```text
/data/adb/modules/rclone_nexus/system/vendor/bin/fusermount3
```

A separately installed NewFuture module may still exist as migration input, but it cannot redirect managed execution or helper selection. The supported `racctl runtime` commands remain inspection-only: `status`, `executable`, and `config`.
