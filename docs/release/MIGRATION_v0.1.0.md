# Migration from v0.1.0-dev to v0.1.0

Rclone Nexus v0.1.0 is the first release-qualified package produced from the original v0.1.0-dev repository baseline.

Persistent state remains under `/data/adb/rclone-nexus`. Module replacement does not erase mount definitions, desired state, policy, jobs, cache metadata, health history, or the operation journal. Volatile runtime state, WebUI sessions, loopback RC credentials, and process records are rebuilt.

The migration path is deliberately fail-closed:

1. validate the existing platform-state schema without mutation during install;
2. verify the packaged integrity manifest;
3. run rollback-safe schema migration during `post-fs-data`;
4. create the private platform-ready marker only after those checks pass;
5. start `racd` from `service.sh` only when the marker exists.

Legacy `mounts.d/*.conf` data imported by early v0.1.0-dev builds remains supported through the v2 registry migration path. Private `args_file` references remain backend-only and are never exposed through the WebUI.

There is no backward-compatibility alias for the abandoned pre-release repository/module names; the canonical identity is `rclone_nexus` / Rclone Nexus.
