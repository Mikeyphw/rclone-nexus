#!/usr/bin/env python3
from __future__ import annotations
import json,re,sys
from collections import Counter
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
ROADMAP=ROOT/'docs/campaign/RUNTIME_STANDALONE_ROADMAP.md'
DISPOSITIONS=ROOT/'release/canonical-roadmap-obligation-dispositions.json'


def fail(msg:str)->None:
    print(f'ERROR: {msg}',file=sys.stderr)
    raise SystemExit(1)


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


def extract_runtime_roadmap(path:Path=ROADMAP)->tuple[list[dict],list[dict]]:
    """Return direct bullet obligations and every non-bullet clause inside positions.

    The supplemental inventory intentionally includes prose wrappers, goals and fenced
    examples/guidance. Each must be explicitly dispositioned so future normative prose
    cannot silently escape the canonical promise compiler.
    """
    lines=path.read_text(encoding='utf-8').splitlines()
    bullets:list[dict]=[]
    supplemental:list[dict]=[]
    position:int|None=None
    section:str|None=None
    i=0
    while i < len(lines):
        line=lines[i]
        m=re.match(r'^## Position (\d+)\b',line)
        if m:
            position=int(m.group(1)); section=None; i+=1; continue
        if position is None:
            i+=1; continue
        if line.startswith('## ') and not line.startswith('## Position '):
            # The only post-position section today is the position-11 finalization
            # sequence. Keep it owned by the current position while giving it an
            # explicit section identity.
            section=line[3:].strip(); i+=1; continue
        if line.startswith('### '):
            section=line[4:].strip(); i+=1; continue
        s=line.strip()
        if s.startswith('```'):
            content=[]; i+=1
            while i < len(lines) and not lines[i].strip().startswith('```'):
                content.append(lines[i]); i+=1
            if i < len(lines): i+=1
            supplemental.append({'position':position,'section':section,'kind':'code','text':'\n'.join(content).strip()})
            continue
        if not s or s=='---' or s.startswith('#'):
            i+=1; continue
        if s.startswith('- '):
            bullets.append({'position':position,'section':section,'text':s[2:].strip()}); i+=1; continue
        kind='numbered' if re.match(r'^\d+\.\s+',s) else 'prose'
        supplemental.append({'position':position,'section':section,'kind':kind,'text':s}); i+=1
    return bullets,supplemental


def _clause_key(item:dict)->tuple:
    return (int(item['position']),str(item.get('section') or ''),str(item['kind']),str(item['text']))


def validate_scope(data:dict, dispositions:dict)->dict:
    items=data.get('items')
    if data.get('schema_version')!=1 or data.get('campaign')!='RUNTIME-STANDALONE' or not isinstance(items,list):
        fail('canonical promise ledger identity malformed')
    count=int(data.get('promise_count') or 0); maximum=int(data.get('max_promise_number') or 0); minimum=int(data.get('min_promise_number') or 0)
    if minimum!=1 or count<=0 or maximum!=count or len(items)!=count:
        fail(f'canonical promise ledger size malformed: min={minimum} max={maximum} count={count} items={len(items)}')
    expected=[f'RNX-P{i:03d}' for i in range(1,maximum+1)]
    if [x.get('id') for x in items] != expected:
        fail(f'promise IDs are not contiguous RNX-P001..RNX-P{maximum:03d}')
    if [int(x.get('number') or 0) for x in items] != list(range(1,maximum+1)):
        fail('promise number fields are not contiguous')

    counts={}; adopted='IMPLEMENTED_AND_PRODUCTION_ADOPTED'
    for item in items:
        counts[item['source_family']]=counts.get(item['source_family'],0)+1
        if not item.get('promise') or not item.get('status'):
            fail(f"malformed promise: {item.get('id')}")
        source_ref=str(item.get('source_ref',''))
        if not _resolve_source_ref(source_ref):
            fail(f"unresolvable source_ref for {item.get('id')}: {source_ref}")
        evidence=item.get('evidence',[])
        if not isinstance(evidence,list):
            fail(f"malformed evidence for {item.get('id')}")
        if item.get('status')==adopted and not evidence:
            fail(f"production-adopted promise has no evidence: {item.get('id')}")
        if item.get('status')==adopted:
            for ref in evidence:
                if not _resolve_evidence(str(ref)):
                    fail(f"unresolvable evidence for {item.get('id')}: {ref}")
    if counts!=data.get('source_counts'):
        fail(f'source family counts stale: {counts!r}')

    if dispositions.get('schema_version')!=1 or dispositions.get('roadmap')!='docs/campaign/RUNTIME_STANDALONE_ROADMAP.md':
        fail('roadmap obligation disposition manifest identity malformed')
    roadmap_text=ROADMAP.read_text(encoding='utf-8')
    for g in dispositions.get('global_clauses',[]):
        if str(g.get('text','')) not in roadmap_text:
            fail(f"global roadmap clause missing/stale: {g.get('id')}")

    bullets,supplemental=extract_runtime_roadmap()
    manifest_clauses=dispositions.get('supplemental_clauses')
    if not isinstance(manifest_clauses,list):
        fail('supplemental roadmap disposition list malformed')
    actual=Counter(_clause_key(x) for x in supplemental)
    declared=Counter(_clause_key(x) for x in manifest_clauses)
    if actual!=declared:
        missing=list((actual-declared).elements()); stale=list((declared-actual).elements())
        fail(f'roadmap supplemental clause dispositions are incomplete/stale: unclassified={missing[:5]!r} stale={stale[:5]!r}')

    valid_classes={'mapped','new-promise','governance','guidance','wrapper','illustrative'}
    new_ids:list[str]=[]
    for clause in manifest_clauses:
        cls=clause.get('classification')
        if cls not in valid_classes:
            fail(f"unknown roadmap clause classification {cls!r}: {clause.get('id')}")
        pids=clause.get('promise_ids',[])
        if not isinstance(pids,list):
            fail(f"malformed promise_ids: {clause.get('id')}")
        if clause.get('kind')=='numbered' and cls not in {'mapped','new-promise'}:
            fail(f"numbered roadmap obligation may not be classified {cls}: {clause.get('id')}")
        if cls in {'mapped','new-promise'} and not pids:
            fail(f"mapped roadmap clause has no canonical promise disposition: {clause.get('id')}")
        for pid in pids:
            if pid not in expected:
                fail(f"roadmap disposition references nonexistent promise {pid}: {clause.get('id')}")
        if cls=='new-promise':
            new_ids.extend(str(x) for x in pids)
    if len(new_ids)!=len(set(new_ids)):
        fail('one newly canonical promise is claimed by more than one roadmap clause')

    runtime_items=[x for x in items if x.get('source_family')=='runtime-standalone']
    by_id={x['id']:x for x in runtime_items}
    for pid in new_ids:
        item=by_id.get(pid)
        if not item:
            fail(f'new roadmap promise missing from runtime-standalone ledger family: {pid}')
        clause=next(c for c in manifest_clauses if pid in c.get('promise_ids',[]) and c.get('classification')=='new-promise')
        if int(item.get('position') or 0)!=int(clause['position']):
            fail(f'{pid} position does not match roadmap clause')

    direct_items=[x for x in runtime_items if x['id'] not in set(new_ids)]
    if Counter(x['promise'].strip() for x in direct_items)!=Counter(x['text'] for x in bullets):
        fail('runtime-standalone direct-bullet ledger no longer exactly matches roadmap position bullets')
    if len(runtime_items)!=len(bullets)+len(new_ids):
        fail(f'runtime-standalone source count does not equal direct bullets + newly canonical clauses: {len(runtime_items)} != {len(bullets)} + {len(new_ids)}')

    guided=(ROOT/'docs/WEBUI_MOUNT_PROMISE_AUDIT.md').read_text(encoding='utf-8')
    if len(re.findall(r'^\|\s*\d+\s*\|',guided,re.M))!=21:
        fail('guided mount ledger is not exactly 21 promises')
    runtime=(ROOT/'docs/GRAND_G1_DEVICE_RUNTIME_WEBUI_REMEDIATION_AUDIT.md').read_text(encoding='utf-8').split('## Promise ledger',1)[1].split('## v2 promise-closure audit',1)[0]
    if [int(x) for x in re.findall(r'^(\d+)\.\s+',runtime,re.M)]!=list(range(1,22)):
        fail('device runtime/WebUI ledger is not exactly 1..21')
    for path in [ROOT/'scripts/dev/check_webui_mount_editor.py',ROOT/'scripts/dev/check_device_runtime_webui_remediation.py',ROOT/'docs/campaign/GOVERNING_CAMPAIGN_PROTOCOL.md']:
        if not path.is_file():
            fail(f'missing scope evidence: {path.relative_to(ROOT)}')
    return {'maximum':maximum,'counts':counts,'direct_bullets':len(bullets),'supplemental_promises':len(new_ids),'supplemental_clauses':len(supplemental)}


def main()->int:
    data=json.loads(LEDGER.read_text(encoding='utf-8'))
    dispositions=json.loads(DISPOSITIONS.read_text(encoding='utf-8'))
    result=validate_scope(data,dispositions)
    print('Canonical merged campaign scope: PASS')
    print(json.dumps({'campaign':'RUNTIME-STANDALONE','promise_range':f"RNX-P001..RNX-P{result['maximum']:03d}",'promise_count':result['maximum'],'source_counts':result['counts'],'runtime_direct_bullets':result['direct_bullets'],'runtime_supplemental_promises':result['supplemental_promises'],'tracked_nonbullet_clauses':result['supplemental_clauses']},sort_keys=True))
    return 0

if __name__=='__main__':
    raise SystemExit(main())
