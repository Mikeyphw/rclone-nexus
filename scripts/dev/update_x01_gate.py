#!/usr/bin/env python3
from __future__ import annotations
import json, os, subprocess, sys, tempfile
from pathlib import Path
import tomllib

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
POLICY=ROOT/'release/final-seal-policy.json'
ADOPTED='IMPLEMENTED_AND_PRODUCTION_ADOPTED'

def fail(msg:str)->None: print(f'ERROR: {msg}',file=sys.stderr); raise SystemExit(1)
def require(cond:bool,msg:str)->None:
    if not cond: fail(msg)
def read(rel:str)->str: return (ROOT/rel).read_text(encoding='utf-8')
def run(argv:list[str], env:dict[str,str]|None=None)->None:
    print('$ '+' '.join(argv)); subprocess.run(argv,cwd=ROOT,check=True,env=env)

def evidence_resolves(ref:str)->bool:
    ref=ref.strip()
    if '::' in ref:
        path_part,node=ref.split('::',1); p=ROOT/path_part
        return p.is_file() and bool(node.strip()) and node in p.read_text(encoding='utf-8',errors='replace')
    if '/' in ref: return (ROOT/ref).is_file()
    try:
        cfg=tomllib.loads(read('.devtool.toml')); target=cfg['targets']['rclone_nexus']
        jobs=set(target.get('jobs',{})); nodes={str(n.get('id')) for f in target.get('workflows',{}).values() if isinstance(f,list) for n in f if isinstance(n,dict) and n.get('id')}
        return ref in jobs or ref in nodes
    except Exception: return False

def assert_scope()->None:
    data=json.loads(LEDGER.read_text()); by={x['id']:x for x in data['items']}
    count=int(data.get('promise_count') or 0); maximum=int(data.get('max_promise_number') or 0); items=data.get('items', [])
    require(count>0 and maximum==count and len(items)==count and [x.get('id') for x in items]==[f'RNX-P{i:03d}' for i in range(1,count+1)],'canonical scope is not contiguous')
    for n in range(404,426):
        pid=f'RNX-P{n:03d}'; item=by.get(pid); require(isinstance(item,dict),f'missing UPDATE-X01 promise {pid}')
        require(item.get('status')==ADOPTED,f'{pid} status={item.get("status")} want {ADOPTED}')
        refs=item.get('evidence') or []; require('update-x01-audit' in refs,f'{pid} not bound to UPDATE-X01 executable gate')
        for ref in refs: require(evidence_resolves(str(ref)),f'{pid} evidence does not resolve: {ref}')
    active=data.get('active_position',{}); position=int(active.get('position') or 0)
    require(position>=7,'canonical active position regressed before UPDATE-X01')
    if position==7:
        require(active.get('promise_range')=='RNX-P404..RNX-P425','canonical UPDATE-X01 active promise range is stale')
        require(active.get('production_adopted_count')==22 and active.get('blocked_by_environment_count')==0,'UPDATE-X01 status counts stale')

def assert_architecture()->None:
    update=read('internal/runtimeupdate/update.go'); store=read('internal/runtimestore/store.go'); activation=read('internal/runtimeactivation/activation.go')
    cli=read('cmd/racctl/main.go'); engine=read('internal/control/engine.go'); web=read('module/webroot/app.js'); service=read('module/service.sh'); daemon=read('internal/daemon/runtime_update_scheduler.go')
    docs=read('docs/implementation/UPDATE-X01.md'); x02=read('scripts/dev/source_x02_gate.py'); devtool=read('.devtool.toml'); policy=json.loads(POLICY.read_text())
    compact=''.join(update.split())
    for token in ('CheckAutomatically:true','AcquireAutomatically:true','QualifyAutomatically:true','StageAutomatically:true','ActivationMode:ActivationNextReboot','RestartActiveMountsAutomatically:false'):
        require(token in compact,f'safe default policy missing: {token}')
    for token in ('CurrentRuntimeID','StagedRuntimeID','PreviousRuntimeID','StagedMissing'):
        require(token in update,f'update status identity missing: {token}')
    require('RunRuntimeUpdateScheduler' in daemon and 'runtime.update.check' in daemon,'automatic check scheduler not production-wired')
    require('runtime update boot-activate' in service,'next-reboot staged activation is not in production boot path')
    require('runtime.update.status' in engine and 'runtime.update.check' in engine and 'runtime.update.activate' in engine and 'runtime.update.rollback' in engine and 'runtime.update.gc' in engine,'typed update engine operations incomplete')
    require('runtime update <command>' in cli and 'policy-set' in cli and 'boot-activate' in cli,'CLI update ingress incomplete')
    require('runtime.update.status' in web and 'runtime.update.rollback' in web and 'Check for update' in web and 'Activate staged' in web and "managerActionButton('Rollback','rollback'" in web,'WebUI update status/actions missing')
    require('extractZipRuntime' in store and 'runtime archive contains symlink' in store and 'runtime archive path escapes archive root' in store,'secure archive acquisition boundary incomplete')
    require('ArchiveSHA256:  archiveDigest' in store and 'BinarySHA256:   binaryDigest' in store,'archive/binary identities are collapsed')
    require('func GarbageCollect' in store and 'sole runtime-store cleanup authority' in store,'canonical runtime cleanup authority missing')
    require('func Acquire(' in store and 'State:            "pending"' in store,'runtime store lacks acquisition-only candidate boundary')
    require('runtimeacquire.AcquireResolution' in update and 'runtimestore.Test(ctx, p, manifest.RuntimeID)' in update,'UPDATE-X01 does not consume unified source/build acquisition before qualification')
    require('state.LastResult = "acquired"' in update and 'if !policy.QualifyAutomatically' in update,'qualification-disabled policy does not stop cleanly after acquisition')
    require('effectiveResolution.ResolutionID' in update and 'resolutionMatchesRuntime(p, activation.State.ActiveRuntimeID, resolution)' in update,'UPDATE-X01 does not preserve effective build-result authority or match build candidates to upstream commits')
    require('func Stage(' in activation and 'func ActivateStaged(' in activation,'qualified staging/activation seam missing')
    require('position>=6' in x02.replace(' ',''),'SOURCE-X02 inherited gate is not progression-safe')
    require('[wrapper.commands.update-x01]' in devtool and '[targets.rclone_nexus.jobs.update-x01-audit]' in devtool and 'update-x01 = [' in devtool,'Devtool UPDATE-X01 wrapper/job/workflow missing')
    required=set(policy.get('required_ancestor_nodes',[])); require('update-x01-audit' in required,'final seal policy not bound to UPDATE-X01')
    for flow in ('grand-g1-source = [','release = ['):
        start=devtool.index(flow); end=devtool.find('\n]',start); body=devtool[start:end]
        require('update-x01-audit' in body,f'{flow.split()[0]} bypasses UPDATE-X01')
        require(body.index('source-x02-audit') < body.index('update-x01-audit') < body.index('webui-final-contract'),f'{flow.split()[0]} UPDATE ordering malformed')
    require('failed or unqualified candidates are never staged' in docs.lower(),'fail-closed candidate policy is not documented')

def assert_cli()->None:
    with tempfile.TemporaryDirectory(prefix='rnx-update-x01-') as raw:
        t=Path(raw); racctl=t/'racctl'; run(['go','build','-o',str(racctl),'./cmd/racctl'])
        env=os.environ.copy(); env['RNEXUS_STATE_DIR']=str(t/'state'); env['RNEXUS_PROVIDER_MODULE_DIR']=str(t/'provider-absent')
        cp=subprocess.run([str(racctl),'runtime','update','status'],cwd=ROOT,env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        require(cp.returncode==0,f'compiled update status failed: {cp.stderr}')
        status=json.loads(cp.stdout); p=status['policy']; require(p['check_automatically'] and p['acquire_automatically'] and p['qualify_automatically'] and p['stage_automatically'],'compiled CLI default update policy is not safe pipeline-on')
        require(p['activation_mode']=='next-reboot' and not p['restart_active_mounts_automatically'],'compiled CLI default would hot swap active mounts')
        cp=subprocess.run([str(racctl),'runtime','update','policy-set','--activation','immediate','--restart-active-mounts','false'],cwd=ROOT,env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        require(cp.returncode!=0,'compiled CLI accepted immediate activation without restart authority')

def main()->int:
    assert_scope(); assert_architecture()
    run(['go','test','./internal/runtimeupdate','./internal/runtimestore','./internal/runtimeactivation','./internal/daemon','./internal/control','./cmd/racctl'])
    run(['go','test','-count=1','./internal/control','-run','^TestRuntimeManagerSourcePolicyEndToEnd$'])
    run(['go','test','-count=1','./internal/runtimeupdate','-run','TestAcquireWithoutAutomaticQualificationStopsAtAcquired'])
    run(['go','test','-count=1','./internal/runtimestore','-run','TestAcquirePublishesPendingCandidateWithoutRunningQualifier'])
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    run([sys.executable,'scripts/dev/source_x02_gate.py'])
    run(['sh','-n','module/service.sh'])
    run(['node','scripts/dev/check_webui_js.mjs'])
    assert_cli()
    print('UPDATE-X01 runtime update manager: PASS')
    print(json.dumps({'promise_range':'RNX-P404..RNX-P425','count':22,'production_adopted':22,'blocked_by_environment':0,'status':ADOPTED,'next':'SOURCE-G1'},sort_keys=True))
    return 0
if __name__=='__main__': raise SystemExit(main())
