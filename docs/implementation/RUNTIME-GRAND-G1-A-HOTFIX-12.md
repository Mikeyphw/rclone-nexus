# RUNTIME-GRAND-G1-A HOTFIX-12 — downstream journey fixed-point sweep

## Scope

Audit every GRAND-G1 boundary after the failed-activation recovery path rather than waiting for one device failure per rerun. The sweep covers encrypted configuration, migration, configured-remote browse, release endurance invocations, staged-reboot preparation/resume, and final RNX-P467..RNX-P489 projection.

## Findings and remediation

1. **Encrypted-at-rest false negative.** Rclone's encrypted config format permits leading blank/comment lines before `RCLONE_ENCRYPT_V0:`. The harness incorrectly required the magic at byte zero. The parser now ignores blank/comment preamble lines and requires the first payload line to be the encryption marker.
2. **Weak plaintext-at-rest assertion.** Absence of the password canary did not prove the config body was encrypted because the password is never expected to be stored in the config. The gate now also proves the qualification remote stanza and `type = local` plaintext are absent.
3. **Migration `/data/adb` script execution fragility.** The synthetic provider binary and process runner depended on direct script execution below `/data/adb`. The provider binary is now the real qualified managed runtime, and synthetic long-lived provider processes are launched explicitly through `/system/bin/sh`.
4. **Configured-remote browse depended on mount ordering.** A stale/offline first mount could fail RNX-P479 even when another configured remote was healthy. The probe now tries each distinct configured remote until one returns typed production `provider.browse` entries; all candidates failing remains a hard failure.
5. **Staged reboot lost source-bound CLI authority.** `prepare-staged-reboot` and `resume-staged-reboot` previously rediscovered an installed/PATH `racctl` after capture restored the environment. Both now resolve and physically revalidate the exact gate `racctl` retained by the composite's RUNTIME-G1 evidence.
6. **Staged candidate selection trusted manifest state without current bytes.** Candidate selection now requires an executable store object whose physical SHA-256 still equals the qualified manifest hash.
7. **Temporary update source collision / duplicate prepare.** The fixed `grand-g1-reboot` source identity could overwrite/collide with pre-existing state. Preparation now creates a qualification-owned unique source identity, records it in resumable evidence, and refuses to prepare a second staged reboot while one is already awaiting reboot.
8. **Preparation cleanup was not transactional.** Failure after source/policy mutation now restores the exact prior update policy and removes the temporary source. Source removal is intentionally suppressed if policy restoration fails so production policy cannot be left pointing at a deleted source.
9. **Policy restoration invented fallback values.** Resume previously defaulted missing fields to values that did not even match production defaults. Exact restoration now requires every persisted policy field and reproduces it without fallback substitution.
10. **Staged reboot proof did not bind active bytes.** Resume now requires `current_binary_sha256` to equal the qualified staged candidate hash and verifies rollback restored the pre-reboot runtime before sealing PASS.
11. **Release endurance commands lost composite authority.** GRAND-G1 `run-release-case` and `resume-release-case` now inject the source-bound gate `racctl` for every release-device observation and refresh the composite hash even when a resumable case transition fails.
12. **WebUI endurance overrode explicit CLI authority.** The release-device WebUI case preferred `/data/adb/modules/.../racctl` even when `RNEXUS_RACCTL` was explicitly supplied. It now honors candidate precedence.
13. **Evidence projection was weaker than the observations already made.** RNX-P469/P471/P472/P474/P489 now require the recorded live process hashes to equal the qualified runtime hashes. RNX-P477 now requires an explicit `encrypted_at_rest` proof bit.
14. **Two GRAND-G1 regression tests were unreachable.** Test methods accidentally lived below `unittest.main()` inside the module guard. They are now real class tests.
15. **Temporary source cleanup could silently fail.** Cleanup used `check=False` but never inspected the return code, so the gate could claim restoration while leaving the qualification source registered. Nonzero removal is now surfaced as a cleanup failure.
16. **Daemon-crash recovery directly executed `/data/adb/.../service.sh`.** The release endurance path now invokes the installed module service entrypoint explicitly through `/system/bin/sh`, matching the hardened Android execution model used by the other gates.
17. **WebUI endurance could hang and could assert, rather than observe, reopen success.** A blocking `readline()` defeated the startup deadline; reopen marked `reachable=true` without making a request; cleanup could leave the detached reopened server alive. Startup reads are now deadline-bounded with `select`, both bootstrap endpoints are actually probed, the reopened PID must differ, and qualification-owned WebUI processes are cleaned up.

## Evidence identity

- Composite GRAND-G1 harness version: 7.
- Release-device harness version: 5.
- Existing evidence from earlier harness versions must be recaptured/revalidated; expensive independent RUNTIME-G1/SOURCE-G1 evidence remains reusable only through their own current validators.

## Validation

- `python3 -m unittest tests.test_runtime_grand_g1_device tests.test_release_qualification tests.test_runtime_grand_g1_watcher_launch tests.test_runtime_standalone_g1_device tests.test_source_g1_supply_chain tests.test_source_x02_termux_ndk`
- `python3 scripts/dev/runtime_grand_g1_device.py source-audit`
- `go test ./internal/migration ./internal/runtimeupdate ./internal/runtimeactivation ./internal/runtimesource ./internal/control`

The real rooted-device run remains authoritative for Android/FUSE/runtime-transition behavior. Host-side validation proves the harness contracts and affected production packages, not the device outcome itself.
