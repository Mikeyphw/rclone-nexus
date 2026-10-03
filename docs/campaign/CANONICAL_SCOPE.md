# Canonical merged campaign scope

Campaign: `RUNTIME-STANDALONE`
Canonical promise range: **RNX-P001..RNX-P500**.

The machine-readable authority is `release/canonical-promise-ledger.json`. The former 24-row GRAND-G1 table is a historical summary only and cannot be used as the current campaign promise count.

## Source families

| Family | Count | Range |
|---|---:|---|
| Original detailed roadmap | 229 | RNX-P001..RNX-P229 |
| Install-stack amendments | 7 | RNX-P230..RNX-P236 |
| Guided mount remediation | 21 | RNX-P237..RNX-P257 |
| Device runtime/WebUI remediation | 21 | RNX-P258..RNX-P278 |
| Runtime-standalone roadmap | 222 | RNX-P279..RNX-P500 |

## Install-stack amendments

1. Devtool-native install, install-stack, install-status and install-verify remain supported while standalone migration is incomplete.
2. Root-manager staging semantics distinguish staged success from post-reboot installed success.
3. Post-reboot verification proves the installed module rather than only installer output.
4. Installed integrity distinguishes runtime-installed files from installer-only archive files.
5. Provider layout verification resolves the intended provider-owned binary rather than an unrelated PATH executable.
6. Install verification does not allow accidental PATH shadowing to stand in for the selected provider/runtime.
7. Devtool transaction handling preserves user dirty tracked state; this is exported to Devtool's transaction authority and is not claimed as locally implemented by this repository.

## Seal disposition

The legacy GRAND-G1 seal definition is invalid for current HEAD once this merged scope exists. It may be audited as historical evidence, but it must not produce a current `sealed` verdict while mandatory RUNTIME-STANDALONE promises remain open. The current final seal must be authored last after standalone remediation and production adoption.
