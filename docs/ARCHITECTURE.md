# Architecture

Rclone Nexus is intentionally a **consumer** of NewFuture's
`rclone-fuse3-magisk` module rather than a fork or replacement.

Dependency direction:

```text
rclone upstream
      |
      v
NewFuture rclone-fuse3-magisk (module id: rclone)
      |  rclone + FUSE runtime + canonical rclone.conf
      v
Rclone Nexus (module id: rclone_nexus)
      |  lifecycle + Android integration + diagnostics
      v
managed mount instances
```

## Ownership boundaries

Rclone Nexus MUST NOT:

- bundle its own `rclone`, `fusermount3`, or libfuse runtime;
- edit files inside `/data/adb/modules/rclone`;
- replace NewFuture's boot/service scripts;
- assume module updates preserve files stored under its own module directory.

Persistent Rclone Nexus state is therefore rooted at `/data/adb/rclone-nexus`.
The initial implementation owns mount definitions, PID/runtime state, logs and
per-mount VFS cache there.

## Initial mount contract

Each `mounts.d/<name>.conf` file uses literal `key=value` records and is parsed
without `source` or `eval`. Supported fields are currently:

- `enabled`
- `remote`
- `mountpoint`
- `vfs_cache_mode`
- `allow_other`
- `log_level`
- `args_file`

An optional `args_file` contains exactly one literal rclone argument per line.
This keeps arbitrary shell syntax outside the configuration trust boundary.

## Planned boundaries

The initial repository deliberately leaves namespace propagation, richer health
classification, network/battery policy, RC metrics, and a WebUI for later
milestones. Those features should extend the Rclone Nexus layer rather than fork
the provider module.
