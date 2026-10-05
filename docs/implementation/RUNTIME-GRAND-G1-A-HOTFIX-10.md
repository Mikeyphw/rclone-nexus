# RUNTIME-GRAND-G1-A HOTFIX-10 — isolated Go module authority

## Finding

Real SOURCE-X02 bclone compilation reached the Go dependency phase but failed with `no required module provides package` for packages that are present in the exact module versions pinned by the bclone commit. The failing providers were already present in `go.mod`/`go.sum`, which exposed host Go module-cache contamination rather than an upstream source defect.

## Remediation

SOURCE-X02 now treats the pinned checkout's `go.mod` and `go.sum` as immutable dependency authority. Each source build uses fresh disposable `GOMODCACHE` and `GOCACHE` directories, sets `GOWORK=off` and `GOTOOLCHAIN=local`, removes ambient `-mod`/`-modfile` overrides, runs `go mod download all`, resolves the graph with `go list -mod=readonly -m all`, and builds with `go build -mod=readonly`.

The builder refuses missing `go.sum` and fails if either module metadata file changes during the build.

## Provenance

Build provenance now binds:

- `go_mod_sha256`
- `go_sum_sha256`
- `go_module_graph_sha256`
- `go_module_count`
- `go_module_mode=readonly`
- `go_workspace_mode=off`
- `go_module_cache_scope=isolated-ephemeral`

The production bundle verifier rejects bundles that omit or weaken these claims.

## Campaign effect

This is a G1-A remediation hotfix. It does not advance promises or seal RUNTIME-GRAND-G1. It refreshes SOURCE-X02/SOURCE-G1 build evidence while preserving the canonical RNX-P001..RNX-P514 universe.
