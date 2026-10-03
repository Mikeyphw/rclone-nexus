#!/usr/bin/env python3
from __future__ import annotations
import json,re,sys
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
def fail(msg:str)->None:
    print(f'ERROR: {msg}',file=sys.stderr); raise SystemExit(1)

def _resolve_source_ref(ref:str)->bool:
    path_part=re.sub(r':L\d+$','',ref.split('#',1)[0].strip())
    return bool(path_part) and (ROOT/path_part).is_file()

def _resolve_evidence(ref:str)->bool:
    ref=ref.strip()
    if not ref:
        return False
    if '::' in ref:
        path_part,node=ref.split('::',1)
        path=ROOT/path_part
        if not path.is_file() or not node.strip():
            return False
        return node in path.read_text(encoding='utf-8',errors='replace')
    if '/' in ref:
        return (ROOT/ref).is_file()
    if ref.startswith('tests.'):
        return (ROOT/(ref.replace('.','/')+'.py')).is_file()
    # Bare references are executable Devtool workflow/job evidence only.
    try:
        import tomllib
        cfg=tomllib.loads((ROOT/'.devtool.toml').read_text(encoding='utf-8'))
        target=cfg.get('targets',{}).get('rclone_nexus',{})
        jobs=set(target.get('jobs',{}))
        workflow_ids=set()
        for flow in target.get('workflows',{}).values():
            if isinstance(flow,list):
                for node in flow:
                    if isinstance(node,dict) and node.get('id'):
                        workflow_ids.add(str(node['id']))
        return ref in jobs or ref in workflow_ids
    except Exception:
        return False
def main()->int:
    data=json.loads(LEDGER.read_text()); items=data.get('items')
    if data.get('schema_version')!=1 or data.get('campaign')!='RUNTIME-STANDALONE' or not isinstance(items,list): fail('canonical promise ledger identity malformed')
    if data.get('promise_count')!=500 or data.get('max_promise_number')!=500 or len(items)!=500: fail('canonical promise ledger must cover exactly RNX-P001..RNX-P500')
    if [x.get('id') for x in items] != [f'RNX-P{i:03d}' for i in range(1,501)]: fail('promise IDs are not contiguous RNX-P001..RNX-P500')
    counts={}
    adopted='IMPLEMENTED_AND_PRODUCTION_ADOPTED'
    for item in items:
        counts[item['source_family']]=counts.get(item['source_family'],0)+1
        if not item.get('promise') or not item.get('status'): fail(f"malformed promise: {item.get('id')}")
        source_ref=str(item.get('source_ref',''))
        if not _resolve_source_ref(source_ref): fail(f"unresolvable source_ref for {item.get('id')}: {source_ref}")
        evidence=item.get('evidence',[])
        if not isinstance(evidence,list): fail(f"malformed evidence for {item.get('id')}")
        if item.get('status')==adopted and not evidence: fail(f"production-adopted promise has no evidence: {item.get('id')}")
        if item.get('status')==adopted:
            for ref in evidence:
                if not _resolve_evidence(str(ref)): fail(f"unresolvable evidence for {item.get('id')}: {ref}")
    if counts!=data.get('source_counts'): fail(f'source family counts stale: {counts!r}')
    roadmap=(ROOT/'docs/campaign/RUNTIME_STANDALONE_ROADMAP.md').read_text().splitlines(); pos=False; bullets=0
    for line in roadmap:
        if line.startswith('## Position '): pos=True
        elif pos and line.startswith('- '): bullets+=1
    if bullets!=222: fail(f'RUNTIME-STANDALONE roadmap expected 222 position bullets, found {bullets}')
    guided=(ROOT/'docs/WEBUI_MOUNT_PROMISE_AUDIT.md').read_text()
    if len(re.findall(r'^\|\s*\d+\s*\|',guided,re.M))!=21: fail('guided mount ledger is not exactly 21 promises')
    runtime=(ROOT/'docs/GRAND_G1_DEVICE_RUNTIME_WEBUI_REMEDIATION_AUDIT.md').read_text().split('## Promise ledger',1)[1].split('## v2 promise-closure audit',1)[0]
    if [int(x) for x in re.findall(r'^(\d+)\.\s+',runtime,re.M)]!=list(range(1,22)): fail('device runtime/WebUI ledger is not exactly 1..21')
    for path in [ROOT/'scripts/dev/check_webui_mount_editor.py',ROOT/'scripts/dev/check_device_runtime_webui_remediation.py',ROOT/'docs/campaign/GOVERNING_CAMPAIGN_PROTOCOL.md']:
        if not path.is_file(): fail(f'missing scope evidence: {path.relative_to(ROOT)}')
    print('Canonical merged campaign scope: PASS')
    print(json.dumps({'campaign':'RUNTIME-STANDALONE','promise_range':'RNX-P001..RNX-P500','promise_count':500,'source_counts':counts},sort_keys=True))
    return 0
if __name__=='__main__': raise SystemExit(main())
