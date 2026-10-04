# UX/SOURCE-POLICY-HOTFIX-04

This remediation closes the two remaining Runtime Manager source/update UX gaps and strengthens the normal SOURCE-X01 / UPDATE-X01 / UX-X01 gates with behavioral cross-surface evidence.

## Delivered

- exposes `restart_active_mounts_automatically` in Runtime Manager and carries it through the canonical `runtime.update.policy.apply` operation;
- derives allowed source channels from one `internal/runtimesource` authority and projects them through `runtime.manager.source_choices`;
- prevents NewFuture pinned-commit/manual-only and GitHub pinned-commit without SOURCE-X02 build authority from being advertised or resolved;
- makes pinned ref requirements backend-derived and fail-closed in the browser;
- proves UI model policy assembly preserves non-edited acquire/qualify fields;
- proves typed control policy apply -> Runtime Manager round trip and impossible source/channel rejection;
- embeds the cross-surface behavioral proof in SOURCE-X01, UPDATE-X01 and UX-X01 authoritative gates.

Canonical scope remains `RNX-P001..RNX-P514`; this is a remediation overlay and does not consume position 11.
