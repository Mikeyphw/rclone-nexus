# CORE-X01 — native control plane + typed operation protocol

Status: **implemented; pending CORE-G1 gate qualification**.

Campaign position: **1/16**. This overlay merges the original CORE-X01 and
CORE-X02 scope exactly as defined by the compressed roadmap.

## Delivered promise ledger

- Added the dependency-free Go `racctl` backend and internal `racd` daemon mode.
- Reduced `rclone-nexus` and `rclone-mountctl` to compatibility launchers over
  the native backend; `rclone-doctor` remains a shell diagnostic until its later
  owning diagnostics scope.
- Added deterministic Android Go builds for arm64-v8a with ABI mapping already
  defined for armeabi-v7a, x86_64 and x86.
- Added typed provider discovery for module, rclone, FUSE device/helper and
  configuration readiness without exposing provider/config/state paths through
  the machine provider contract.
- Enforced private `0700` state/runtime directories and `0600` daemon socket and
  lock files; root execution additionally normalizes ownership to uid/gid 0.
- Added exclusive daemon locking, Unix-domain transport and clean context/signal
  shutdown with socket cleanup.
- Added version/capabilities JSON, protocol version negotiation, strict request
  envelopes, typed response envelopes and NDJSON events.
- Added a fixed operation registry with query, preview, run, cancel and
  reconcile classes; no generic command/argv/shell execution exists.
- Added stable machine error codes separated from human-readable detail.
- Added response/event/request bounds and recursive secret redaction before
  transport serialization.
- Added read-only `provider.status`, `mount.list` and `mount.status` operations,
  plus start preview and compatibility lifecycle operations needed to preserve
  the v0.1 CLI surface until LIFE-X01 replaces the lifecycle internals.
- Added active-request cancellation plumbing for daemon-owned run/reconcile
  operations.
- Added Devtool-owned Go test and reproducible Android build jobs while
  retaining the existing shell/module-contract/compatibility jobs.

## Deliberately deferred to LIFE-X01/LIFE-X02

CORE-X01 preserves the v0.1 mount lifecycle semantics in Go; it does **not**
claim the later transactional configuration registry, process identity proofs,
operation journal, richer health states, retry/backoff, or stale mount recovery.
Those remain owned by positions 2/16 and 3/16.

## Targeted validation owned by this overlay

- `go test ./...`
- deterministic `GOOS=android GOARCH=arm64` racctl build performed twice and
  SHA-256 compared
- shell syntax validation
- module contract validation
- Python compatibility/lifecycle tests
- protocol golden/invalid-schema tests
- protocol range negotiation tests
- provider path non-disclosure test
- request/response/event bound tests
- recursive redaction tests
- daemon singleton and shutdown cleanup tests
- deterministic module package contract
