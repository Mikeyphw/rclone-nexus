# Release device evidence — executable schema v3

`device-qualification.json` is private, gitignored real-device evidence. GRAND-G1 rejects legacy v1/v2 evidence and assertion-only `status: pass` records.

Start on the Android/Termux qualification device with Nexus installed, the canonical managed runtime authority operational, and at least one real configured mount. A NewFuture provider is optional compatibility evidence and is not required for managed-mode qualification:

```sh
./devtoolw release-evidence
python3 scripts/dev/release_device_qualification.py status
```

`capture` now auto-discovers configured mounts from the typed Nexus configuration snapshot. `--mount NAME` remains available only to restrict the set; it is no longer required to avoid empty namespace evidence.

Each endurance case is then started with:

```sh
python3 scripts/dev/release_device_qualification.py run reboot
```

For device actions the harness should not perform itself (reboot, root-manager/runtime-or-provider reload, radio/doze/storage transitions, remote outage/auth changes, Android user switching, scheduled-job waiting), `run` records the machine baseline and prints the exact next action. After performing it, use:

```sh
python3 scripts/dev/release_device_qualification.py resume reboot
```

A case remains resumable until the required transition is actually observed. The harness does not accept a user-authored PASS.

Three owned perturbations are executable directly through `run`: `stale_fuse_killed_rclone` kills only the machine-observed Nexus-owned rclone PID and verifies stale-FUSE recovery; `daemon_crash_restart` kills the observed `racd` and verifies restart through Nexus' module service hook; `webui_reopen_idle_expiry` starts a 5-second loopback server, verifies idle expiry, and reopens through the typed WebUI surface.

Useful commands:

```sh
python3 scripts/dev/release_device_qualification.py status
python3 scripts/dev/release_device_qualification.py run stale_fuse_killed_rclone
python3 scripts/dev/release_device_qualification.py run daemon_crash_restart
python3 scripts/dev/release_device_qualification.py run webui_reopen_idle_expiry
python3 scripts/dev/release_device_qualification.py validate
python3 scripts/dev/release_device_qualification.py validate --require-complete
```

The eleven generally applicable scenarios must have valid machine-observation PASS proofs. `android_user_namespace_change` may be skipped only when the harness itself observes exactly one Android user; arbitrary skip reasons are rejected.

The evidence hash chain detects accidental/manual rewriting of observations. It is an integrity/provenance mechanism, not hardware attestation; GRAND-G1 additionally reruns the source, gate, package and security qualification around the private device evidence.
RUNTIME-G1 mutable boot evidence is captured as immutable qualification snapshots before gate cleanup. Desired-state, supervisor health, mount/service logs, and boot-service artifacts must therefore reference the gate-owned `qualification/.../evidence/snapshot/` tree and record the originating live `source_path`; direct hashes of mutable live state are not accepted.
RUNTIME-G1 device evidence is deliberately ephemeral. `runtime-g1-device-qualification.json` must remain gitignored and untracked; workflows that consume it capture a fresh copy immediately before the audit and remove it afterward. Artifact validation also removes it on exit so validate-before-commit transactions cannot stage private device evidence into Git.


### SOURCE-G1 private supply-chain evidence

`source-g1-supply-chain-qualification.json` is generated only while SOURCE-G1 is validating. It binds real external source resolutions to downloaded archive/runtime bytes, live activation/rollback process identity, adversarial failure results, and root-owned immutable evidence snapshots. It is intentionally gitignored, excluded from release source digests, and removed before Devtool creates the SOURCE-G1 commit.
