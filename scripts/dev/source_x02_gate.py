#!/usr/bin/env python3
from __future__ import annotations
import hashlib, json, os, struct, subprocess, sys, tempfile
from pathlib import Path
import tomllib

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
POLICY=ROOT/'release/final-seal-policy.json'
ADOPTED='IMPLEMENTED_AND_PRODUCTION_ADOPTED'

def fail(msg:str)->None:
    print(f'ERROR: {msg}',file=sys.stderr); raise SystemExit(1)
def require(cond:bool,msg:str)->None:
    if not cond: fail(msg)
def read(rel:str)->str: return (ROOT/rel).read_text(encoding='utf-8')
def run(argv:list[str])->None:
    print('$ '+' '.join(argv)); subprocess.run(argv,cwd=ROOT,check=True)

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
    for n in range(391,404):
        pid=f'RNX-P{n:03d}'; item=by.get(pid)
        require(isinstance(item,dict),f'missing SOURCE-X02 promise {pid}')
        require(item.get('status')==ADOPTED,f'{pid} status={item.get("status")} want {ADOPTED}')
        refs=item.get('evidence') or []
        require('source-x02-audit' in refs,f'{pid} not bound to SOURCE-X02 executable gate')
        for ref in refs: require(evidence_resolves(str(ref)),f'{pid} evidence does not resolve: {ref}')
    active=data.get('active_position',{})
    position=int(active.get('position') or 0)
    require(position>=6,'canonical campaign regressed before SOURCE-X02')
    if position==6:
        require(active.get('promise_range')=='RNX-P391..RNX-P403','canonical active SOURCE-X02 range is stale')
        require(active.get('production_adopted_count')==13 and active.get('blocked_by_environment_count')==0,'SOURCE-X02 status counts stale')

def assert_architecture()->None:
    bundle=read('internal/runtimebuild/bundle.go'); tests=read('internal/runtimebuild/bundle_test.go')
    source=read('internal/runtimesource/source.go'); source_tests=read('internal/runtimesource/source_test.go')
    builder=read('scripts/dev/runtime_source_build.py'); workflow=read('.github/workflows/runtime-source-build.yml')
    cli=read('cmd/racctl/main.go'); docs=read('docs/implementation/SOURCE-X02.md'); devtool=read('.devtool.toml')
    x01=read('scripts/dev/source_x01_gate.py'); policy=json.loads(POLICY.read_text())
    for token in ('repository','requested_ref','resolved_commit','go_version','ndk_version','ndk_host','compiler','compiler_version','compiler_mode','compiler_target','binary_sha256'):
        require(token in bundle.lower() or token in builder.lower(),f'provenance field missing: {token}')
    require('GOOS": "android"' in builder and 'GOARCH": "arm64"' in builder and 'CGO_ENABLED": "1"' in builder,'Android arm64 build environment missing')
    require('aarch64-linux-android' in builder and '"android"' in builder and '-trimpath' in builder and '-fuse-ld=lld' in builder,'upstream-style Android build recipe incomplete')
    require('native-clang-ndk-sysroot' in builder and 'CGO_CFLAGS' in builder and '--sysroot=' in builder and '-resource-dir=' in builder and 'compiler_resource_dir' in builder,'Termux native-clang + pinned NDK sysroot/resource-dir build mode missing')
    require('CompilerMode' in bundle and 'CompilerTarget' in bundle and 'CompilerResourceDir' in bundle and 'CompilerRTBuiltinsSHA256' in bundle and 'CompilerLibunwindSHA256' in bundle and 'native-clang-ndk-sysroot' in bundle,'SOURCE-X02 verifier does not bind alternate compiler/resource provenance')
    require('source checkout HEAD' in builder and 'resolved commit must be immutable full 40-hex SHA' in builder,'exact commit pinning not enforced before build')
    require('schedule:' in workflow and 'workflow_dispatch:' in workflow and 'BenjiThatFoxGuy/bclone' in workflow,'scheduled latest bclone/manual workflow missing')
    require('runtime-build-v1-' in workflow and 'gh release create' in workflow and 'runtime-source-build.tar' in workflow,'SOURCE-X02 does not publish immutable build results for production consumption')
    acquire=read('internal/runtimeacquire/acquire.go'); published=read('internal/runtimebuild/published.go')
    require('requiresPublishedBuild' in acquire and 'runtimebuild.AcquirePublished' in acquire,'production import path does not bridge GitHub resolutions into SOURCE-X02 build results')
    require('PublishedBuildContract = "v1"' in published and 'VerifyBundle(dir)' in published and 'provenance does not match immutable upstream resolution' in published,'published build result lacks immutable/provenance verification')
    require('runtime source resolve bclone --channel latest-stable' in workflow,'scheduled bclone build bypasses SOURCE-X01 latest-stable resolver')
    require('resolve-ref' in workflow and "ref: ${{ steps.resolve.outputs.commit }}" in workflow,'manual mutable refs are not resolved then checked out by commit')
    for pin in ("GO_VERSION: '1.27.1'","NDK_VERSION: '28.2.13676358'",'runs-on: ubuntu-24.04'):
        require(pin in workflow,f'reproducible CI pin missing: {pin}')
    action_pins = {
        'actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1': 'checkout v7.0.1',
        'actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e': 'setup-go v7.0.0',
        'actions/upload-artifact@bbbca2ddaa5d8feaa63e36b76fdaad77386f024f': 'upload-artifact v7.0.0',
    }
    for pin, label in action_pins.items(): require(pin in workflow, f'CI action is not immutable: {label}')
    require('actions/checkout@v7' not in workflow and 'actions/setup-go@v7' not in workflow and 'actions/upload-artifact@v7' not in workflow,'mutable CI action tags remain')
    for token in ('ci_runner_os','ci_runner_arch','ci_image_os','ci_image_version'):
        require(token in builder and token in bundle, f'CI environment provenance missing: {token}')
    require('VerifyBundle' in bundle and 'ImportBundle' in bundle and 'PersistBuildResolution' in source,'build output does not feed canonical source/runtime pipeline')
    require('verify-build' in cli and 'import-build' in cli,'production CLI source-build ingress missing')
    require('/system/bin/linker64' in bundle and 'elf.EM_AARCH64' in bundle,'actual Android arm64 ELF identity is not verified')
    for token in ('TestUnpinnedMutableResultRejected','TestPartialBundleRejected','TestManifestHashMismatchRejected','TestWrongABIRejectedBeforeELF','TestLinuxARM64ClaimingAndroidRejectedByInterpreter'):
        require(token in tests,f'SOURCE-X02 negative regression missing: {token}')
    require('TestPersistBuildResolutionBindsRegisteredSourceAndBytes' in source_tests,'source-build resolution/store handoff regression missing')
    require('position >= 5' in x01 and 'position == 5' in x01,'SOURCE-X01 gate is not progression-safe')
    require('[wrapper.commands.source-x02]' in devtool and '[targets.rclone_nexus.jobs.source-x02-audit]' in devtool and 'source-x02 = [' in devtool,'Devtool SOURCE-X02 wrapper/job/workflow missing')
    required=set(policy.get('required_ancestor_nodes',[])); require('source-x02-audit' in required,'final seal policy not bound to SOURCE-X02')
    for flow in ('grand-g1-source = [','release = ['):
        start=devtool.index(flow); end=devtool.find('\n]',start); body=devtool[start:end]
        require('source-x02-audit' in body,f'{flow.split()[0]} bypasses SOURCE-X02')
        require(body.index('source-x01-audit') < body.index('source-x02-audit') < body.index('webui-final-contract'),f'{flow.split()[0]} SOURCE ordering malformed')
    require('No bclone release/version string is hardcoded' in docs,'dynamic bclone policy is not documented')

def android_elf()->bytes:
    interp=b'/system/bin/linker64\0'; phoff=64; off=64+56
    ident=b'\x7fELF'+bytes([2,1,1,0])+bytes(8)
    hdr=struct.pack('<HHIQQQIHHHHHH',2,183,1,0,phoff,0,0,64,56,1,64,0,0)
    ph=struct.pack('<IIQQQQQQ',3,4,off,0,0,len(interp),len(interp),1)
    return ident+hdr+ph+interp

def assert_production_cli()->None:
    with tempfile.TemporaryDirectory(prefix='rnx-source-x02-') as raw:
        t=Path(raw); racctl=t/'racctl'; subprocess.run(['go','build','-o',str(racctl),'./cmd/racctl'],cwd=ROOT,check=True)
        bundle=t/'bundle'; bundle.mkdir(); binary=bundle/'bclone-android-arm64'; binary.write_bytes(android_elf()); binary.chmod(0o755)
        bh=hashlib.sha256(binary.read_bytes()).hexdigest()
        manifest={
            'schema_version':1,'state':'complete','source_id':'bclone','engine':'bclone','repository':'BenjiThatFoxGuy/bclone',
            'requested_ref':'v-fixture','resolved_commit':'a'*40,'go_version':'go1.27.1','ndk_version':'28.2.13676358',
            'compiler':'/ndk/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang','compiler_version':'clang 19 fixture',
            'api_level':21,'goos':'android','goarch':'arm64','abi':'arm64-v8a','cgo_enabled':True,'tags':['android'],'trimpath':True,
            'build_flags':['-v','-tags','android','-trimpath'],'ldflags':['-s','-w'],'binary_name':binary.name,'binary_sha256':bh,'binary_size':binary.stat().st_size,'produced_unix_ms':1,
        }
        prov=bundle/'provenance.json'; prov.write_text(json.dumps(manifest,indent=2,sort_keys=True)+'\n')
        ph=hashlib.sha256(prov.read_bytes()).hexdigest(); (bundle/'SHA256SUMS').write_text(f'{bh}  {binary.name}\n{ph}  provenance.json\n')
        cp=subprocess.run([str(racctl),'runtime','source','verify-build',str(bundle)],cwd=ROOT,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        require(cp.returncode==0,f'compiled verify-build failed: {cp.stderr}')
        out=json.loads(cp.stdout); require(out['manifest']['resolved_commit']=='a'*40 and out['manifest']['abi']=='arm64-v8a','compiled verifier lost immutable/ABI provenance')
        (bundle/'provenance.json').write_text(prov.read_text()+' ')
        bad=subprocess.run([str(racctl),'runtime','source','verify-build',str(bundle)],cwd=ROOT,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        require(bad.returncode!=0 and 'provenance manifest SHA-256 mismatch' in bad.stderr,'compiled verifier accepted provenance tamper')

def main()->int:
    assert_scope(); assert_architecture()
    run(['go','test','./internal/runtimebuild','./internal/runtimeacquire','./internal/runtimesource','./internal/runtimestore','./cmd/racctl'])
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    run([sys.executable,'scripts/dev/source_x01_gate.py'])
    assert_production_cli()
    print('SOURCE-X02 Android source-build automation: PASS')
    print(json.dumps({'promise_range':'RNX-P391..RNX-P403','count':13,'production_adopted':13,'blocked_by_environment':0,'status':ADOPTED,'next':'UPDATE-X01'},sort_keys=True))
    return 0
if __name__=='__main__': raise SystemExit(main())
