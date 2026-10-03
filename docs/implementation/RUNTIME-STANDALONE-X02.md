# RUNTIME-STANDALONE X02 — Runtime store + qualification

## Position

- Campaign: `RUNTIME-STANDALONE`
- Canonical promise range: `RNX-P308..RNX-P348` (41 promises)
- Gate-window position: implementation overlay 2 of 3 before `RUNTIME-G1`
- Full-plan position: 2 of 11
- Next overlay: `RUNTIME-X03 — Transactional activation + rollback`

## Production authority added

X02 adds a Nexus-owned immutable candidate store at:

```text
/data/adb/rclone-nexus/runtimes/<runtime-id>/
├── rclone
└── manifest.json
```

`racctl runtime import` is the production intake. It snapshots source bytes into the store first and only then invokes the canonical qualifier. The source path or download location never becomes the execution authority.

Runtime IDs bind **engine + binary bytes + canonical source provenance**, so the same bytes imported from different provenance do not overwrite one another's source history.

X02 deliberately does **not** change `/runtime/active` or activation state. X03 owns activation, rollback, previous-runtime retention, and reboot-safe switching.

## Import sources

The one intake path supports:

- `local-file` — local binary file;
- `executable-path` — explicit advanced executable input, snapshotted before qualification rather than trusted in place;
- `url` — direct URL download;
- `github-release` — explicit repository + immutable resolved ref + asset name + asset URL;
- `source-build` — locally/CI-built binary plus repository + immutable resolved ref provenance;
- `newfuture-derived` — NewFuture-derived local/downloaded candidate.

Mutable GitHub/latest resolution policy is intentionally deferred to SOURCE-X01. X02 requires an already resolved ref for the GitHub/source-build forms and records that immutable identity.

## Manifest and evidence

Each candidate manifest records:

- schema version and runtime ID;
- engine and version output;
- source type, repository, resolved ref, asset name and sanitized asset URL where applicable;
- source/archive SHA-256 and stored binary SHA-256;
- ELF class/data/machine/OSABI/type and normalized architecture;
- import timestamp;
- qualifier version;
- qualification state, checks, byte binding, and physical evidence paths.

Credential-bearing URL userinfo/query/fragment data is not persisted. Qualification detail strings are bounded and redact secret-like values.

## Qualification authority

All runtime commands executed by the qualifier are launched from an already-open candidate file descriptor (`/proc/self/fd/3`) rather than by reopening the candidate pathname. This prevents a path swap after hashing from redirecting version/help/config/FUSE execution.

The production qualifier requires:

1. executable regular file and manifest hash match;
2. valid ELF metadata;
3. arm64 on arm64 Android;
4. parseable rclone-compatible `version` output;
5. config parsing and remote enumeration;
6. `mount` command help plus the exact Nexus generated global/command/RC flag contract;
7. a real Android identity before Android qualification can succeed;
8. real root + `/dev/fuse` + fusermount3 for the local FUSE smoke;
9. readable file through the smoke mount;
10. authenticated loopback RC `core/version` response;
11. `/proc/<pid>/exe` byte identity equal to the candidate;
12. clean SIGTERM termination;
13. post-qualification SHA-256 equal to the imported bytes;
14. sanitized qualification evidence.

A non-Android host can exercise static/process compatibility checks, but the result is `blocked_by_environment`, never `qualified`.

## Adversarial evidence

X02 includes executable negative tests for:

- truncated ELF;
- wrong-architecture policy on arm64 Android;
- fake version output;
- unsupported generated/global flag;
- command-specific/global-help split;
- candidate disappearance during production qualification;
- candidate pathname replacement between hash and launch;
- secret-bearing diagnostics and URL provenance.

The TOCTOU test proves two facts separately: the pinned launch still executes the original inode, and final path re-hashing rejects the candidate because the canonical stored path no longer contains the qualified bytes.

## Qualification status

`RNX-P308..RNX-P348` is implemented and wired into the production CLI. Six promises remain intentionally nonterminal until a real rooted Android/FUSE qualification run exists:

- `RNX-P330` Android execution compatibility;
- `RNX-P335` live RC support during an actual runtime smoke mount;
- `RNX-P336` temporary local FUSE smoke mount;
- `RNX-P337` real signal/termination behavior;
- `RNX-P338` real process ownership capture;
- `RNX-P341` rejection of a Linux-arm64 candidate that executes but cannot satisfy the Android/FUSE contract.

Those six are `BLOCKED_BY_ENVIRONMENT`, not synthetically promoted to production-qualified. The remaining 35 X02 promises are `IMPLEMENTED_AND_PRODUCTION_ADOPTED` with executable evidence.
