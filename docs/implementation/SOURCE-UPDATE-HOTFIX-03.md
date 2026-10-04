# SOURCE/UPDATE HOTFIX-03 — Published Android build bridge + pinned commits

Closes two coupled gaps: SOURCE-X02 build bundles were not consumable by UPDATE-X01, and GitHub pinned-commit resolutions had no release asset and therefore could not complete `import-resolution`.

The canonical SOURCE-X02 workflow now publishes an immutable, commit-keyed build tar. `runtimeacquire` is the single production acquisition authority used by CLI, typed control and UPDATE-X01. It routes build-required GitHub resolutions and commit-only resolutions through the published Android bundle, re-verifies provenance/hashes/Android ELF, persists a `source-build` resolution, and only then uses the normal runtime-store acquire/qualify/stage pipeline.

Verified build bytes are materialized into content-addressed durable Nexus source state before the `source-build` resolution is persisted. Published-download temporary directories are never referenced by canonical resolutions, so the resulting immutable resolution remains replayable after acquisition cleanup and across later imports.


## v4 Termux publication portability

The durable build store does not require hard links. After the temporary file is fully copied, synced, closed, and re-hashed, Nexus atomically renames it within the same private `runtime/sources/builds` directory to the SHA-256 content address, then re-verifies the persisted digest and size. This avoids Android/Termux `link(2)` permission denial while preserving immutable content-addressed replay semantics.
