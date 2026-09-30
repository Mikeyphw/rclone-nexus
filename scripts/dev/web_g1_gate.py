#!/usr/bin/env python3
from __future__ import annotations
from pathlib import Path
import re, sys

ROOT = Path(__file__).resolve().parents[2]
errors: list[str] = []

def text(rel: str) -> str:
    return (ROOT / rel).read_text(encoding='utf-8')
def require(value: bool, message: str) -> None:
    if not value: errors.append(message)

required = [
    'scripts/dev/web_x01_gate.py','scripts/dev/web_x02_gate.py','scripts/dev/web_x03_gate.py',
    'scripts/dev/check_webui_g1.mjs','docs/implementation/WEB-G1.md',
    'module/webroot/index.html','module/webroot/style.css','module/webroot/app.js','module/webroot/bridge.js',
    'internal/webui/server.go','internal/control/engine.go','internal/control/engine_test.go',
]
for rel in required: require((ROOT / rel).is_file(), f'missing WEB-G1 surface: {rel}')

bridge = text('module/webroot/bridge.js')
app = text('module/webroot/app.js')
html = text('module/webroot/index.html')
css = text('module/webroot/style.css')
server = text('internal/webui/server.go')
engine = text('internal/control/engine.go')
engine_tests = text('internal/control/engine_test.go')

# Both transports terminate at the same native typed operation registry.
require('engine.Descriptor' in text('internal/webui/runtime.go'), 'embedded bridge does not revalidate native operation registry')
require('s.cfg.Engine.Descriptor(name)' in server, 'standalone API does not revalidate native operation registry')
require('validateCapabilities(caps)' in bridge and "selected = 'embedded'" in bridge, 'embedded selection is not capability validated')
require("selected = 'standalone'" in bridge and 'embeddedError' in bridge, 'embedded failure lacks standalone fallback')
require('selected = null' in bridge, 'transport selection cannot fail closed')

# No generic root/browser authority.
combined = '\n'.join([bridge, app, server, engine])
for bad in ('shell.exec','rclone.exec','rc.call','system.exec','/api/v1/shell','/api/v1/exec','file.read','path.read','sh -c','bash -c'):
    require(bad not in combined, f'generic privileged WebUI surface found: {bad}')
for bad in ('innerHTML','outerHTML','insertAdjacentHTML','eval(','new Function','localStorage','sessionStorage','indexedDB'):
    require(bad not in app + bridge, f'unsafe/browser-local authority primitive found: {bad}')

# Standalone authorization/security contracts.
for token in ('SameSiteStrictMode','HttpOnly: true','X-Rclone-Nexus-CSRF','Origin','MaxHeaderBytes','MaxBytesReader','ConstantTimeCompare','127.0.0.1:0','net.Listen("tcp4"','frame-ancestors \'none\''):
    require(token in server, f'standalone security contract missing: {token}')
require('subtle.ConstantTimeCompare' in server, 'standalone secret comparisons are not constant-time')

# Mutation previews are backend-enforced, not only UI convention.
for resource in ('namespace-apply','namespace-rollback','cache-clear','cache-forget'):
    require(resource in engine, f'missing proof resource {resource}')
require(engine.count('consumeActionPreview(') >= 5, 'namespace/cache mutations are not proof-gated')
require('TestWebG1NamespaceAndCacheMutationsRequireFreshPreviewProof' in engine_tests, 'missing preview-proof regression for runtime mutations')
for token in ('expected_revision:pre.current_revision','candidate_digest:pre.candidate_digest','preview_proof:pre.preview_proof'):
    require(token in app, f'browser runtime mutation missing proof field: {token}')

# Credential and bounded diagnostics rules.
for secret in ('--rc-user','--rc-pass','refresh_token','client_secret'):
    require(secret not in app + html, f'credential-bearing token reached browser source: {secret}')
require("query('operation.list'" in app, 'browser reopen does not restore persistent operation truth')
require('doctor.bundle.read' in app and 'diagnostics.logs' in app, 'bounded diagnostics surfaces missing')
require('MaxResponseBytes' in server and 'MaxBytesReader' in server, 'standalone response/request bounds missing')

# Phone/tablet and keyboard/accessibility contract.
for token in ('name="viewport"','aria-label="Primary navigation"','aria-live="polite"','aria-modal="true"'):
    require(token in html, f'accessibility token missing: {token}')
for token in ('@media (max-width: 680px)', '.tabs { overflow-x: auto;', '.form-grid { grid-template-columns: 1fr;', '@media (prefers-reduced-motion: reduce)'):
    require(token in css, f'responsive/accessibility CSS missing: {token}')
require("event.key!=='Escape'" in app and "aria-current','page'" in app, 'keyboard/navigation accessibility semantics missing')

if errors:
    for error in errors: print(f'ERROR: {error}', file=sys.stderr)
    raise SystemExit(1)
print('WEB-G1 audit: OK')
