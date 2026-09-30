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

## Native control-plane boundary (CORE-X01)

`racctl` is now the native authority shared by compatibility CLI launchers and
`racd`. Machine clients use a versioned, bounded, redacted JSON protocol over a
root-owned Unix socket. The registry exposes only fixed typed operations; there
is no arbitrary shell/argv RPC. Provider discovery reports readiness facts but
never provider/config/state filesystem paths.

The v0.1 lifecycle behavior has been ported into Go only to preserve compatibility
until LIFE-X01/LIFE-X02 replace it with the authoritative transactional lifecycle.

## Planned boundaries

Namespace propagation, richer health classification, network/battery policy, RC
metrics, and the WebUI remain later milestones. Those features extend the same
control plane rather than fork the provider module.
