#!/usr/bin/env python3
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]

def read(path: str) -> str:
    return (ROOT / path).read_text(encoding='utf-8')

engine = read('internal/control/engine.go')
app = read('module/webroot/app.js')
html = read('module/webroot/index.html')
provider = read('internal/provider/remotes.go')
doctor = read('internal/doctor/doctor.go')
logs = read('internal/diagnostics/read.go')
settings = read('internal/websettings/settings.go')

required_ops = [
    'diagnostics.logs','doctor.bundle.read','provider.remotes','provider.browse',
    'ui.settings','ui.settings.preview','ui.settings.apply','jobs.preview','jobs.apply',
]
for op in required_ops:
    assert f'"{op}"' in engine, f'missing typed backend operation {op}'

for forbidden in ['system.exec','rclone.exec','rc.call','file.read','path.read']:
    assert f'engine.register("{forbidden}"' not in engine, f'forbidden generic operation {forbidden}'

assert 'previewproof.Issue(engine.Paths, "jobs"' in engine
assert 'previewproof.Consume(engine.Paths, args.PreviewProof, "jobs"' in engine
assert 'previewproof.Issue(engine.Paths, "ui-settings"' in engine
assert 'previewproof.Consume(engine.Paths, args.PreviewProof, "ui-settings"' in engine

assert 'listremotes' in provider and 'lsjson' in provider
assert '--config' in provider and 'remote is not configured' in provider
assert 'on-the-fly' not in app.lower()
assert re.search(r'512\s*<<\s*10', doctor), 'support bundle fixed read ceiling missing'
assert 'logBudget :=' in doctor, 'support bundle aggregate log budget missing'
assert 'SanitizeText' in logs and 'limit > 500' in logs
assert 'settings-v1.json' in settings and 'syscall.Flock' in settings

for view in ['home','mounts','jobs','runtime','logs','settings']:
    assert f'data-view="{view}"' in html
assert 'data-view="operations"' not in html, 'Operations should be contextual under Runtime in final navigation'

for forbidden in ['localStorage','sessionStorage','indexedDB','innerHTML','eval(','new Function']:
    assert forbidden not in app, f'forbidden browser authority primitive {forbidden}'
for secret in ['--rc-user','--rc-pass','refresh_token','client_secret']:
    assert secret not in app, f'secret-bearing browser content {secret}'

assert 'navigator.clipboard' in app and 'doctor.bundle.read' in app
assert 'provider.browse' in app and 'namespace.inspect' in app and 'rc.metrics' in app
assert 'document.visibilityState' in app

for setting in ['reduced_motion','log_max_bytes','log_backups','webui_idle_seconds','default_mount_view']:
    assert setting in settings, f'missing persisted WebUI setting {setting}'
assert 'ResolveIdle' in read('internal/webui/runtime.go'), 'persisted WebUI idle lifetime is not consumed by server runtime'
assert 'logLimits(p paths.Paths)' in read('internal/diagnostics/log.go'), 'persisted log retention is not consumed by diagnostics'
assert 'user.user_id' in app, 'per-Android-user runtime qualification missing'
assert 'useRemoteForMount' in app and "query('config.snapshot')" in app, 'remote-to-mount handoff must not depend on prior Mounts navigation'
assert 'reduce-motion' in read('module/webroot/style.css'), 'reduced-motion presentation contract missing'
print('WEB-X03 audit PASS')
