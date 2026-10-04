#!/usr/bin/env python3
from pathlib import Path
import json, subprocess, sys

ROOT = Path(__file__).resolve().parents[2]

def run(args):
    print('$ ' + ' '.join(args), flush=True)
    subprocess.run(args, cwd=ROOT, check=True)

def require(value, message):
    if not value:
        raise RuntimeError(message)

def main():
    run([sys.executable, 'scripts/dev/check_canonical_scope.py'])
    run(['go','test','-count=1','./internal/runtimesource','./internal/runtimemanager','./internal/control','./internal/runtimeupdate','./cmd/racctl'])
    run(['go','test','-count=1','./internal/control','-run','^TestRuntimeManagerSourcePolicyEndToEnd$'])
    run(['go','test','-count=1','./internal/runtimesource','-run','^TestSourceChannelCapabilitiesRejectImpossibleCombinations$|^TestResolveRejectsUnsupportedChannelBeforeNetwork$'])
    run(['node','scripts/dev/check_runtime_manager_ux.mjs'])

    ledger = json.loads((ROOT/'release/canonical-promise-ledger.json').read_text())
    by_id = {item['id']: item for item in ledger['items']}
    evidence = 'scripts/dev/ux_source_policy_hotfix_gate.py'
    for promise in ('RNX-P452','RNX-P455'):
        require(evidence in (by_id[promise].get('evidence') or []), f'{promise} missing UX/source-policy remediation evidence')

    # The owning canonical gates carry the same behavioral proof forward.
    for gate in ('scripts/dev/source_x01_gate.py','scripts/dev/update_x01_gate.py','scripts/dev/ux_x01_gate.py'):
        text = (ROOT/gate).read_text()
        require('TestRuntimeManagerSourcePolicyEndToEnd' in text, f'{gate} does not retain cross-surface behavioral proof')

    print('UX/SOURCE-POLICY-HOTFIX-04: PASS — source-aware channel choices and restart-active-mount update policy are behaviorally wired through canonical authority')
    return 0

if __name__ == '__main__':
    raise SystemExit(main())
