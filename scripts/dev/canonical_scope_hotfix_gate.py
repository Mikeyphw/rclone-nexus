#!/usr/bin/env python3
from __future__ import annotations
import importlib.util,json,re,subprocess,sys
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
DISPOSITIONS=ROOT/'release/canonical-roadmap-obligation-dispositions.json'
ADOPTED='IMPLEMENTED_AND_PRODUCTION_ADOPTED'


def fail(msg:str)->None:
    print(f'ERROR: {msg}',file=sys.stderr); raise SystemExit(1)
def require(cond:bool,msg:str)->None:
    if not cond: fail(msg)
def run(argv:list[str])->None:
    print('$ '+' '.join(argv)); subprocess.run(argv,cwd=ROOT,check=True)


def assert_scope_closure()->None:
    d=json.loads(LEDGER.read_text()); items=d['items']; by={x['id']:x for x in items}
    require(d.get('promise_count')==514 and d.get('max_promise_number')==514 and len(items)==514,'canonical maximum is not RNX-P514')
    require([x['id'] for x in items]==[f'RNX-P{i:03d}' for i in range(1,515)],'canonical IDs are not contiguous through RNX-P514')
    require(d.get('source_counts',{}).get('runtime-standalone')==236,'runtime-standalone source count is not 236')
    expected={f'RNX-P{i:03d}' for i in range(501,515)}
    actual={x['id'] for x in items if int(x.get('number',0))>=501}
    require(actual==expected,f'supplemental canonical IDs mismatch: {sorted(actual^expected)}')
    for pid in sorted(expected):
        require(by[pid].get('status')==ADOPTED,f'{pid} unexpectedly not adopted: {by[pid].get("status")}')
    a=d.get('active_position',{})
    require(a.get('position')==10 and a.get('production_adopted_count')==29,'UX active-position count was not expanded for RNX-P514')
    require(not a.get('reopened_prior_promises'),'canonical scope still reports a reopened prior promise after RNX-P508 closure')


def assert_disposition_manifest()->None:
    m=json.loads(DISPOSITIONS.read_text()); clauses=m.get('supplemental_clauses',[])
    new_ids=[pid for c in clauses if c.get('classification')=='new-promise' for pid in c.get('promise_ids',[])]
    require(new_ids==[f'RNX-P{i:03d}' for i in range(501,515)],'new-promise dispositions do not exactly compile RNX-P501..P514')
    numbered=[c for c in clauses if c.get('kind')=='numbered']
    require(numbered and all(c.get('classification') in {'mapped','new-promise'} for c in numbered),'numbered roadmap workflow step can escape canonical promise disposition')


def assert_no_fixed_500_gate_guards()->None:
    bad=[]
    rx=re.compile(r'(promise_count|max_promise_number)[^\n]{0,80}==\s*500|RNX-P001\.\.RNX-P500 scope changed|must cover RNX-P001\.\.RNX-P500')
    for path in sorted((ROOT/'scripts/dev').glob('*gate.py'))+[ROOT/'scripts/dev/check_canonical_scope.py']:
        text=path.read_text(encoding='utf-8')
        if rx.search(text): bad.append(path.relative_to(ROOT).as_posix())
    require(not bad,'gates still hardcode obsolete canonical maximum 500: '+', '.join(bad))


def assert_progression_gates_under_expanded_scope()->None:
    modules=[
        'runtime_standalone_x01_gate.py','runtime_standalone_x02_gate.py','runtime_standalone_x03_gate.py','runtime_standalone_g1_gate.py',
        'source_x01_gate.py','source_x02_gate.py','update_x01_gate.py','source_g1_gate.py','ux_x01_gate.py',
    ]
    for name in modules:
        spec=importlib.util.spec_from_file_location('scope_'+name.replace('.','_'),ROOT/'scripts/dev'/name)
        require(spec is not None and spec.loader is not None,f'cannot load {name}')
        mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
        mod.assert_scope()


def assert_prior_gate_is_resealed_behaviorally()->None:
    cp=subprocess.run([sys.executable,'scripts/dev/migrate_x01_gate.py'],cwd=ROOT,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    combined=cp.stdout+'\n'+cp.stderr
    require(cp.returncode==0,'MIGRATE-X01 did not reseal after RNX-P508 behavioral closure: '+combined[-4000:])
    require('MIGRATE-X01 NewFuture migration + standalone packaging: PASS' in combined,'MIGRATE-X01 reseal did not execute its behavioral gate')


def main()->int:
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    run([sys.executable,'-m','unittest','-v','tests.test_canonical_scope_compiler'])
    assert_scope_closure(); assert_disposition_manifest(); assert_no_fixed_500_gate_guards(); assert_progression_gates_under_expanded_scope()
    # Requalify the two newly numbered SOURCE-X01 invariants without recursively
    # executing every historical ancestor gate. The SOURCE-X01 assertion surface
    # and focused resolver tests are the owning evidence for P501/P502.
    spec=importlib.util.spec_from_file_location('source_x01_gate',ROOT/'scripts/dev/source_x01_gate.py')
    require(spec is not None and spec.loader is not None,'cannot load SOURCE-X01 gate')
    mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
    mod.assert_scope(); mod.assert_architecture()
    run(['go','test','-count=1','./internal/runtimesource','-run','TestLatestStableResolvesToImmutableCommitAndAssetIDAndPersists|TestPinnedReleaseRetargetDoesNotRewritePriorResolution|TestPinnedCommitRequiresFullImmutableSHA'])
    # Scope closure must invalidate an older gate when a newly discovered
    # obligation lacks complete behavioral proof. A green migration gate here
    # would be a regression in the governing campaign protocol.
    assert_prior_gate_is_resealed_behaviorally()
    print('CANONICAL-SCOPE-HOTFIX-01: PASS')
    print(json.dumps({'promise_range':'RNX-P001..RNX-P514','promise_count':514,'new_promises':14,'runtime_standalone_count':236,'reopened':[],'next_hotfix':'UPDATE-X01 qualification policy semantics'},sort_keys=True))
    return 0

if __name__=='__main__': raise SystemExit(main())
