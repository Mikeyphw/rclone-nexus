# RUNTIME-GRAND-G1-A HOTFIX-11 — Android encrypted-config password-command fixture

## Finding

The automatic GRAND-G1 encrypted-config journey generated `config-pass.sh` with literal `\n` characters instead of physical line breaks. The resulting Android password-command fixture was malformed, so an otherwise valid rclone/bclone runtime could encrypt the test config but fail the subsequent production `config encryption check` / `listremotes` decrypt-use proof.

The fixture also relied on direct execution of a script stored under `/data/adb`, unnecessarily coupling credential retrieval to Android mount/SELinux executable policy.

## Remediation

- Generate a syntactically valid shell fixture with real line boundaries.
- Keep the password helper root-readable (`0600`) rather than marking credential material executable.
- Invoke it explicitly as `/system/bin/sh <fixture>` through `RCLONE_PASSWORD_COMMAND`.
- Preserve the stronger password-command proof instead of downgrading to plaintext `RCLONE_CONFIG_PASS`.
- Report non-secret return-code/remote-visibility diagnostics when decrypt-use fails.
- Bump the composite GRAND-G1 harness version so evidence produced by the malformed fixture cannot be accepted as current.
- Add a regression contract proving physical newlines and explicit Android-shell invocation.

## Campaign effect

This is a G1-A remediation hotfix. It does not advance promises or weaken RNX-P477/RNX-P488. The device gate must still prove encrypted-at-rest config, production-runtime decrypt/use, and credential redaction.
