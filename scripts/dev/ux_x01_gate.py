#!/usr/bin/env python3
from __future__ import annotations
import importlib.util, json, os, subprocess, sys, tempfile, tomllib
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
POLICY=ROOT/'release/final-seal-policy.json'
ADOPTED='IMPLEMENTED_AND_PRODUCTION_ADOPTED'
POS10=set(range(127,137))|set(range(449,467))

def fail(m:str)->None: print(f'ERROR: {m}',file=sys.stderr); raise SystemExit(1)
def require(c:bool,m:str)->None:
    if not c: fail(m)
def read(r:str)->str: return (ROOT/r).read_text(encoding='utf-8')
def run(a:list[str],env=None)->None: print('$ '+' '.join(a)); subprocess.run(a,cwd=ROOT,check=True,env=env)
def evidence_resolves(ref:str)->bool:
    ref=ref.strip()
    if '::' in ref:
        p,n=ref.split('::',1); q=ROOT/p; return q.is_file() and n in q.read_text(encoding='utf-8',errors='replace')
    if '/' in ref: return (ROOT/ref).is_file()
    try:
        cfg=tomllib.loads(read('.devtool.toml')); t=cfg['targets']['rclone_nexus']; jobs=set(t.get('jobs',{})); nodes={str(n.get('id')) for f in t.get('workflows',{}).values() if isinstance(f,list) for n in f if isinstance(n,dict) and n.get('id')}
        return ref in jobs or ref in nodes
    except Exception: return False

def assert_scope()->None:
    d=json.loads(LEDGER.read_text()); by={int(x['number']):x for x in d['items']}
    require(d.get('max_promise_number')==500 and d.get('promise_count')==500,'canonical scope changed')
    actual={int(x['number']) for x in d['items'] if x.get('position')==10}
    require(actual==POS10,f'position-10 universe mismatch: {sorted(actual^POS10)}')
    for n in sorted(POS10):
        x=by[n]; require(x.get('status')==ADOPTED,f'RNX-P{n:03d} status={x.get("status")}')
        refs=x.get('evidence') or []; require('ux-x01-audit' in refs,f'RNX-P{n:03d} not sealed by ux-x01-audit')
        for r in refs: require(evidence_resolves(str(r)),f'RNX-P{n:03d} evidence missing: {r}')
    a=d.get('active_position',{}); require(a.get('position')==10 and a.get('name')=='UX-X01','active position not UX-X01')
    require(a.get('production_adopted_count')==28 and a.get('blocked_by_environment_count')==0,'position-10 counts stale')

def assert_architecture()->None:
    mgr=read('internal/runtimemanager/manager.go'); eng=read('internal/control/engine.go'); cli=read('cmd/racctl/main.go'); app=read('module/webroot/app.js'); model=read('module/webroot/model.js'); css=read('module/webroot/style.css'); dev=read('.devtool.toml'); pol=json.loads(POLICY.read_text())
    for token in ('runtimeauth.Resolve','runtimestore.List','runtimeactivation.StatusOf','runtimeupdate.SnapshotOf','runtimesource.List','migration.StateSnapshot','migration.Detect','previous runtime is not qualified','last update result is not retryable','candidate is not qualified'):
        require(token in mgr,f'Runtime Manager authority/policy missing: {token}')
    for op in ('runtime.manager','runtime.test','runtime.source.register','runtime.source.resolve','runtime.source.import-resolution','runtime.source.import-local'):
        require(f'"{op}"' in eng,f'typed Runtime Manager operation missing: {op}')
    require('case "manager":' in cli and 'runtime.manager' in cli,'racctl Runtime Manager parity missing')
    for token in ("query('runtime.manager')","run('runtime.source.resolve'","preview('migration.preview'","runtimeManagerAction(state.runtimeManager","runtimeCandidateAction(candidate"):
        require(token in app,f'WebUI does not consume canonical Runtime Manager authority: {token}')
    require('runtimeIssueCanRetry' in model and 'Backend did not expose this action' in model,'browser action helpers do not fail closed')
    require('env(safe-area-inset-bottom' in css and '.runtime-form' in css and '@media (max-width:680px)' in css,'mobile/safe-area Runtime Manager contract missing')
    require('[targets.rclone_nexus.jobs.ux-x01-audit]' in dev and 'ux-x01 = [' in dev and '[wrapper.commands.ux-x01]' in dev,'Devtool UX-X01 workflow missing')
    require('ux-x01-audit' in set(pol.get('required_ancestor_nodes',[])),'final seal policy bypasses UX-X01')

def assert_predecessor_progression()->None:
    path=ROOT/'scripts/dev/migrate_x01_gate.py'; spec=importlib.util.spec_from_file_location('migrate_x01_gate',path); require(spec is not None and spec.loader is not None,'cannot load MIGRATE-X01 gate')
    mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod); mod.assert_scope(); mod.assert_architecture()

def compiled_cli_proof()->None:
    with tempfile.TemporaryDirectory(prefix='rnx-ux-x01-') as raw:
        t=Path(raw); racctl=t/'racctl'; run(['go','build','-o',str(racctl),'./cmd/racctl'])
        state=t/'state'; binary=state/'runtime/active/bin/rclone'; config=state/'config/rclone/rclone.conf'; binary.parent.mkdir(parents=True); config.parent.mkdir(parents=True)
        binary.write_text("#!/bin/sh\necho 'rclone vUX-X01'\n",encoding='utf-8'); binary.chmod(0o755); config.write_text('[demo]\ntype = local\n',encoding='utf-8')
        env=os.environ.copy(); env.update({'RNEXUS_STATE_DIR':str(state),'RNEXUS_PROVIDER_MODULE_DIR':str(t/'provider-absent'),'RNEXUS_RUNTIME_MODE':'managed'})
        caps=json.loads(subprocess.check_output([str(racctl),'capabilities'],cwd=ROOT,env=env,text=True)); names={x.get('name') for x in caps.get('operations',[])}
        required={'runtime.manager','runtime.test','runtime.source.register','runtime.source.resolve','runtime.source.import-resolution','runtime.source.import-local','migration.preview','migration.finalize.preview'}
        require(required<=names,f'compiled capability registry missing: {sorted(required-names)}')
        manager=json.loads(subprocess.check_output([str(racctl),'runtime','manager'],cwd=ROOT,env=env,text=True))
        require(manager.get('schema_version')==1,'runtime manager schema mismatch')
        require(manager.get('runtime',{}).get('operational') is True,'compiled Runtime Manager did not observe operational managed authority')
        source_ids={x.get('id') for x in manager.get('sources',[])}; require({'bclone','rclone','newfuture'}<=source_ids,'builtin sources missing from Runtime Manager')
        require(manager.get('actions',{}).get('rollback',{}).get('enabled') is False,'rollback enabled without a previous runtime')
        require(bool(manager.get('actions',{}).get('rollback',{}).get('reason')),'disabled rollback lacks backend reason')

def main()->int:
    assert_scope(); assert_architecture(); assert_predecessor_progression()
    run(['go','test','-count=1','./internal/runtimemanager','./internal/control','./cmd/racctl','./internal/webui'])
    run(['node','scripts/dev/check_runtime_manager_ux.mjs'])
    run(['node','scripts/dev/check_webui_final.mjs'])
    run([sys.executable,'scripts/dev/platform_g1_gate.py'])
    run([sys.executable,'scripts/dev/web_g1_gate.py'])
    compiled_cli_proof()
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    print('UX-X01 Runtime Manager WebUI + CLI: PASS')
    print(json.dumps({'position':10,'count':28,'production_adopted':28,'blocked_by_environment':0,'status':ADOPTED,'next':'RUNTIME-GRAND-G1'},sort_keys=True))
    return 0
if __name__=='__main__': raise SystemExit(main())
