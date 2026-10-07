# Rclone Nexus

Rclone Nexus is a non-invasive Android root module that gives one packaged
rclone-family executable a native typed control plane, transactional mount
configuration, lifecycle supervision, namespace visibility diagnostics,
scheduled jobs, loopback RC telemetry, and a credential-safe WebUI.

The supported product line is **static-runtime only**: Nexus owns exactly one
runtime binary at package time and executes that binary at runtime.

## Static runtime authority

The authoritative executable is:

```text
/data/adb/modules/rclone_nexus/system/bin/rclone
```

`internal/runtimeauth` resolves that path directly. PATH,
`RNEXUS_RCLONE_BIN`, `RNEXUS_EXTERNAL_RCLONE_BIN`, `RCLONE_CONFIG`, a legacy
NewFuture module, old activation state, and the retired runtime candidate store
cannot redirect managed execution.

The managed configuration remains persistent Nexus state:

```text
/data/adb/rclone-nexus/config/rclone/rclone.conf
```

Runtime replacement is deliberately a **build/package operation**. There is no
supported on-device Runtime Manager, import, source registry, activation,
rollback, staged update, or boot promotion path. To change from rclone to
bclone, change versions, or otherwise replace the runtime, build a new module
ZIP and flash it.

A NewFuture `rclone-fuse3-magisk` installation may still exist as migration
input or as a source from which you obtain bytes for packaging, but it is never
runtime authority for the static product.

See [`docs/STATIC_RUNTIME_BUILD.md`](docs/STATIC_RUNTIME_BUILD.md) and
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Package a runtime

Provide an Android-compatible rclone-family executable when packaging:

```sh
RNEXUS_RCLONE_PREBUILT=/path/to/rclone-or-bclone \
  python3 scripts/dev/package_module.py
```

Or place it manually before packaging:

```sh
mkdir -p module/system/bin
cp /path/to/rclone-or-bclone module/system/bin/rclone
chmod 0755 module/system/bin/rclone
python3 scripts/dev/package_module.py
```

The packager writes the runtime as `system/bin/rclone` in the ZIP and binds its
bytes, mode, and size into `integrity.manifest.json`. `customize.sh` refuses a
package without that executable. At boot, `service.sh` verifies package state and
requires `racctl runtime status --require-operational` before reconciliation.

The source tree intentionally does not need to check a runtime binary into Git.
`python3 scripts/dev/check-package.py` uses an isolated deterministic fixture to
validate the packaging contract without pretending that fixture is a release
runtime.

## Ownership boundaries

- Nexus module id: `rclone_nexus`.
- Legacy/provider module id: `rclone`.
- Persistent state: `/data/adb/rclone-nexus`.
- Bundled runtime authority: `<module>/system/bin/rclone`.
- Managed rclone config: `/data/adb/rclone-nexus/config/rclone/rclone.conf`.
- Nexus does not mutate `/data/adb/modules/rclone` during normal operation or migration.
- Mount configuration is parsed as data; no mount definition is sourced or `eval`'d.
- Managed mount lifecycle is process-identity and ownership checked before signalling or unmounting.
- Android app visibility is evidence-based; Nexus does not claim universal namespace propagation.

## Configuration and lifecycle

Legacy `mounts.d/<name>.conf` files remain a v0.1 import source. The durable
configuration authority is the revisioned registry under:

```text
/data/adb/rclone-nexus/config/registry-v2.json
/data/adb/rclone-nexus/config/previous-v2.json
```

Mutation follows `candidate -> validate -> preview -> revision/digest-bound
apply`. Desired state is persistent and separate from observed process/mount
state, so an explicit stop remains stopped across reconciliation.

`racd` continuously evaluates runtime/config/FUSE/storage/network readiness and
keeps per-mount health/retry truth. A stale legacy runtime activation file has
no lifecycle authority in the static product.

## Provider migration

Migration from NewFuture is a reviewed authority handoff, not runtime selection.
Nexus reads the legacy provider as migration input, validates the provider
configuration using the bundled runtime, imports reviewed mounts/jobs disabled,
and requires the provider to be explicitly disabled before finalization.

```sh
racctl migration inspect
racctl migration preview --mount <mount-id> --job sync:1 --job-every 24h
racctl migration apply <proof> <revision> <digest> --mount <mount-id> --job sync:1 --job-every 24h
# Disable/remove the legacy provider explicitly in the root manager.
racctl migration finalize-preview
racctl migration finalize <proof> <revision> <digest>
```

Migration evidence records the SHA-256 of the **bundled** runtime used for the
handoff. It does not consult an activation store.

## Runtime status

The runtime control surface is intentionally read-only:

```sh
racctl runtime status
racctl runtime status --json
racctl runtime status --require-operational
racctl runtime executable
racctl runtime config
```

The WebUI Runtime view consumes the same `runtime.status` operation and is
status-only. It exposes no Runtime Manager/update/source/activation actions.

## Android namespace visibility

Nexus inventories mount namespaces from `/proc`, classifies observed
service/root/shell/zygote/Termux/app membership, and reports only visibility it
can prove. Optional same-path bind propagation revalidates namespace identity,
source ownership, and target safety before `setns`/bind operations. Rollback is
ownership-bound and leaves unrelated mounts untouched.

```sh
su -c 'racctl namespace inspect drive'
su -c 'racctl namespace apply drive'
su -c 'racctl namespace rollback-preview drive'
su -c 'racctl namespace rollback drive'
```

## Jobs and RC telemetry

Nexus owns typed `sync`, `copy`, and read-only `check` jobs. Jobs cannot supply
arbitrary rclone argv or on-the-fly backend syntax. Destructive sync requires
explicit confirmation. Scheduled claims persist before launch and are locked to
avoid replay after daemon restart.

Every Nexus-started mount receives a random authenticated loopback RC endpoint.
Credentials stay under root-owned runtime state; clients receive only allow-listed
metrics rather than a generic RC proxy.

## WebUI

The WebUI uses the same typed operation registry as CLI and `racd`. It has no
generic shell, arbitrary argv, arbitrary file, or generic rclone-RC endpoint.
Mount/config mutations use backend preview proofs. Logs/support data are bounded
and redacted. Remote browsing exposes configured names/entries without exposing
`rclone.conf` credentials.

## Development with DevTool

The repository uses DevTool target-local jobs and workflows. The static-runtime
finalization target is a first-class EXO workflow rather than a shell wrapper:

```sh
./devtoolw static-runtime-finalize
```

Its DAG validates the static product surface, Go runtime authority, repository-
wide Go tests, module/package contracts, and WebUI JavaScript through the owning
WebUI target. The final seal is also the validation workflow referenced by the
static-runtime DevTool overlay.

General commands remain available for the non-runtime subsystems:

```sh
./devtoolw validate
./devtoolw test
./devtoolw core-g1
./devtoolw android-g1
./devtoolw platform-g1
./devtoolw webui
```

Historical RUNTIME-STANDALONE/SOURCE/UPDATE campaign documents remain in Git as
implementation provenance. They are not active runtime product contracts and
are not part of the static-runtime finalization workflow.

## Validation

Useful direct checks:

```sh
go test ./...
python3 -m pytest -q tests/test_static_runtime_product_surface.py
python3 scripts/dev/check-module-contract.py
python3 scripts/dev/check-package.py
node --check module/webroot/app.js
node --check module/webroot/model.js
```

`check-package.py` validates packaging with a deterministic fixture runtime.
Actual distributable/release packaging requires a real Android-compatible
`RNEXUS_RCLONE_PREBUILT`.

## Build output

```text
dist/rclone-nexus-v0.1.0.zip
```

The deterministic module ZIP contains `system/bin/racctl` and exactly one
rclone-family runtime at `system/bin/rclone`, with both covered by package
integrity metadata.
