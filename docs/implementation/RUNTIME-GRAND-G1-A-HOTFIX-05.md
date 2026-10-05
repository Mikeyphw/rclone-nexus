# RUNTIME-GRAND-G1-A HOTFIX-05 — GitHub rate-limit resilient source bootstrap

## Finding

A clean standalone bootstrap can exhaust GitHub's unauthenticated REST budget during repeated SOURCE/G1 qualification. The resolver previously collapsed every GitHub non-2xx response into an opaque HTTP status, treated HTTP 403 as non-retryable, and had no way to retry an already-persisted immutable resolution without spending more metadata requests.

## Remediation

- Shared `internal/githubapi` authority applies API version, optional authentication, and fail-closed rate-limit diagnostics.
- Token precedence is `RNEXUS_GITHUB_TOKEN`, `GITHUB_TOKEN`, `GH_TOKEN`, then the Nexus-owned `config/github.token` file. The persisted token file must not be group/world accessible.
- Runtime source metadata, runtime release-asset downloads, and canonical NewFuture `fusermount3` acquisition use the same transport authority.
- HTTP 403/429 rate limits expose remaining/reset/authentication state without exposing credentials and classify as retryable.
- `racctl runtime update retry` reuses the last persisted immutable resolution and already-acquired candidate when possible, performing no fresh source metadata lookup. It never silently substitutes stale data for a normal `runtime update check`; reuse is explicit.

## G1 impact

This is a G1-A remediation only. RNX-P467..P489 remain partially adopted pending fresh device evidence; RNX-P490..P500 remain reserved for G1-B final seal.
