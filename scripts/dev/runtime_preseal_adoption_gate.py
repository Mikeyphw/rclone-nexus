#!/usr/bin/env python3
from __future__ import annotations
import json, re, subprocess, sys, tomllib
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
CURRENT=ROOT/'release/runtime-grand-g1-policy.json'
LEGACY=ROOT/'release/final-seal-policy.json'
ADOPTED='IMPLEMENTED_AND_PRODUCTION_ADOPTED'
PARTIAL='PARTIALLY_ADOPTED'
UNIMPLEMENTED='UNIMPLEMENTED'

def fail(msg:str)->None:
    print(f'ERROR: {msg}',file=sys.stderr); raise SystemExit(1)
def require(cond:bool,msg:str)->None:
    if not cond: fail(msg)
def read(rel:str)->str: return (ROOT/rel).read_text(encoding='utf-8')
def run(argv:list[str])->None:
    print('$ '+' '.join(argv)); subprocess.run(argv,cwd=ROOT,check=True)

def config_nodes()->set[str]:
    cfg=tomllib.loads(read('.devtool.toml')); target=cfg['targets']['rclone_nexus']
    out=set(target.get('jobs',{}))
    for flow in target.get('workflows',{}).values():
        if isinstance(flow,list):
            for node in flow:
                if isinstance(node,dict) and node.get('id'): out.add(str(node['id']))
    return out

def evidence_resolves(ref:str,nodes:set[str])->bool:
    ref=ref.strip()
    if not ref: return False
    if '::' in ref:
        path,node=ref.split('::',1); p=ROOT/path
        return p.is_file() and node.strip() in p.read_text(encoding='utf-8',errors='replace')
    if '/' in ref: return (ROOT/ref).is_file()
    return ref in nodes

def assert_adoption()->dict:
    d=json.loads(LEDGER.read_text()); items=d['items']; by={i['id']:i for i in items}
    require(d.get('promise_count')==514 and d.get('max_promise_number')==514,'canonical scope is not P514')
    require([i['id'] for i in items]==[f'RNX-P{i:03d}' for i in range(1,515)],'canonical scope is not contiguous')
    meta=d.get('preseal_adoption_requalification') or {}
    source_ids=list(meta.get('source_requalified_ids') or []); device_ids=list(meta.get('device_pending_ids') or [])
    require(len(source_ids)==146 and meta.get('source_requalified_count')==146,'source requalification set is stale')
    require(len(device_ids)==51 and meta.get('device_pending_count')==51,'device-pending legacy set is stale')
    require(meta.get('final_gate_pending_ids')==['RNX-P229'],'legacy final-gate pending set is stale')
    nodes=config_nodes()
    for pid in source_ids:
        item=by.get(pid); require(item and item.get('status')==ADOPTED,f'{pid} was not freshly adopted')
        for ref in item.get('evidence') or []: require(evidence_resolves(str(ref),nodes),f'{pid} unresolved evidence: {ref}')
    partial=[i for i in items if i.get('status')==PARTIAL]
    require(len(partial)==75,f'expected 75 honest partials after G1-A, found {len(partial)}')
    for item in partial:
        ev=set(item.get('evidence') or [])
        if 467 <= int(item['id'].split('P')[1]) <= 489:
            require('runtime-grand-g1-device-source-audit' in ev,f"G1-A device promise lacks implementation evidence: {item['id']}")
        else:
            require('device-evidence' in ev or 'grand-g1-audit' in ev,f"source-only promise left partial: {item['id']}")
    unimpl=[i['id'] for i in items if i.get('status')==UNIMPLEMENTED]
    require(unimpl==[f'RNX-P{i:03d}' for i in range(490,501)],f'G1-A unimplemented set drifted: {unimpl[:3]}..{unimpl[-3:] if unimpl else []}')
    return {'source_requalified':len(source_ids),'legacy_device_pending':len(device_ids),'runtime_grand_g1_device_partial':23,'legacy_final_pending':1,'runtime_grand_g1_unimplemented':len(unimpl)}

def assert_current_final_policy()->None:
    p=json.loads(CURRENT.read_text()); legacy=json.loads(LEGACY.read_text())
    require(p.get('campaign')=='RUNTIME-STANDALONE' and p.get('campaign_position')==11,'current final policy identity is wrong')
    require(p.get('canonical_range')=='RNX-P001..RNX-P514' and p.get('canonical_promise_count')==514,'current final policy range is stale')
    require(p.get('real_device_promises')==[f'RNX-P{i:03d}' for i in range(467,490)],'current real-device promise set is stale')
    require(p.get('final_gate_promises')==[f'RNX-P{i:03d}' for i in range(490,501)],'current final-gate promise set is stale')
    require(p.get('device_harness')=='scripts/dev/runtime_grand_g1_device.py','current policy does not bind G1-A harness')
    require(p.get('device_validate_node')=='runtime-grand-g1-device-validate','current policy does not bind G1-A validation node')
    require(p.get('legacy_grand_g1_may_seal_current_campaign') is False,'legacy seal is not explicitly forbidden')
    require(legacy.get('legacy_summary_only') is True and legacy.get('current_campaign')=='RUNTIME-STANDALONE','legacy final policy is not marked historical')
    nodes=config_nodes()
    for node in p.get('required_source_nodes') or []: require(node in nodes,f'current final policy source node does not resolve: {node}')
    for node in p.get('required_device_nodes') or []: require(node in nodes,f'current final policy device node does not resolve: {node}')
    cp=subprocess.run([sys.executable,'scripts/dev/grand_g1_gate.py','--source-only'],cwd=ROOT,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    require(cp.returncode!=0,'legacy GRAND-G1 unexpectedly became seal-eligible')
    require('historical-only' in (cp.stdout+cp.stderr) and 'RUNTIME-GRAND-G1' in (cp.stdout+cp.stderr),'legacy gate failure is not the permanent current-campaign fence')

def assert_docs()->None:
    roadmap=read('docs/ROADMAP.md'); readme=read('README.md')
    require('Historical 16-position campaign only' in roadmap and 'RUNTIME-GRAND-G1 remains open' in roadmap,'historical roadmap status is still presented as current')
    require('Active implementation campaign' in readme and 'RUNTIME_STANDALONE_ROADMAP.md' in readme,'README does not point at active 11-position campaign')
    forbidden=('Status: **GRAND-G1 implemented (16/16)','There is no remaining roadmap overlay')
    for token in forbidden: require(token not in roadmap,f'stale current 16/16 claim remains: {token}')

def assert_ci_immutability()->None:
    workflow=read('.github/workflows/runtime-source-build.yml'); builder=read('scripts/dev/runtime_source_build.py'); bundle=read('internal/runtimebuild/bundle.go')
    pins={
      'actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1',
      'actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e',
      'actions/upload-artifact@bbbca2ddaa5d8feaa63e36b76fdaad77386f024f',
    }
    for pin in pins: require(pin in workflow,f'immutable SOURCE-X02 action pin missing: {pin}')
    require(not re.search(r'uses:\s+actions/(?:checkout|setup-go|upload-artifact)@v\d+',workflow),'mutable SOURCE-X02 action tag remains')
    for token in ("GO_VERSION: '1.27.1'","NDK_VERSION: '28.2.13676358'","ANDROID_API: '21'"): require(token in workflow,f'toolchain pin missing: {token}')
    for token in ('ci_runner_os','ci_runner_arch','ci_image_os','ci_image_version'):
        require(token in builder and token in bundle,f'CI environment provenance missing: {token}')

def assert_cross_gate_behavior()->None:
    for rel in ('scripts/dev/source_x01_gate.py','scripts/dev/update_x01_gate.py','scripts/dev/ux_x01_gate.py'):
        text=read(rel); require('TestRuntimeManagerSourcePolicyEndToEnd' in text,f'{rel} lost shared source/update/UX behavioral proof')
    x02=read('scripts/dev/source_x02_gate.py')
    require('requiresPublishedBuild' in x02 and 'runtimebuild.AcquirePublished' in x02,'SOURCE-X02 gate lost production build-result bridge proof')
    run(['go','test','-count=1','./internal/control','-run','^TestRuntimeManagerSourcePolicyEndToEnd$'])
    run(['go','test','-count=1','./internal/runtimesource','-run','^TestSourceChannelCapabilitiesRejectImpossibleCombinations$|^TestResolveRejectsUnsupportedChannelBeforeNetwork$'])
    run(['go','test','-count=1','./internal/runtimebuild','-run','^TestPublishedBuildBecomesPendingSourceBuildCandidate$|^TestPublishedBuildResolutionRemainsReplayableAfterDownloadTempRemoved$'])
    run(['go','test','-count=1','./internal/runtimeupdate','-run','^TestAcquireWithoutAutomaticQualificationStopsAtAcquired$'])
    run(['node','scripts/dev/check_runtime_manager_ux.mjs'])

def main()->int:
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    counts=assert_adoption(); assert_current_final_policy(); assert_docs(); assert_ci_immutability(); assert_cross_gate_behavior()
    print('RUNTIME PRE-SEAL adoption/gate audit: PASS')
    print(json.dumps({'canonical_range':'RNX-P001..RNX-P514',**counts,'next':'RUNTIME-GRAND-G1 real-device/final-seal implementation'},sort_keys=True))
    return 0
if __name__=='__main__': raise SystemExit(main())
