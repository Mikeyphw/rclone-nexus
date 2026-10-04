#!/usr/bin/env python3
from __future__ import annotations
import importlib.util, json, subprocess, sys
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
ADOPTED='IMPLEMENTED_AND_PRODUCTION_ADOPTED'

def fail(msg:str)->None:
    print(f'ERROR: {msg}',file=sys.stderr); raise SystemExit(1)
def require(cond:bool,msg:str)->None:
    if not cond: fail(msg)
def run(argv:list[str])->None:
    print('$ '+' '.join(argv)); subprocess.run(argv,cwd=ROOT,check=True)

def assert_scope()->None:
    d=json.loads(LEDGER.read_text()); by={x['id']:x for x in d['items']}
    p=by['RNX-P508']
    require(p.get('status')==ADOPTED,f'RNX-P508 status={p.get("status")}')
    refs=set(p.get('evidence') or [])
    require('migrate-x01-audit' in refs,'RNX-P508 is not sealed by migrate-x01-audit')
    require('internal/migration/migration_test.go::TestFinalizeStartsOnlyExplicitlySelectedMounts' in refs,'RNX-P508 direct finalization regression is not canonical evidence')
    active=d.get('active_position',{})
    require(not active.get('reopened_prior_promises'),'active scope still reports a reopened prior promise')

def assert_production_gate()->None:
    spec=importlib.util.spec_from_file_location('migrate_x01_gate',ROOT/'scripts/dev/migrate_x01_gate.py')
    require(spec is not None and spec.loader is not None,'cannot load MIGRATE-X01 gate')
    mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
    mod.assert_scope(); mod.assert_architecture()

def main()->int:
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    assert_scope(); assert_production_gate()
    run(['go','test','-count=1','./internal/migration','-run','TestMountSelectionIsExplicitAndPublicIDIsPrintable|TestFinalizeStartsOnlyExplicitlySelectedMounts'])
    # The owning MIGRATE gate compiles racctl and proves the same selected-vs-unselected
    # final authority switch through the real CLI/control/mount lifecycle.
    run([sys.executable,'scripts/dev/migrate_x01_gate.py'])
    print('MIGRATE-X01-HOTFIX-01 selected-mount finalization: PASS')
    print(json.dumps({'closed':['RNX-P508'],'canonical_range':'RNX-P001..RNX-P514','next_hotfix':'UPDATE-X01 qualification policy semantics'},sort_keys=True))
    return 0

if __name__=='__main__': raise SystemExit(main())
