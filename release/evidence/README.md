# Release device evidence

`device-qualification.json` is generated on the actual Android/Termux qualification device and is intentionally ignored by Git and excluded from the reproducible release source digest. Desktop validation never fabricates these results.

Capture passive device/root-manager/provider/doctor facts and optional mount namespace evidence through Devtool:

```sh
./devtoolw release-evidence
```

Or capture one or more configured mounts explicitly:

```sh
python3 scripts/dev/release_device_qualification.py capture --mount drive --mount media
```

The capture helper first tries the installed typed `racctl` surface directly and then uses the device root manager (`su`) for the same fixed argv when root-owned Nexus state is not readable from the Termux app namespace. It never invokes an arbitrary shell command supplied by evidence data.

After actually exercising each endurance case, record the observed result:

```sh
python3 scripts/dev/release_device_qualification.py record reboot pass --note 'rebooted; racd reconciled mounts/jobs'
```

Use `skip` only when a case is genuinely unavailable and include the concrete reason. Before GRAND-G1:

```sh
python3 scripts/dev/release_device_qualification.py validate --require-complete
```

Validation fails closed when the Nexus version, Android identity, compatible root manager, provider readiness/rclone version/FUSE helper, or doctor report is missing or failed. GRAND-G1 additionally requires every endurance case to be complete.
