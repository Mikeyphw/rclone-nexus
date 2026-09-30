# WEB-G1 — WebUI security/functionality qualification gate

WEB-G1 closes the WebUI implementation window (WEB-X01 through WEB-X03). It is
a gate/remediation overlay, not a new product-feature wave.

## Audit-loop remediations

The gate found and closed two authority gaps before qualification:

1. **Embedded transport fallback.** `selectTransport()` previously selected the
   manager bridge before proving its capability response. A broken KernelSU/
   APatch bridge could therefore strand the UI even when the authenticated
   standalone loopback authority was available. Selection now validates the
   embedded capability schema/protocol first, falls back only after a successful
   standalone capability probe, and clears transport state when neither is
   proven. Requests already sent over one transport are never automatically
   replayed over another transport, avoiding duplicate mutations.
2. **Runtime mutation preview authority.** Namespace apply/rollback and cache
   clear/forget were previewed by the WebUI, but the native run operations did
   not require proof that the preview happened. Those mutations now require the
   same root-private, one-use, short-lived preview-proof mechanism used by mount,
   job and settings mutation. Proofs are bound to the operation, mount name and
   current mount-registry revision/digest. CLI convenience commands perform the
   matching preview automatically; direct typed calls without proof fail closed.
3. **Fast-job validation flake.** The Python integration test treated a progress
   event as mandatory even though a very fast rclone process may exit before the
   asynchronous progress scanner publishes one. The gate now treats progress as
   optional telemetry and asserts durable terminal job state/run count instead;
   when progress is emitted its bytes are still validated.

WEB-G1 also records keyboard/accessibility semantics for modal Escape handling
and active navigation state, and qualifies the existing responsive phone/tablet
CSS contracts.

## Gate invariants

The gate must prove:

- standalone and embedded transports terminate at the same native typed
  operation registry;
- there is no arbitrary shell, argv, generic file reader, root RPC or generic
  rclone RC proxy;
- bootstrap replay, session, CSRF, Origin, Host and request/response bounds
  remain enforced;
- embedded bridge failure falls back only to a capability-proven standalone
  authority and never fabricates success;
- credentials and RC secrets do not enter browser source or typed responses;
- configuration, jobs, settings, namespace and destructive cache mutations are
  bound to authoritative validation/preview where required;
- operation truth is restored from the persistent backend journal after browser
  reopen;
- diagnostics/support export remains bounded and sanitized;
- all six top-level screens retain responsive phone/tablet layout plus native
  keyboard/accessibility semantics.

The permanent validation surfaces are `./devtoolw web-g1` for native/security
qualification and `./devtoolw webui` for the chroot-owned JavaScript contracts.
