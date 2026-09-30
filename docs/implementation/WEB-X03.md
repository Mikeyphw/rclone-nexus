# WEB-X03 — Runtime, Jobs, Logs, Doctor, Remotes and Settings convergence

WEB-X03 is full-plan position 13/16 and the third/final implementation position
in the WebUI gate window. It completes the product surface over the typed backend
without creating a browser-owned privileged runtime.

## Delivered

- Final top-level navigation: Home, Mounts, Jobs, Runtime, Logs and Settings.
- Operations remain persistent backend journal truth and are contextual under
  Runtime rather than becoming a second source of runtime state.
- Runtime combines readiness/self-heal state, policy decisions, namespace
  visibility evidence, VFS/cache usage and allow-listed local RC metrics.
- Namespace apply/rollback and cache clear/forget remain previewed typed actions.
- Jobs list/editor/manual run uses the existing typed job registry and operation
  journal. Job registry apply now requires a short-lived one-use preview proof
  bound to revision and digest, matching mount mutation safety.
- Structured logs are bounded, sanitized, severity-labelled and follow by
  visibility-aware polling rather than an unbounded browser stream.
- Doctor PASS/WARN/FAIL guidance renders through typed diagnostics.
- Support bundles can be built and saved only by content-derived bundle id; no
  generic root file reader/download operation exists. Bundle collection and
  protocol reads have fixed byte budgets.
- Provider remote discovery returns configured names only. Credential-free
  browsing validates the selected configured remote and bounded subpath; no
  rclone.conf material enters the browser.
- WebUI presentation settings are revisioned root-owned state. Apply requires a
  fresh one-use preview proof and uses a cross-process registry lock.
- Root-manager capabilities, package integrity and transport status are rendered
  read-only from the platform backend.

## Security boundary

There is still no generic shell, argv, root RPC, arbitrary file reader or generic
rclone RC proxy. RC credentials remain only in root runtime state. Browser state
is disposable presentation state; persisted settings are backend-owned and
runtime/configuration truth is always reloaded from Nexus.

## Targeted validation

- full Go and Python integration suites;
- jobs/settings preview-proof replay and stale-revision safety;
- remote names/path provenance and on-the-fly backend rejection;
- support bundle size/identity/path constraints plus secret canaries;
- bounded sanitized logs and severity model;
- final navigation/static/accessibility contract;
- visibility-aware follow/polling and no browser persistence/dynamic HTML;
- existing WEB-X01/WEB-X02 security regressions;
- reproducible Android backend and module package contract.
