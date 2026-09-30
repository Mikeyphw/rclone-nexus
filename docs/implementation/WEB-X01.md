# WEB-X01 — secure WebUI foundation and transports

Full-plan position **11/16**. This implementation merges the former WEB-X01,
WEB-X02 and WEB-X03 transport/security work while deliberately leaving mount
editing and operational workspaces to WEB-X02/WEB-X03.

## Delivered boundary

- Static `module/webroot/` contains no CDN/network dependencies and renders all
  backend-derived text through `textContent`.
- Standalone mode binds only `tcp4 127.0.0.1:0` and serves the same module
  `webroot/` assets through an authenticated local HTTP authority.
- A 256-bit one-use bootstrap token is exchanged for a short-lived HttpOnly,
  SameSite=Strict session plus a double-submit CSRF token.
- Host validation is exact. Non-GET API requests require the exact standalone
  Origin and matching CSRF header/cookie.
- CSP, frame, MIME, referrer, permissions and no-store headers are applied to
  every standalone response. Header/body/time limits are bounded and idle
  shutdown cleans the owning runtime state.
- A root-private `0600` runtime record permits `racctl webui start` to reuse a
  live owned server while issuing a fresh one-use bootstrap token. The browser
  never receives that administrative reuse secret.
- `/api/v1` exposes only the native operation registry. URL class and operation
  descriptor class must match before the request reaches the shared Engine.
  There is no shell, arbitrary argv/file API, generic rclone execution or
  generic RC proxy.
- Embedded manager mode is selected only when a real injected `ksu.exec`
  capability exists. It invokes one fixed Nexus binary path and passes only a
  bounded base64url protocol Request to `racctl webui bridge`; the native
  backend parses and revalidates schema, operation and class.
- Standalone selection is a typed `/api/v1/transport` capability probe, never a
  `localhost` hostname assumption. Standalone remains the cross-manager path.
- `module/action.sh` starts/reuses the standalone server and opens its one-use
  Android VIEW URL through the native backend.
- The initial Home page is read-only and compatibility-gated. It requires the
  backend schema/protocol and the `provider.status`, `mount.status` and
  `platform.status` typed operations before rendering runtime data.

## Security invariants

Credentials and provider configuration remain backend-only. No WebUI response
contains the standalone administrative secret, RC credentials, `rclone.conf`,
or a caller-controlled command line. Browser payloads cannot select an
executable, shell command, file path, or rclone argv.

## Devtool ownership

`rclone_webui` is a dedicated chroot-executed target. Node syntax and typed
client contract tests run there so Termux host Node is not a hidden dependency.
The native `web-x01` workflow owns Go transport tests and static security audit.
