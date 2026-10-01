#!/system/bin/sh
set -eu
python3 -m unittest tests.test_install_stack -v
python3 - <<'PY'
import tomllib
from pathlib import Path
with Path('.devtool.toml').open('rb') as fh:
    data = tomllib.load(fh)
commands = data['wrapper']['commands']
workflows = data['targets']['rclone_nexus']['workflows']
for name in ('install', 'install-stack', 'install-verify', 'install-status'):
    if name not in commands or name not in workflows:
        raise SystemExit(f'missing Devtool install surface: {name}')
if 'install-bootstrap-gate' not in workflows:
    raise SystemExit('missing install-bootstrap-gate workflow')
print('GRAND-G1 install/bootstrap remediation source audit: PASS')
PY
