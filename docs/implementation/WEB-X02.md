# WEB-X02 — Home, Mounts, Operations and transactional mount editor

WEB-X02 is full-plan position 12/16 and the second implementation position in
WebUI gate window 2/3. It keeps WEB-X01's transport/security boundary and adds
real operational UI only through existing typed backend operations.

## Delivered

- Home summary sourced from provider, mount, platform and persistent operation
  journal queries.
- Mount cards combine the public config registry, lifecycle state and supervisor
  health; lifecycle controls use typed start/stop/restart/reconcile operations.
- Operations view rehydrates from the persistent journal after browser reopen,
  renders bounded progress/terminal detail and exposes cancellation only when
  the backend marks an operation cancellable.
- Polling is view-scoped and suspended while the document is hidden.
- Mount create/edit/disable/delete uses a full typed candidate registry; the
  browser never parses or edits root configuration files.
- Existing `args_file` presence may be shown, but its path/content never crosses
  the typed mutation surface and is preserved server-side by mount name.
- Editor covers VFS profile/cache fields and network/battery/storage policy.
- Backend preview returns per-change reasons/consequences and affected restart
  set. Applying a candidate requires revision + digest + a fresh one-use proof.
- Preview proofs are root-private, short-lived, candidate/revision-bound and
  consumed on first apply attempt. Rollback uses the same proof contract.
- Previous-known-good rollback is previewed before apply.
- Standalone requests may carry a bounded caller request ID so the WebUI can
  correlate in-flight lifecycle/config work with the persistent operation
  journal. Embedded mode already carries the same request ID in its protocol
  envelope.

## Security / truth boundary

The WebUI stores no runtime/configuration truth in localStorage, sessionStorage
or IndexedDB. Browser state is disposable presentation state. A reopen always
queries Nexus again. There is still no arbitrary shell/argv/root RPC, generic
rclone execution or generic RC proxy.

## Targeted validation

- Go tests for one-use preview proof binding/replay and client request-ID routing.
- Existing configuration/lifecycle/PID/ownership suites.
- Python integration including legacy config migration with proof-bound apply.
- Node model tests for custom-profile preservation, private args metadata
  stripping, delete candidates, stale-preview rejection and operation journal
  modelling.
- Static WebUI audit for typed operation usage, visibility-scoped polling,
  forbidden browser persistence/dynamic HTML primitives, and preview-proof
  authority.
