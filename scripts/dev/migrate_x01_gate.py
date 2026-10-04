#!/usr/bin/env python3
from __future__ import annotations
import hashlib, json, os, signal, subprocess, sys, tempfile, time
from pathlib import Path
import tomllib

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
POLICY=ROOT/'release/final-seal-policy.json'
ADOPTED='IMPLEMENTED_AND_PRODUCTION_ADOPTED'
POS9=set(range(106,127))|set(range(435,449))

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
    actual={int(x['number']) for x in d['items'] if x.get('position')==9}
    require(actual==POS9,f'position-9 universe mismatch: {sorted(actual^POS9)}')
    for n in sorted(POS9):
        x=by[n]; require(x.get('status')==ADOPTED,f'RNX-P{n:03d} status={x.get("status")}')
        refs=x.get('evidence') or []; require('migrate-x01-audit' in refs,f'RNX-P{n:03d} not sealed by migrate-x01-audit')
        for r in refs: require(evidence_resolves(str(r)),f'RNX-P{n:03d} evidence missing: {r}')
    a=d.get('active_position',{}); position=int(a.get('position') or 0)
    require(position>=9,'campaign position regressed before MIGRATE-X01')
    if position==9:
        require(a.get('name')=='MIGRATE-X01','active position 9 is not MIGRATE-X01')
        require(a.get('production_adopted_count')==35 and a.get('blocked_by_environment_count')==0,'position-9 counts stale')

def assert_architecture()->None:
    mig=read('internal/migration/migration.go'); eng=read('internal/control/engine.go'); cli=read('cmd/racctl/main.go'); paths=read('internal/paths/paths.go'); install=read('scripts/dev/install_stack.py'); pkg=read('scripts/dev/check-package.py'); dev=read('.devtool.toml'); pol=json.loads(POLICY.read_text())
    for token in ('AWAITING_PROVIDER_DISABLE','ProviderConfigSHA256','ProviderRcloneVersion','parseJobFile','provider lifecycle refused stop','provider disappeared mid-migration','duplicate destination path','imported job conflicts with existing Nexus job','legacy provider lifecycle became active after standalone migration','authority switch failed; restored pre-migration Nexus state'):
        require(token in mig,f'migration production invariant missing: {token}')
    require('syscall.Flock' in mig and 'MigrationLock' in mig,'migration mutations are not serialized')
    require('ms := selectedSet(req.SelectedMounts, mountIDs, false)' in mig,'mount import is not explicit opt-in')
    require('"migration.preview"' in eng and '"migration.finalize.preview"' in eng and 'previewproof.Consume' in eng,'typed proof-bound migration operations incomplete')
    require('migration.Recover(context.Background(), p)' in cli and 'case "migration":' in cli,'daemon/CLI migration production ingress incomplete')
    require('MigrationState' in paths and 'MigrationEvidenceDir' in paths,'durable migration state paths absent')
    require("args.command == \"install\"" in install or 'if args.command == "install"' in install,'plain Nexus install command missing')
    require('rclone-nexus-v' in pkg and 'webroot/index.html' in pkg and 'integrity.manifest.json' in pkg,'standalone package contract incomplete')
    require('migrate-x01-audit' in set(pol.get('required_ancestor_nodes',[])),'final seal policy bypasses MIGRATE-X01')
    require('[wrapper.commands.migrate-x01]' in dev and '[targets.rclone_nexus.jobs.migrate-x01-audit]' in dev and 'migrate-x01 = [' in dev,'Devtool migration workflow missing')
    require('/data/adb/modules/rclone' in read('README.md') and 'No mutation' in read('README.md'),'provider non-mutation contract missing')

def tree_hash(root:Path)->str:
    h=hashlib.sha256()
    for p in sorted(x for x in root.rglob('*') if x.is_file() or x.is_symlink()):
        rel=p.relative_to(root).as_posix().encode(); h.update(rel+b'\0')
        if p.is_symlink(): h.update(os.readlink(p).encode())
        else: h.update(p.read_bytes())
        h.update(b'\0')
    return h.hexdigest()

def cmd_json(argv:list[str], env:dict[str,str]):
    cp=subprocess.run(argv,cwd=ROOT,env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    require(cp.returncode==0,f'command failed {argv}: {cp.stderr}')
    return json.loads(cp.stdout)

def assert_cli_migration()->None:
    with tempfile.TemporaryDirectory(prefix='rnx-migrate-x01-') as raw:
        t=Path(raw); racctl=t/'racctl'; run(['go','build','-o',str(racctl),'./cmd/racctl'])
        provider=t/'provider'; conf=provider/'conf'; conf.mkdir(parents=True); (provider/'module.prop').write_text('id=rclone\nversion=1.75.0\n')
        (conf/'rclone.conf').write_text('[demo]\ntype = local\n'); (conf/'sync').write_text('demo:src /tmp/rnx-migrate-dst\n')
        runner=provider/'runner.sh'; runner.write_text("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"); runner.chmod(0o755)
        pbin=provider/'system/bin/rclone'; pbin.parent.mkdir(parents=True); pbin.write_text("#!/bin/sh\n[ \"$1\" = version ] && { echo 'rclone v1.75.0'; exit 0; }; exit 0\n"); pbin.chmod(0o755)
        state=t/'state'; mbin=state/'runtime/active/bin/rclone'; mbin.parent.mkdir(parents=True); mbin.write_text("#!/bin/sh\nif [ \"$1\" = listremotes ]; then echo demo:; exit 0; fi\n[ \"$1\" = version ] && { echo 'rclone managed'; exit 0; }; exit 0\n"); mbin.chmod(0o755)
        mountp=t/'mountpoint'; mountp.mkdir()
        procs=[subprocess.Popen(['/bin/sh',str(runner),'mount','demo:',str(mountp)]), subprocess.Popen(['/bin/sh',str(runner),'rclone-web','--rc-addr','127.0.0.1:5572'])]
        try:
            env=os.environ.copy(); env.update({'RNEXUS_STATE_DIR':str(state),'RNEXUS_PROVIDER_MODULE_DIR':str(provider),'RNEXUS_MIGRATION_QUIESCE_TIMEOUT_MS':'2000'})
            time.sleep(.15)
            ins=cmd_json([str(racctl),'migration','inspect'],env)
            require(ins['provider_present'] and ins['provider_enabled'] and ins['provider_config_sha256'],'provider/config detection failed')
            require(ins['provider_rclone_version'].startswith('rclone v1.75.0'),'provider version/path detection failed')
            require(len(ins['mounts'])>=1 and any(x['type']=='sync' for x in ins['jobs']),'provider mounts/jobs not discovered')
            require(ins['service_active'] and ins['webui_active'],'provider service/WebUI activity not detected')
            mid=ins['mounts'][0]['id']; require(mid.startswith('mount:') and '\x00' not in mid,'mount selection ID not printable')
            mp=cmd_json([str(racctl),'migration','preview','--mount',mid],env); require(len(mp['preview']['selected_mounts'])==1,'explicit mount review failed')
            before=tree_hash(provider)
            pv=cmd_json([str(racctl),'migration','preview','--job','sync:1','--job-every','24h'],env)
            require(len(pv['preview']['selected_mounts'])==0 and len(pv['preview']['selected_jobs'])==1 and pv['preview']['can_apply'],'reviewed migration preview wrong')
            cmd_json([str(racctl),'migration','apply',pv['preview_proof'],str(pv['current_revision']),pv['candidate_digest'],'--job','sync:1','--job-every','24h'],env)
            require(tree_hash(provider)==before,'Nexus mutated provider files during apply/quiesce')
            st=cmd_json([str(racctl),'migration','status'],env); require(st['state']['phase']=='AWAITING_PROVIDER_DISABLE','migration did not stop before explicit provider disable')
            (provider/'disable').write_text('\n') # external/root-manager action, deliberately outside Nexus
            fp=cmd_json([str(racctl),'migration','finalize-preview'],env); require(fp['preview']['can_finalize'],'finalize preview rejected disabled provider')
            fin=cmd_json([str(racctl),'migration','finalize',fp['preview_proof'],str(fp['current_revision']),fp['candidate_digest']],env)
            require(fin['phase']=='COMPLETED' and Path(fin['evidence_path']).is_file(),'standalone migration did not complete with durable evidence')
            require((provider/'module.prop').is_file() and (conf/'rclone.conf').is_file(),'Nexus removed/mutated legacy provider module')
        finally:
            for p in procs:
                if p.poll() is None:
                    p.send_signal(signal.SIGTERM)
                    try: p.wait(timeout=1)
                    except subprocess.TimeoutExpired: p.kill(); p.wait()

def main()->int:
    assert_scope(); assert_architecture()
    run(['go','test','-count=1','./internal/migration','./internal/control','./cmd/racctl'])
    run(['go','test','-count=1','./internal/diagnostics','./internal/doctor','./internal/integrity','./internal/rootmgr','./internal/platformstate','./internal/platformlifecycle'])
    run([sys.executable,'-m','unittest','-v','tests.test_install_stack'])
    run([sys.executable,'scripts/dev/check-module-contract.py'])
    run([sys.executable,'scripts/dev/check-package.py'])
    assert_cli_migration()
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    print('MIGRATE-X01 NewFuture migration + standalone packaging: PASS')
    print(json.dumps({'position':9,'count':35,'production_adopted':35,'blocked_by_environment':0,'status':ADOPTED,'next':'UX-X01'},sort_keys=True))
    return 0
if __name__=='__main__': raise SystemExit(main())
