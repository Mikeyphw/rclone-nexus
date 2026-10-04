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

### SOURCE-G1 canonical closure

SOURCE-G1 seals RNX-P374..RNX-P434 (61 cumulative promises). RNX-P426..P434
are the gate-owned real-execution obligations and are qualified only while the
private rooted/network evidence is present inside validation; that evidence is
then removed before commit. `source-g1-audit` is the durable executable evidence
node for those promises.

### MIGRATE-X01

`migrate-x01-audit` is the executable position-9 evidence node. It binds all
35 merged promises (`RNX-P106..RNX-P126`, `RNX-P435..RNX-P448`) to production
migration/package behavior, including a compiled `racctl` fixture migration and
negative-state Go tests. Durable per-install migration evidence itself lives in
`$RNEXUS_STATE_DIR/migration/evidence/` and is never committed to the repository.

### RUNTIME-GRAND-G1-A private device qualification

`runtime-grand-g1-device.json` is the resumable position-11 device matrix. G1-A never seals the campaign. It composes the current RUNTIME-G1, SOURCE-G1 and release endurance evidence, then adds actual latest bclone/rclone Android builds, live runtime switching, qualification rejection, rooted MIGRATE-X01, diagnostic redaction, crash recovery, configured-remote browsing and a staged-next-reboot proof.

Start/capture on the rooted Android qualification device:

```sh
./devtoolw runtime-grand-g1-a
python3 scripts/dev/runtime_grand_g1_device.py capture
python3 scripts/dev/runtime_grand_g1_device.py status
```

Run/resume the remaining machine-observed release cases using `run-release-case` / `resume-release-case`, and perform the dedicated staged-runtime reboot with:

```sh
python3 scripts/dev/runtime_grand_g1_device.py prepare-staged-reboot
# reboot the device
python3 scripts/dev/runtime_grand_g1_device.py resume-staged-reboot
python3 scripts/dev/runtime_grand_g1_device.py validate --require-complete
```

No manual/user-authored PASS is accepted. The evidence is private and gitignored. Its source binding covers executable product/device behavior rather than final-gate ledger metadata, allowing G1-B to modify governance-only files without invalidating the physical device observations; G1-B separately binds the complete final source tree.

