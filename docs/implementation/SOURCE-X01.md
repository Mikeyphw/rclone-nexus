# SOURCE-X01 — Source registry + latest/pinned semantics

## Scope

SOURCE-X01 is full-plan position 5 of 11 and the first source/update gate-window implementation scope. It closes canonical promises `RNX-P374..RNX-P390` without moving runtime qualification out of the immutable X02 runtime store.

The production authority is `internal/runtimesource`. It owns source definitions, channel semantics, immutable resolution documents and the hand-off into `runtimestore.Import`.

## First-class registry

Three immutable built-ins are always present:

- `bclone` → `BenjiThatFoxGuy/bclone`, engine `bclone`, `latest-stable` by default;
- `rclone` → `rclone/rclone`, engine `rclone`, `latest-stable` by default;
- `newfuture` → `NewFuture/rclone-fuse3-magisk`, engine `rclone`, `latest-stable` by default.

Custom registry entries cover arbitrary GitHub repositories, explicit HTTPS URLs, local binaries, and local/custom source-build outputs. Built-ins cannot be shadowed or removed. Custom registry mutation is serialized and published atomically under the root-owned runtime source state directory.

## Channels

The typed channels are:

- `latest-stable`: resolve GitHub's current stable release at check time; draft/prerelease metadata is rejected;
- `pinned-release`: resolve exactly the requested release tag, then peel the tag to an immutable commit;
- `pinned-commit`: require a full 40-hex Git commit and verify that exact object;
- `manual-only`: no automatic mutable-ref lookup; local bytes are hashed and URL sources require an explicit SHA-256.

No bclone version is compiled into Nexus. `latest-stable` always queries the resolver when a new check is requested.

## Immutable GitHub resolution

A GitHub release resolution persists all authority required by later stages:

- exact requested repository and numeric repository ID;
- numeric release ID and release tag;
- peeled 40-hex commit SHA;
- numeric asset ID, exact asset name and asset API URL;
- GitHub-provided SHA-256 digest when available;
- registry revision and source-spec digest.

The import hand-off uses the numeric `releases/assets/<asset-id>` API URL, never `/releases/latest/download/...` and never a tag-based browser URL. Thus a later change to the `latest` endpoint or a retargeted tag cannot silently redirect an already-persisted resolution.

Metadata redirects are same-origin only. Repository identity must resolve to the requested `OWNER/REPO`. Stable resolution rejects draft/prerelease releases. Asset metadata must bind the expected repository, tag and numeric asset ID. Untrusted browser-download domains are rejected.

## Manual and local provenance

- URL sources are `manual-only` and require a caller-supplied SHA-256 before a resolution can exist.
- Local binaries are hashed at resolution time and their byte digest is persisted.
- Source-build outputs bind the local output digest to an exact repository and full commit SHA.

`runtimestore.Import` now accepts the resolution ID, release/asset IDs and expected SHA-256. It checks an expected digest before candidate publication. GitHub asset downloads use the asset API form and allow redirects only to GitHub's trusted asset domains; arbitrary URL redirects remain same-origin.

## CLI production ingress

The production CLI exposes the same authority:

```text
racctl runtime source list
racctl runtime source show SOURCE_ID
racctl runtime source register --id ID --kind KIND [options]
racctl runtime source remove SOURCE_ID
racctl runtime source resolve SOURCE_ID [--channel CHANNEL] [--ref REF]
racctl runtime source resolutions
racctl runtime source inspect-resolution RESOLUTION_ID
racctl runtime source import-resolution RESOLUTION_ID
```

`import-resolution` never re-resolves a mutable ref. It imports exactly the persisted resolution through the X02 runtime store/qualifier.

## Adversarial closure

The executable SOURCE-X01 gate covers:

- latest endpoint changing after a resolution was persisted;
- release-tag retargeting after a resolution was persisted;
- missing expected release asset;
- repository owner/name mismatch;
- prerelease/draft returned for a stable request;
- metadata redirect escaping the trusted GitHub API origin;
- mutable branch names presented as `pinned-commit`;
- expected SHA mismatch before runtime candidate publication;
- builtin shadow/removal attempts.

SOURCE-X02 remains responsible for reproducible Android source-build automation when resolved release assets are unsuitable.
