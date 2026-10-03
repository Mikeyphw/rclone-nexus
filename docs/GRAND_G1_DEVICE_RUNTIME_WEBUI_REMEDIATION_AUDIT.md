# GRAND-G1 device runtime + WebUI remediation audit

This remediation is part of GRAND-G1 (roadmap position 16/16). It does not create a new roadmap position. It closes real-device defects found after the guided mount-creation WebUI remediation.

## Evidence that triggered this remediation

A real NewFuture provider (`rclone` module, rclone v1.75.1) rejected Nexus' generated mount command because Nexus appended the unsupported option `--rc-no-open-browser`. The resulting rclone help burst was then misclassified by several layers as remote/network failure, while runtime namespace actions remained available even though no Nexus-owned source mount existed. Android screenshots also showed status-bar/header overlap, horizontally clipped navigation, and an unusably verbose raw-log presentation.

## Promise ledger

1. Remove the obsolete `--rc-no-open-browser` argument from generated RC argv.
2. Qualify every generated long mount option against the *selected provider binary's* `rclone mount --help` contract before starting the process.
3. Prefer the NewFuture module binary and fusermount3 helper over unrelated PATH executables, while retaining explicit override and PATH fallback.
4. Make shell and Go provider discovery agree on `system/vendor/bin` and provider-owned environment/config discovery.
5. Capture startup process exit during the convergence grace period rather than discarding `cmd.Wait()` truth.
6. Represent startup failures with code, category, stage, detail, retryability, and exit code.
7. Preserve structured lifecycle failures through the control protocol instead of collapsing them to `operation_failed`.
8. Classify unsupported CLI/configuration failures as terminal/non-retryable and prevent automatic restart-budget consumption.
9. Preserve transient remote/network startup failures as retryable.
10. Prevent readiness (`REMOTE_OFFLINE`) from overwriting a known terminal startup root cause.
11. Let `offline-allowed` mounts cold-start against cache when the device itself is offline even when remote probing is configured; online auth/probe failures remain authoritative.
12. Detect Android connectivity through IPv4 default route, IPv6 default route, and active interface/VPN fallback.
13. Parse rclone per-line timestamps and real severity prefixes instead of substring matching words such as "error" in help text.
14. Collapse rclone CLI help explosions into one bounded diagnostic with expandable suppressed lines.
15. Gate namespace/app-visibility actions until a live Nexus-owned source mount exists; render non-applicable namespace states neutrally.
16. Make Start/Stop/Restart controls reflect actual runtime state and terminal/retryable failure state.
17. Surface actionable lifecycle failure recovery in Mounts with edit/logs/operations and retry only for retryable failures.
18. Add log severity/source/search filtering and compact root-cause-first rendering.
19. Hide the large compatibility-success banner during healthy operation and reserve it for degraded/incompatible states.
20. Respect Android safe-area insets and make mobile primary navigation scroll/snap/auto-center rather than clip under system UI.
21. Add executable regression coverage for provider CLI compatibility, restart-budget ownership, offline-allowed semantics, IPv6/VPN network discovery, log parsing/collapse, provider precedence, and structured control errors.

## v2 promise-closure audit

A post-v1 production-path audit found six partial deliveries that source-level checks had not fully proven. v2 closes them without changing the 21-promise scope:

- **Promise 2:** provider CLI qualification now runs against the exact binary already selected for launch instead of resolving the provider a second time.
- **Promise 4:** `common.sh` now mirrors NewFuture's own `set -a; . env; set +a` contract so provider `RCLONE_CONFIG`, encrypted-config variables, proxy settings, and `conf/env` overrides are exported into racctl/rclone child processes. Embedded-manager `action.sh`, `rclone-doctor`, and install verification also use that env-loading wrapper boundary.
- **Promise 8:** invalid mount config, args files, missing provider/config, VFS resolution, provider CLI probe failures, and CLI incompatibility are explicit non-retryable lifecycle failures. Provider/config readiness failures are also represented as non-budgeted terminal health and clear automatically after the provider is repaired.
- **Promise 9:** transient network startup failures remain explicitly retryable and consume the bounded restart budget.
- **Promises 14/18:** CLI-help bursts are attached to the root error record when available, bounded to a sample, and the WebUI states when only a sample of suppressed lines is retained.
- **Promises 16/17:** persistent mount cards disable Start/Restart for terminal failures, make Edit the primary recovery, and expose Logs/Operations. Retry is offered only when backend `retryable=true`.
- **Security invariant:** structured startup details are sanitized before crossing the lifecycle/control/WebUI boundary; a regression test carries an authorization canary through a failing fake rclone and proves it is redacted.

The v2 checker additionally executes the provider shell-env boundary with a synthetic NewFuture-style `env` + `conf/env`, proving that inherited values actually reach child environment rather than merely appearing as shell locals.

## Architectural invariants

- The root cause recorded by the process boundary outranks downstream readiness guesses.
- Automatic retry is allowed only for explicitly retryable lifecycle failures.
- Provider CLI compatibility is proven against the selected provider executable, not inferred from Nexus source or an arbitrary host rclone.
- The WebUI does not invent runtime truth: source ownership, health, retryability, and namespace applicability remain backend-owned.
- Raw help/usage output is diagnostic detail, not a stream of independent errors.
- No provider credentials or RC secrets are exposed in structured errors, diagnostics, or the WebUI.

## Validation boundary

The remediation validator runs targeted Go packages spanning provider → argv → lifecycle → readiness → supervisor → diagnostics → control, the WebUI mount contract, a static promise checker, shell syntax, JS syntax when Node is available, and the module package contract. The complete source tree is also expected to pass `go test ./...` before packaging.

## v3 validation-environment and global-flag closure

The v2 artifact exposed two additional qualification defects on the real Termux/Android validator:

1. The provider-help fixture used `printf` after deliberately clearing `PATH`. Android's system shell does not guarantee `printf` is a builtin, so the fixture could return success with no advertised flags and falsely report every generated flag as missing. The fixture is now PATH-independent and uses shell builtins only.
2. More importantly, production qualification treated `rclone mount --help` as the complete flag authority. Rclone separates command-local flags from global flags, so valid generated options such as `--config` and `--rc-*` may not appear in mount help. Qualification now merges mandatory `mount --help` with `help flags` and root `--help` when available, against the exact provider executable selected for launch. Only a flag absent from the combined provider contract is rejected.

These fixes preserve fail-closed CLI qualification while avoiding false incompatibility failures for valid global rclone options.
