#!/usr/bin/env python3
from __future__ import annotations
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
errors: list[str] = []
def require(cond: bool, message: str) -> None:
    if not cond: errors.append(message)
def text(rel: str) -> str: return (ROOT / rel).read_text(encoding='utf-8')

required = [
    'module/webroot/index.html','module/webroot/app.js','module/webroot/bridge.js','module/webroot/model.js','module/webroot/style.css',
    'internal/previewproof/previewproof.go','internal/previewproof/previewproof_test.go','internal/webui/server.go','internal/webui/server_test.go',
    'scripts/dev/check_webui_x02.mjs','scripts/dev/web_x02_gate.py','docs/implementation/WEB-X02.md',
]
for rel in required: require((ROOT / rel).is_file(), f'missing WEB-X02 surface: {rel}')
html, app, bridge = text('module/webroot/index.html'), text('module/webroot/app.js'), text('module/webroot/bridge.js')
engine, registry = text('internal/control/engine.go'), text('internal/mounts/registry.go')
registry_tests = text('internal/mounts/registry_test.go')

for surface in (app, bridge):
    for bad in ('localStorage','sessionStorage','indexedDB','innerHTML','outerHTML','insertAdjacentHTML','eval(','new Function'):
        require(bad not in surface, f'forbidden browser primitive: {bad}')
for ident in ('view-home','view-mounts','mountForm','previewPanel','operationCards','rollbackBackdrop'):
    require(f'id="{ident}"' in html, f'missing WebUI surface: {ident}')
require('id="view-operations"' in html or 'id="view-runtime"' in html, 'operations journal must remain a reachable WebUI surface')
for field in ('name','remote','mountpoint','vfs_profile','vfs_cache_mode','network_mode','min_battery','min_free_cache_space','cache_high_water','cache_low_water'):
    require(f'id="field-{field}"' in html, f'missing mount editor field: {field}')
for op in ('config.snapshot','config.preview','config.apply','config.rollback.preview','config.rollback','mount.start','mount.stop','mount.restart','mount.reconcile','operation.list','operation.cancel'):
    require(op in app, f'WebUI does not use typed operation {op}')
require('preview_proof' in app and 'preview_proof' in engine and 'PreviewProof' in registry, 'fresh preview proof is not end-to-end')
require('previewproof.Consume' in engine and 'previewproof.Issue' in engine, 'backend preview proof is not authoritative')
require('X-Rclone-Nexus-Request-ID' in bridge and 'X-Rclone-Nexus-Request-ID' in text('internal/webui/server.go'), 'journal request ID correlation missing')
require("document.addEventListener('visibilitychange'" in app and 'document.visibilityState' in app, 'visibility-scoped polling missing')
require('has_args_file' not in text('module/webroot/model.js').split('delete out.has_args_file')[1], 'private args metadata mutation contract malformed')
require('consequences' in registry and 'namespace_requalify' in registry and 'cache_policy_recheck' in registry, 'backend preview consequences missing')
require('TestApplyRestartsOnlyAffectedRunningMount' in registry_tests, 'affected-only mount reconciliation regression missing')
require('mountpoints overlap' in registry_tests and 'duplicate config key' in registry_tests, 'invalid/duplicate mount configuration regressions missing')
for bad in ('shell.exec','rclone.exec','rc.call','sh -c','bash -c'):
    require(bad not in app + bridge, f'generic privileged WebUI surface found: {bad}')

if errors:
    for error in errors: print(f'ERROR: {error}', file=sys.stderr)
    raise SystemExit(1)
print('WEB-X02 audit: OK')
