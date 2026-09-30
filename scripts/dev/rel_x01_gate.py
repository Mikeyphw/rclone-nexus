#!/usr/bin/env python3
from __future__ import annotations
from pathlib import Path
import re
ROOT = Path(__file__).resolve().parents[2]
roadmap=(ROOT/'docs/ROADMAP.md').read_text()
if not any(status in roadmap for status in ['REL-X01 implemented (15/16)', 'GRAND-G1 implemented (16/16)', 'GRAND-G1 qualified (16/16)']): raise SystemExit('roadmap status has not reached REL-X01 or later')
prop=(ROOT/'module/module.prop').read_text()
if 'version=v0.1.0\n' not in prop or '-dev' in prop: raise SystemExit('release module version not final')
for rel in ['scripts/dev/release_artifacts.py','scripts/dev/check_release.py','scripts/dev/release_failure_injection.py','scripts/dev/release_device_qualification.py','release/evidence/schema-v3.json','release/evidence/README.md']:
    if not (ROOT/rel).is_file(): raise SystemExit(f'missing release surface: {rel}')
web=(ROOT/'module/webroot').read_text() if (ROOT/'module/webroot').is_file() else ''
# The final gate owns completion, but REL-X01 must provide executable capture/run/resume/validate qualification rather than assertion-only recording.
evidence=(ROOT/'scripts/dev/release_device_qualification.py').read_text()
for token in ['reboot','provider_update_reload','wifi_mobile_offline','daemon_crash_restart','simultaneous_mounts_jobs','--require-complete','machine-observation-chain','awaiting-action','resume_case','verify_case_proof']:
    if token not in evidence: raise SystemExit(f'device qualification harness missing {token}')
if 'sub.add_parser("record"' in evidence or "sub.add_parser('record'" in evidence:
    raise SystemExit('legacy assertion-only record surface remains reachable')
artifacts=(ROOT/'scripts/dev/release_artifacts.py').read_text()
if 'release/evidence/device-qualification.json' not in artifacts:
    raise SystemExit('generated device evidence is not excluded from release source digest')
if 'full-plan 16/16' not in roadmap or 'GRAND-G1 implemented' not in roadmap:
    raise SystemExit('roadmap does not point truthfully to implemented GRAND-G1')
# No release tool may gain arbitrary shell execution.
for rel in ['scripts/dev/release_artifacts.py','scripts/dev/check_release.py','scripts/dev/release_failure_injection.py','scripts/dev/release_device_qualification.py','release/evidence/schema-v3.json','release/evidence/README.md']:
    text=(ROOT/rel).read_text()
    if 'shell=True' in text or 'os.system(' in text: raise SystemExit(f'unsafe shell release helper: {rel}')
print('REL-X01 audit: OK')
