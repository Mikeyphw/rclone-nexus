#!/usr/bin/env python3
from __future__ import annotations
import argparse, importlib.util, json, subprocess, sys
from pathlib import Path
import tomllib

ROOT=Path(__file__).resolve().parents[2]
LEDGER=ROOT/'release/canonical-promise-ledger.json'
POLICY=ROOT/'release/final-seal-policy.json'
ADOPTED='IMPLEMENTED_AND_PRODUCTION_ADOPTED'

def fail(msg:str)->None: print(f'ERROR: {msg}',file=sys.stderr); raise SystemExit(1)
def require(c:bool,msg:str)->None:
    if not c: fail(msg)
def read(rel:str)->str: return (ROOT/rel).read_text(encoding='utf-8')
def run(argv:list[str])->None:
    print('$ '+' '.join(argv)); subprocess.run(argv,cwd=ROOT,check=True)

def evidence_resolves(ref:str)->bool:
    ref=ref.strip()
    if '::' in ref:
        p,n=ref.split('::',1); q=ROOT/p
        return q.is_file() and bool(n.strip()) and n in q.read_text(encoding='utf-8',errors='replace')
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
    for n in list(range(374,435))+[501,502]:
        pid=f'RNX-P{n:03d}'; item=by.get(pid); require(isinstance(item,dict),f'missing SOURCE/UPDATE promise {pid}')
        require(item.get('status')==ADOPTED,f'{pid} status={item.get("status")} want {ADOPTED}')
        refs=item.get('evidence') or []
        require('source-g1-audit' in refs,f'{pid} is not sealed by SOURCE-G1 behavioral gate')
        for ref in refs: require(evidence_resolves(str(ref)),f'{pid} evidence does not resolve: {ref}')
    active=data.get('active_position',{}); position=int(active.get('position') or 0)
    require(position>=8,'canonical active position regressed before SOURCE-G1')
    if position==8:
        require(active.get('name')=='SOURCE-G1','position 8 active name is not SOURCE-G1')
        require(active.get('promise_range')=='RNX-P374..RNX-P434 + RNX-P501..RNX-P502','SOURCE-G1 cumulative promise range stale')
        require(active.get('production_adopted_count')==63 and active.get('blocked_by_environment_count')==0,'SOURCE-G1 cumulative status counts stale')

def assert_architecture()->None:
    src=read('internal/runtimesource/source.go'); store=read('internal/runtimestore/store.go'); update=read('internal/runtimeupdate/update.go')
    acquire=read('internal/runtimeacquire/acquire.go')
    activation=read('internal/runtimeactivation/activation.go'); build=read('internal/runtimebuild/bundle.go'); cli=read('cmd/racctl/main.go')
    workflow=read('.github/workflows/runtime-source-build.yml'); devtool=read('.devtool.toml'); cleanup=read('scripts/dev/cleanup_validation_outputs.py')
    release=read('scripts/dev/release_artifacts.py'); policy=json.loads(POLICY.read_text())
    require('AssetName: "magisk-rclone_arm64-v8a.zip"' in src,'NewFuture builtin is still metadata-only / has no importable Android asset')
    for token in ('RepositoryID','ReleaseID','CommitSHA','*Asset','ResolutionID'):
        require(token in src,f'immutable source resolution authority missing {token}')
    require('runtime archive path escapes archive root' in store and 'runtime archive contains symlink' in store and 'ExpectedSHA256' in store,'archive/hash fail-closed boundary incomplete')
    require('runtimeacquire.AcquireResolution' in update and 'runtimestore.Test' in update and 'runtimeactivation.Stage' in update and 'ActivateStaged' in update and 'Rollback' in update,'update manager bypasses canonical acquire/qualify/stage/activation pipeline')
    for token in ('requiresPublishedBuild', 'runtimebuild.AcquirePublished', 'runtimesource.AcquireResolution', 'func AcquireResolution', 'func ImportResolution', 'runtimestore.Test'):
        require(token in acquire,f'canonical runtime acquisition bridge missing {token}')
    require('elfAndroidArm64' in build and '/system/bin/linker64' in build,'SOURCE-X02 verifier does not prove Android arm64 ELF identity')
    require('workflow_dispatch:' in workflow and 'schedule:' in workflow and 'resolved_commit' in workflow and 'runtime source verify-build' in workflow,'SOURCE-X02 real CI automation topology incomplete')
    for token in ('runtime source resolve','runtime update check','runtime update activate','runtime update rollback'):
        require(token in cli or token.replace(' ','') in ''.join(cli.split()),f'production CLI ingress missing {token}')
    require('release/evidence/source-g1-supply-chain-qualification.json' in cleanup,'SOURCE-G1 private evidence is not validation-cleaned')
    require('release/evidence/source-g1-supply-chain-qualification.json' in release,'SOURCE-G1 private evidence is not excluded from release source identity')
    for token in ('[wrapper.commands.source-g1]','[targets.rclone_nexus.jobs.source-g1-source-audit]','[targets.rclone_nexus.jobs.source-g1-device-evidence]','[targets.rclone_nexus.jobs.source-g1-audit]','[targets.rclone_nexus.jobs.source-g1-validation-cleanup]','source-g1 = ['):
        require(token in devtool,f'Devtool SOURCE-G1 lifecycle missing {token}')
    required=set(policy.get('required_ancestor_nodes',[])); require('source-g1-source-audit' in required and 'source-g1-audit' in required,'final seal policy is not SOURCE-G1-bound')
    start=devtool.index('grand-g1-source = ['); end=devtool.find('\n]',start); body=devtool[start:end]
    require(body.index('update-x01-audit') < body.index('source-g1-source-audit') < body.index('webui-final-contract'),'grand-g1-source bypasses SOURCE-G1 source audit')
    start=devtool.index('release = ['); end=devtool.find('\n]',start); body=devtool[start:end]
    for token in ('source-g1-source-audit','source-g1-device-evidence','source-g1-audit','source-g1-validation-cleanup'):
        require(token in body,f'release bypasses SOURCE-G1 lifecycle node {token}')
    require(body.index('update-x01-audit') < body.index('source-g1-source-audit') < body.index('source-g1-device-evidence') < body.index('source-g1-audit') < body.index('source-g1-validation-cleanup') < body.index('webui-final-contract'),'release SOURCE-G1 ordering malformed')

def assert_no_static_only_seal()->None:
    gate=read('scripts/dev/source_g1_device.py')
    # These are concrete behavior checkpoints. Removing any one invalidates the
    # milestone even if the corresponding model/config still exists.
    for token in (
        "runtime','source','resolve',sid", "runtime','update','check','newfuture", "runtime','update','activate", "runtime','update','rollback",
        'import_historical_newfuture_baseline', "'pinned-release'", "baseline_source",
        'proc_hash(pidb,state)', 'archive_sha != asset_digest', "run_negative(binary,env,'g1-wrong-hash'", "negatives['offline_source']", 'real_builder_probe(find_ndk(),tmp)', 'compiler probe exited',
    ):
        require(token in gate,f'SOURCE-G1 behavioral proof checkpoint missing: {token}')
    require('Simulated GitHub/provider labels are not enough' in read('docs/campaign/RUNTIME_STANDALONE_ROADMAP.md'),'canonical SOURCE-G1 behavioral requirement disappeared')



def assert_position8_gate_promises_are_behavioral()->None:
    gate=read('scripts/dev/source_g1_device.py')
    tests=read('tests/test_source_g1_supply_chain.py')
    # RNX-P426..P434 are promises of this gate itself. They may only be sealed
    # by concrete rooted/network execution plus evidence verification, never by
    # static presence. These checkpoints correspond one-for-one with the nine
    # canonical "Must exercise" obligations.
    checkpoints={
        426: ('resolve real external bclone, official rclone and latest NewFuture sources', 'test_physical_resolution_snapshots_bind_persisted_authority'),
        427: ('exercise latest NewFuture resolution -> download -> hash/archive -> qualification -> stage', 'test_runnable_ndk_compiler_makes_real_build_supported'),
        428: ('archive_sha != asset_digest', 'test_all_adversarial_cases_are_mandatory'),
        429: ('process_sha256', 'test_activation_must_execute_staged_bytes'),
        430: ('select, import and qualify a real historical NewFuture baseline A', 'test_historical_baseline_is_resolved_and_imported_through_production_source_path'),
        431: ('real external update did not produce distinct staged runtime bytes', 'test_activation_must_execute_staged_bytes'),
        432: ('one-click rollback and prove prior executable bytes restored', 'test_rollback_must_restore_prior_bytes'),
        433: ('exercise adversarial acquisition failures through production update path', 'test_all_adversarial_cases_are_mandatory'),
        434: ('snapshot durable source/update authority evidence', 'test_physical_resolution_snapshots_bind_persisted_authority'),
    }
    for promise,(device_token,test_token) in checkpoints.items():
        require(device_token in gate,f'RNX-P{promise:03d} behavioral device checkpoint missing: {device_token}')
        require(test_token in tests,f'RNX-P{promise:03d} behavioral evidence regression missing: {test_token}')


def load_gate(name:str, rel:str):
    path=ROOT/rel
    spec=importlib.util.spec_from_file_location(name,path)
    require(spec is not None and spec.loader is not None,f'cannot load inherited gate {rel}')
    module=importlib.util.module_from_spec(spec)
    sys.modules[name]=module
    spec.loader.exec_module(module)
    return module

def assert_inherited_boundaries()->None:
    # SOURCE-G1 audits only the implementation window since the already-sealed
    # RUNTIME-G1 milestone. Import predecessor assertion functions directly so
    # their invariants and compiled CLI probes run once, without recursively
    # invoking every historical gate main and test suite. Broad tests run once
    # below and the rooted/network phase supplies the milestone behavior proof.
    g1=load_gate('rnexus_g1_source_boundary','scripts/dev/runtime_standalone_g1_gate.py')
    x01=load_gate('rnexus_source_x01_boundary','scripts/dev/source_x01_gate.py')
    x02=load_gate('rnexus_source_x02_boundary','scripts/dev/source_x02_gate.py')
    upd=load_gate('rnexus_update_x01_boundary','scripts/dev/update_x01_gate.py')
    g1.assert_scope(); g1.assert_architecture()
    x01.assert_scope(); x01.assert_architecture(); x01.assert_production_cli()
    x02.assert_scope(); x02.assert_architecture(); x02.assert_production_cli()
    upd.assert_scope(); upd.assert_architecture(); upd.assert_cli()

def assert_behavioral_regression_matrix()->None:
    # SOURCE-G1 seals behavior, not symbols. These named integration/regression
    # cases are mapped to the canonical promise failures they prove and the
    # package tests below execute them. A removed behavioral case therefore
    # invalidates the milestone even if the production type/function remains.
    files={
        'source': read('internal/runtimesource/source_test.go'),
        'build': read('internal/runtimebuild/bundle_test.go'),
        'store': read('internal/runtimestore/store_test.go'),
        'update': read('internal/runtimeupdate/update_test.go'),
        'activation': read('internal/runtimeactivation/activation_test.go'),
    }
    required={
        # RNX-P374..P390: real source classes/channels and resolver adversaries.
        'source': (
            'TestBuiltinsCoverCanonicalFirstClassGitHubSources',
            'TestCustomRegistryCoversGitHubURLLocalAndSourceBuild',
            'TestLatestStableResolvesToImmutableCommitAndBuildAuthorityAndPersists',
            'TestPinnedReleaseRetargetDoesNotRewritePriorResolution',
            'TestNegativeGitHubCasesFailClosed',
            'TestPoisonedMetadataRedirectIsRejected',
            'TestPinnedCommitRequiresFullImmutableSHA',
            'TestManualLocalURLAndSourceBuildResolutionsBindBytes',
            'TestImportRequestUsesImmutableAssetAPIURLNotLatestOrTagURL',
        ),
        # RNX-P391..P403: reproducible build provenance and actual ELF identity.
        'build': (
            'TestUnpinnedMutableResultRejected',
            'TestPartialBundleRejected',
            'TestManifestHashMismatchRejected',
            'TestWrongABIRejectedBeforeELF',
            'TestLinuxARM64ClaimingAndroidRejectedByInterpreter',
            'TestSymlinkBundleMemberRejected',
        ),
        # RNX-P404..P425: fail-closed acquisition/update/rollback behavior.
        'store': (
            'TestImportExpectedSHA256FailsBeforeCandidatePublication',
            'TestZipReleaseAssetExtractsExecutableAndBindsArchiveAndBinaryDigests',
            'TestZipReleaseRejectsTraversalSymlinkAndMalformedArchives',
            'TestGarbageCollectPreservesProtectedAndBoundsUnprotectedHistory',
            'TestInterruptedDownloadPublishesNoCandidate',
        ),
        'update': (
            'TestDefaultPolicyMatchesSafePersonalUpdateDefaults',
            'TestBootActivationClearsDisappearedStagedCandidateAndFailsClosed',
            'TestOfflineUpdateCheckIsRetryableAndDoesNotPublishActivationState',
            'TestSanitizeErrorRemovesURLCredentialsAndQuery',
            'TestDisappearedSourceFailsClosedWithoutChangingActivationState',
        ),
        'activation': (
            'TestActivationRollbackWhenCandidateDisappearsAfterStaging',
            'TestActivationRollbackOnStartupContractFailure',
            'TestProjectionRenameDoesNotOverwriteBytesUnderOpenProcess',
        ),
    }
    for area,names in required.items():
        text=files[area]
        for name in names:
            require(('func '+name+'(') in text,f'SOURCE-G1 behavioral regression missing for {area}: {name}')

def source_only()->None:
    assert_scope(); assert_architecture(); assert_no_static_only_seal(); assert_position8_gate_promises_are_behavioral(); assert_behavioral_regression_matrix()
    assert_inherited_boundaries()
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    run(['go','test','./internal/runtimesource','./internal/runtimebuild','./internal/runtimestore','./internal/runtimeupdate','./internal/runtimeactivation','./internal/daemon','./internal/control','./cmd/racctl'])
    run(['sh','-n','module/service.sh'])
    run(['node','scripts/dev/check_webui_js.mjs'])
    run([sys.executable,'-m','unittest','-v','tests.test_source_g1_supply_chain'])

def main()->int:
    ap=argparse.ArgumentParser(); ap.add_argument('--source-only',action='store_true'); ap.add_argument('--evidence',default='release/evidence/source-g1-supply-chain-qualification.json'); a=ap.parse_args()
    source_only()
    if not a.source_only:
        run([sys.executable,'scripts/dev/source_g1_device.py','verify','--evidence',a.evidence])
    print('SOURCE-G1 source/update supply-chain authority: PASS')
    print(json.dumps({'promise_range':'RNX-P374..RNX-P434 + RNX-P501..RNX-P502','count':63,'production_adopted':63,'blocked_by_environment':0,'status':ADOPTED,'proof':'behavioral-production-adoption','next':'MIGRATE-X01'},sort_keys=True))
    return 0
if __name__=='__main__': raise SystemExit(main())
