# RUNTIME-STANDALONE — X01 canonical runtime ownership

Campaign position: **RUNTIME-X01 / Position 1 of 11**. Canonical merged scope: **RNX-P001..RNX-P500**. This overlay owns **RNX-P279..RNX-P307 (29 promises)**.

## Canonical authority direction

The production direction is now:

```text
Nexus state + explicit runtime mode
        |
        v
internal/runtimeauth.Resolve
        |
        +--> managed: /data/adb/rclone-nexus/runtime/active/bin/rclone
        |             /data/adb/rclone-nexus/config/rclone/rclone.conf
        |
        +--> external: compatibility lowering only
        |              explicit external env -> legacy provider -> PATH
        |
        +--> migration-required: observation/fail-closed state only
        |
        v
provider.FindRclone / provider.ConfigPath compatibility facade
        |
        +--> service/boot preflight
        +--> racctl + daemon/control API
        +--> mount lifecycle + supervisor/readiness
        +--> jobs/remotes
        +--> doctor/diagnostics
        +--> WebUI runtime.status
        +--> install verification
        +--> Devtool release qualification
```

The provider module and host `PATH` are no longer upstream authorities for managed execution. They are consulted only by the explicitly noncanonical `external` compatibility adapter or as migration evidence. `RCLONE_CONFIG` is likewise ignored in managed mode; the only supported managed configuration override is the explicitly named `RNEXUS_MANAGED_RCLONE_CONFIG` path setting used to construct the canonical `Paths` value.

## Runtime modes

`managed` makes Nexus authoritative for executable/config selection. An enabled legacy provider module alongside a managed runtime is treated as an ambiguous competing lifecycle/jobs authority and fails operational qualification. A legacy provider marked disabled/removed is observation-only and does not block managed operation.

`external` intentionally retains the historical provider compatibility ordering: explicit external binary, compatibility `RNEXUS_RCLONE_BIN`, provider module candidates, then PATH. This mode is reported as noncanonical.

`migration-required` is selected when no managed runtime is present but a legacy provider exists and no explicit external mode was selected. Nexus does not silently execute the legacy runtime from this state.

## Production adoption

The boot service calls `racctl runtime status --json --require-operational` before starting `racd` or reconciliation. Normal `racctl runtime status`, the control engine's `runtime.status` operation, mount/job runtime launches, readiness/supervision, doctor diagnostics, WebUI Runtime/Home views, and install verification all consume the same Go authority or its compatibility facade. Release workflows include the dedicated `runtime-standalone-x01-audit` node.

Shell entry points do **not** infer runtime mode. Provider environment is sourced only after an explicit external-compatibility signal, and shell runtime/config helper functions delegate to `racctl runtime executable|config`. The native resolver therefore remains upstream of shell execution rather than receiving a shell-selected mode as input.

## Duplicate authorities disposition

The old Go provider-directory/PATH selection was removed from `provider.FindRclone`; it now delegates to `runtimeauth`. PATH fallback exists only in the external adapter. The shell no longer infers a parallel runtime mode or binary/config path; compatibility helpers project the native resolver. Managed Go execution uses the Nexus-owned config path and reports `operational=false` when that config is absent. An enabled parent provider is conservatively considered a possible automount/autosync authority and causes managed mode to fail closed rather than risk double lifecycle or job execution.

The historical Devtool workflow named `runtime-x01` is **not** evidence for this campaign position: it belongs to the earlier jobs/RC roadmap. This campaign uses `runtime-standalone-x01` and `runtime-standalone-x01-audit` to avoid an evidence-name collision.

## Negative evidence

Executable tests prove managed operation with the provider absent; external mode without an executable fails closed; enabled legacy-provider + managed runtime is ambiguous; legacy-only state requires migration; poisoned PATH cannot redirect managed executable selection; and poisoned generic `RCLONE_CONFIG` or the legacy `Paths.RcloneConfig` field cannot redirect managed configuration. The normal `racctl runtime status --require-operational` CLI is exercised rather than proving only internal constructors.

## Scope status and next position

This overlay production-adopts RNX-P279..RNX-P307 only. The campaign remains open because later runtime-standalone promises RNX-P308..RNX-P500 are intentionally not claimed by X01, and pre-standalone promises that require fresh qualification remain open under the canonical ledger.

Next overlay: **RUNTIME-X02 — runtime store and deterministic qualification**. Gate-window position: **1 of 4** (`X01`, `X02`, `X03`, then `RUNTIME-G1`). Full roadmap position: **1 of 11**. After the runtime gate, the next major scope is the activation/migration work defined by the standalone roadmap.
