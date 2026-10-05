from __future__ import annotations
import importlib.util, json, os, sys, tempfile, unittest
from types import SimpleNamespace
from unittest import mock
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('source_g1_device',ROOT/'scripts/dev/source_g1_device.py')
mod=importlib.util.module_from_spec(spec); assert spec and spec.loader; sys.modules[spec.name]=mod; spec.loader.exec_module(mod)

def resolution(source,repo,asset):
    return {'schema_version':1,'resolution_id':source+'-'+'a'*64,'source_id':source,'spec_digest':'b'*64,'registry_revision':1,'engine':'rclone' if source!='bclone' else 'bclone','kind':'newfuture-derived' if source=='newfuture' else 'github-release','channel':'latest-stable','repository':repo,'repository_id':10,'requested_ref':'latest-stable','release_id':20,'release_tag':'v1','commit_sha':'c'*40,'asset':{'id':30,'name':asset,'api_url':f'https://api.github.com/repos/{repo}/releases/assets/30','browser_download_url':f'https://github.com/{repo}/releases/download/v1/{asset}','digest':'sha256:'+'d'*64,'size':123},'resolved_unix_ms':1}

def base():
    a='1'*64; b='2'*64
    d={'schema_version':1,'harness_version':mod.HARNESS_VERSION,'status':'PASS','captured_at':'now','session':'source-g1-test','source_bindings':mod.source_bindings(),'device':{'sdk':'36','abi':'arm64-v8a','fingerprint_sha256':'f'*64},'sources':{
        'bclone':resolution('bclone','BenjiThatFoxGuy/bclone','rclone-v1-linux-arm64.zip'),
        'rclone':resolution('rclone','rclone/rclone','rclone-v1-linux-arm64.zip'),
        'newfuture':resolution('newfuture','NewFuture/rclone-fuse3-magisk','magisk-rclone_arm64-v8a.zip')},
        'baseline_source':dict(resolution('newfuture','NewFuture/rclone-fuse3-magisk','magisk-rclone_arm64-v8a.zip'), resolution_id='newfuture-'+'e'*64, channel='pinned-release', requested_ref='v0', release_id=19, release_tag='v0', commit_sha='e'*40, asset={'id':29,'name':'magisk-rclone_arm64-v8a.zip','api_url':'https://api.github.com/repos/NewFuture/rclone-fuse3-magisk/releases/assets/29','browser_download_url':'https://github.com/NewFuture/rclone-fuse3-magisk/releases/download/v0/magisk-rclone_arm64-v8a.zip','digest':'sha256:'+'e'*64,'size':120}),
        'source_build':{'supported':False,'reason':'no NDK'},
        'fuse_helper':{'repository':'NewFuture/rclone-fuse3-magisk','release_tag':'v1','asset_id':30,'asset_name':'magisk-rclone_arm64-v8a.zip','archive_sha256':'a'*64,'helper_sha256':'b'*64},
        'flow':{'baseline':{'runtime_id':'a','binary_sha256':a,'mount_pid':10,'process_sha256':a,'resolution_id':'newfuture-'+'e'*64,'release_tag':'v0'},'staged':{'runtime_id':'b','binary_sha256':b,'archive_sha256':'d'*64,'resolution_id':'newfuture-'+'a'*64},'activated':{'runtime_id':'b','mount_pid':11,'process_sha256':b},'rollback':{'runtime_id':'a','mount_pid':12,'process_sha256':a}},
        'negatives':{k:{'returncode':1,'last_result':'candidate-failed','retryable':k=='offline_source','error_redacted':True} for k in ('hash_mismatch','malformed','traversal','symlink','offline_source')},
        'references':[{'kind':'x','path':f'/private/{i}','sha256':str(i%10)*64} for i in range(7)]}
    d['document_sha256']=mod.digest({k:v for k,v in d.items() if k!='document_sha256'}); return d

def write(d):
    p=Path(tempfile.mkdtemp())/'evidence.json'; p.write_text(json.dumps(d)); return p

class SourceG1EvidenceTests(unittest.TestCase):
    def _ndk_fixture(self, root: Path, host: str = 'linux-x86_64') -> Path:
        ndk=root/'29.0.14206865'
        compiler=ndk/'toolchains/llvm/prebuilt'/host/'bin/aarch64-linux-android21-clang'
        compiler.parent.mkdir(parents=True)
        compiler.write_text('#!/bin/sh\nexit 0\n')
        compiler.chmod(0o755)
        (ndk/'source.properties').write_text('Pkg.Revision = 29.0.14206865\n')
        return ndk


    def test_resolution_state_path_matches_production_paths_layout(self):
        rid='src-'+'a'*32
        got=mod.resolution_state_path('/state',rid)
        self.assertEqual(got,'/state/runtime/sources/resolutions/'+rid+'.json')
        self.assertNotIn('/runtime-sources/',got)

    def test_resolution_state_path_rejects_noncanonical_id(self):
        with self.assertRaisesRegex(RuntimeError,'invalid SOURCE-G1 resolution ID'):
            mod.resolution_state_path('/state','newfuture-'+'a'*64)

    def test_ndk_present_but_unrunnable_host_prebuilt_is_not_supported(self):
        with tempfile.TemporaryDirectory() as td:
            ndk=self._ndk_fixture(Path(td))
            failed=SimpleNamespace(returncode=255, stdout='', stderr="qemu-x86_64: Could not open '/lib64/ld-linux-x86-64.so.2': No such file or directory\n")
            with mock.patch.dict(os.environ, {'RNEXUS_SOURCE_G1_NDK':str(ndk),'ANDROID_NDK_HOME':'','ANDROID_NDK_ROOT':'','ANDROID_HOME':'','ANDROID_SDK_ROOT':'','RNEXUS_SOURCE_G1_NATIVE_CLANG':''}, clear=False), \
                 mock.patch.object(mod.shutil,'which',return_value=None), mock.patch.object(mod.subprocess,'run',return_value=failed) as run:
                probe=mod.find_ndk()
            self.assertFalse(probe['supported'])
            self.assertTrue(probe['present'])
            self.assertIn('installed but not runnable',probe['reason'])
            self.assertIn('qemu-x86_64',probe['reason'])
            self.assertEqual(run.call_args.args[0][-1],'--version')

    def test_runnable_ndk_compiler_makes_real_build_supported(self):
        with tempfile.TemporaryDirectory() as td:
            ndk=self._ndk_fixture(Path(td),host='linux-aarch64')
            ok=SimpleNamespace(returncode=0, stdout='Android clang version 20.0.0\n', stderr='')
            with mock.patch.dict(os.environ, {'RNEXUS_SOURCE_G1_NDK':str(ndk),'ANDROID_NDK_HOME':'','ANDROID_NDK_ROOT':'','ANDROID_HOME':'','ANDROID_SDK_ROOT':''}, clear=False), \
                 mock.patch.object(mod.subprocess,'run',return_value=ok):
                probe=mod.find_ndk()
            self.assertTrue(probe['supported'])
            self.assertEqual(probe['host'],'linux-aarch64')
            self.assertEqual(probe['version'],'29.0.14206865')
            self.assertIn('Android clang version',probe['compiler_banner'])
            self.assertEqual(probe['compiler_mode'],'ndk-prebuilt')
            self.assertEqual(probe['compiler_target'],'aarch64-linux-android21')


    def test_termux_native_clang_can_drive_pinned_ndk_sysroot_when_host_prebuilt_is_unrunnable(self):
        with tempfile.TemporaryDirectory() as td:
            ndk=self._ndk_fixture(Path(td))
            (ndk/'toolchains/llvm/prebuilt/linux-x86_64/sysroot').mkdir(parents=True)
            failed=SimpleNamespace(returncode=255, stdout='', stderr="qemu-x86_64: missing loader")
            native={'supported':True,'compiler':'/data/data/com.termux/files/usr/bin/clang','compiler_banner':'clang 22','compiler_mode':'native-clang-ndk-sysroot','compiler_target':'aarch64-linux-android21','sysroot':str(ndk/'toolchains/llvm/prebuilt/linux-x86_64/sysroot'),'compiler_resource_dir':str(ndk/'toolchains/llvm/prebuilt/linux-x86_64/lib/clang/19'),'compiler_rt_builtins_sha256':'a'*64,'compiler_libunwind_sha256':'b'*64}
            with mock.patch.dict(os.environ, {'RNEXUS_SOURCE_G1_NDK':str(ndk),'ANDROID_NDK_HOME':'','ANDROID_NDK_ROOT':'','ANDROID_HOME':'','ANDROID_SDK_ROOT':'','RNEXUS_SOURCE_G1_NATIVE_CLANG':''}, clear=False), \
                 mock.patch.object(mod.shutil,'which',return_value='/data/data/com.termux/files/usr/bin/clang'), \
                 mock.patch.object(mod.subprocess,'run',return_value=failed), \
                 mock.patch.object(mod,'_probe_native_clang',return_value=native) as probe_native:
                probe=mod.find_ndk()
            self.assertTrue(probe['supported'])
            self.assertEqual(probe['compiler_mode'],'native-clang-ndk-sysroot')
            self.assertEqual(probe['host'],'linux-x86_64')
            self.assertEqual(probe['version'],'29.0.14206865')
            probe_native.assert_called_once()

    def test_native_clang_probe_binds_ndk_resource_dir_for_android_runtimes(self):
        with tempfile.TemporaryDirectory() as td:
            ndk=Path(td)/'ndk'; host=ndk/'toolchains/llvm/prebuilt/linux-x86_64'
            (host/'sysroot').mkdir(parents=True)
            resource=host/'lib/clang/19'; runtime=resource/'lib/linux'; (runtime/'aarch64').mkdir(parents=True)
            (runtime/'libclang_rt.builtins-aarch64-android.a').write_bytes(b'builtins')
            (runtime/'aarch64/libunwind.a').write_bytes(b'unwind')
            calls=[]
            def fake_run(argv, **kwargs):
                calls.append(argv)
                if argv[1:] == ['--version']:
                    return SimpleNamespace(returncode=0, stdout='clang 22\n', stderr='')
                return SimpleNamespace(returncode=0, stdout='', stderr='')
            with mock.patch.object(mod.subprocess,'run',side_effect=fake_run), mock.patch.object(mod,'_android_arm64_linked_elf',return_value=True):
                probe=mod._probe_native_clang(ndk,host,'/data/data/com.termux/files/usr/bin/clang')
            self.assertTrue(probe['supported'])
            self.assertEqual(probe['compiler_resource_dir'],str(resource))
            link=calls[-1]
            self.assertIn(f'-resource-dir={resource}',link)
            self.assertIn(f'--sysroot={host / "sysroot"}',link)
            self.assertEqual(len(probe['compiler_rt_builtins_sha256']),64)
            self.assertEqual(len(probe['compiler_libunwind_sha256']),64)

    def test_unrunnable_ndk_probe_is_recorded_without_invoking_builder(self):
        probe={'supported':False,'present':True,'reason':'Android NDK aarch64 compiler is installed but not runnable in this validation environment: linux-x86_64: compiler probe exited 255'}
        with tempfile.TemporaryDirectory() as td, mock.patch.object(mod.subprocess,'run') as run:
            result=mod.real_builder_probe(probe,Path(td))
        self.assertFalse(result['supported'])
        self.assertTrue(result['compiler_present'])
        self.assertIn('not runnable',result['reason'])
        run.assert_not_called()
    def test_mount_fixture_config_matches_runtimeg1_remote(self):
        text=mod.mount_fixture_config()
        self.assertIn('[runtimeg1]', text)
        self.assertNotIn('[sourceg1]', text)
        self.assertIn('type = local', text)

    def test_mount_fixture_preflight_uses_active_runtime_and_proves_entry(self):
        with mock.patch.object(mod, 'racctl', return_value=SimpleNamespace(stdout='/state/runtimes/a/rclone\n')), \
             mock.patch.object(mod.g1, 'root_run', return_value=SimpleNamespace(returncode=0, stdout='proof.txt\n', stderr='')) as root_run:
            mod.preflight_mount_fixture('/module/racctl', {'RNEXUS_STATE_DIR':'/state'}, '/source')
        argv=root_run.call_args.args[0]
        self.assertEqual(argv[0], '/state/runtimes/a/rclone')
        self.assertIn('runtimeg1:/source', argv)
        self.assertIn('/state/config/rclone/rclone.conf', argv)

    def test_mount_fixture_preflight_rejects_missing_remote(self):
        with mock.patch.object(mod, 'racctl', return_value=SimpleNamespace(stdout='/state/runtimes/a/rclone\n')), \
             mock.patch.object(mod.g1, 'root_run', return_value=SimpleNamespace(returncode=1, stdout='', stderr="didn't find section in config file ('runtimeg1')")):
            with self.assertRaisesRegex(RuntimeError, 'fixture preflight failed'):
                mod.preflight_mount_fixture('/module/racctl', {'RNEXUS_STATE_DIR':'/state'}, '/source')
    def test_historical_baseline_is_resolved_and_imported_through_production_source_path(self):
        latest=resolution('newfuture','NewFuture/rclone-fuse3-magisk','magisk-rclone_arm64-v8a.zip')
        historical=dict(latest, resolution_id='newfuture-'+'e'*64, channel='pinned-release', requested_ref='v0', release_id=19, release_tag='v0', commit_sha='e'*40, asset={'id':29,'name':'magisk-rclone_arm64-v8a.zip','api_url':'https://api.github.com/repos/NewFuture/rclone-fuse3-magisk/releases/assets/29','browser_download_url':'https://github.com/NewFuture/rclone-fuse3-magisk/releases/download/v0/magisk-rclone_arm64-v8a.zip','digest':'sha256:'+'e'*64,'size':120})
        manifest={'runtime_id':'runtime-old','binary_sha256':'1'*64,'qualification':{'qualified':True}}
        responses=[SimpleNamespace(returncode=0,stdout=json.dumps(historical),stderr=''),SimpleNamespace(returncode=0,stdout=json.dumps(manifest),stderr='')]
        with mock.patch.object(mod,'github_release_candidates',return_value=['v0']), mock.patch.object(mod,'racctl',side_effect=responses) as call:
            got_resolution,got_manifest=mod.import_historical_newfuture_baseline('/racctl',{},latest)
        self.assertEqual(got_resolution['release_tag'],'v0'); self.assertEqual(got_manifest['runtime_id'],'runtime-old')
        resolve_args=call.call_args_list[0].args[2]
        self.assertEqual(resolve_args[:4],['runtime','source','resolve','newfuture'])
        self.assertIn('pinned-release',resolve_args); self.assertIn('v0',resolve_args)
        self.assertEqual(call.call_args_list[1].args[2],['runtime','source','import-resolution',historical['resolution_id']])

    def test_historical_baseline_must_not_equal_latest_release(self):
        d=base(); d['baseline_source']['release_tag']=d['sources']['newfuture']['release_tag']; d['baseline_source']['release_id']=d['sources']['newfuture']['release_id']; d['document_sha256']=mod.digest({k:v for k,v in d.items() if k!='document_sha256'})
        with self.assertRaisesRegex(RuntimeError,'historical baseline is not distinct'): mod.verify(write(d),physical=False)

    def test_structural_behavioral_evidence_passes(self): mod.verify(write(base()),physical=False)
    def test_document_tamper_rejected(self):
        d=base(); d['flow']['rollback']['runtime_id']='evil'
        with self.assertRaisesRegex(RuntimeError,'document digest'): mod.verify(write(d),physical=False)
    def test_activation_must_execute_staged_bytes(self):
        d=base(); d['flow']['activated']['process_sha256']='9'*64; d['document_sha256']=mod.digest({k:v for k,v in d.items() if k!='document_sha256'})
        with self.assertRaisesRegex(RuntimeError,'activation did not execute staged bytes'): mod.verify(write(d),physical=False)
    def test_rollback_must_restore_prior_bytes(self):
        d=base(); d['flow']['rollback']['process_sha256']='9'*64; d['document_sha256']=mod.digest({k:v for k,v in d.items() if k!='document_sha256'})
        with self.assertRaisesRegex(RuntimeError,'rollback did not restore'): mod.verify(write(d),physical=False)
    def test_newfuture_requires_real_android_asset(self):
        d=base(); d['sources']['newfuture']['asset']['name']='update-arm64-v8a.json'; d['document_sha256']=mod.digest({k:v for k,v in d.items() if k!='document_sha256'})
        with self.assertRaisesRegex(RuntimeError,'NewFuture Android asset'): mod.verify(write(d),physical=False)
    def test_offline_failure_must_be_retryable(self):
        d=base(); d['negatives']['offline_source']['retryable']=False; d['document_sha256']=mod.digest({k:v for k,v in d.items() if k!='document_sha256'})
        with self.assertRaisesRegex(RuntimeError,'offline failure not retryable'): mod.verify(write(d),physical=False)
    def test_all_adversarial_cases_are_mandatory(self):
        d=base(); del d['negatives']['symlink']; d['document_sha256']=mod.digest({k:v for k,v in d.items() if k!='document_sha256'})
        with self.assertRaisesRegex(RuntimeError,'negative proof missing'): mod.verify(write(d),physical=False)
    def test_private_evidence_is_gitignored(self):
        text=(ROOT/'.gitignore').read_text(); self.assertIn('release/evidence/source-g1-supply-chain-qualification.json',text)

    def _physical_fixture(self):
        d=base(); refs=[]; texts={}
        for sid,source in d['sources'].items():
            path=f'/private/{sid}-resolution.json'; sha=(sid[0] if sid[0].isdigit() else format((ord(sid[0])%10),'x'))*64
            refs.append({'kind':'source-resolution','path':path,'sha256':sha,'source_path':f'/live/{sid}.json'})
            texts[path]=json.dumps(source)
        path='/private/newfuture-baseline-resolution.json'; sha='e'*64
        refs.append({'kind':'source-resolution-baseline','path':path,'sha256':sha,'source_path':'/live/newfuture-baseline.json'})
        texts[path]=json.dumps(d['baseline_source'])
        helper=d['fuse_helper']
        helper_path='/private/fusermount3'; helper_manifest_path='/private/fusermount3-manifest.json'
        refs.append({'kind':'fuse-helper','path':helper_path,'sha256':helper['helper_sha256']})
        refs.append({'kind':'fuse-helper-manifest','path':helper_manifest_path,'sha256':'c'*64})
        texts[helper_manifest_path]=json.dumps({**helper,'helper_sha256':helper['helper_sha256']})
        for i in range(4):
            refs.append({'kind':'runtime-binary','path':f'/private/bin-{i}','sha256':str(i+3)*64})
        d['references']=refs
        d['document_sha256']=mod.digest({k:v for k,v in d.items() if k!='document_sha256'})
        hashes={r['path']:r['sha256'] for r in refs}
        return d,texts,hashes

    def test_physical_resolution_snapshots_bind_persisted_authority(self):
        d,texts,hashes=self._physical_fixture()
        with mock.patch.object(mod.g1,'root_hash',side_effect=lambda p: hashes[p]), mock.patch.object(mod.g1,'root_text',side_effect=lambda p: texts[p]):
            mod.verify(write(d),physical=True)

    def test_physical_resolution_snapshot_divergence_is_rejected(self):
        d,texts,hashes=self._physical_fixture(); p='/private/newfuture-resolution.json'
        bad=json.loads(texts[p]); bad['release_id']=999; texts[p]=json.dumps(bad)
        with mock.patch.object(mod.g1,'root_hash',side_effect=lambda p: hashes[p]), mock.patch.object(mod.g1,'root_text',side_effect=lambda p: texts[p]):
            with self.assertRaisesRegex(RuntimeError,'persisted newfuture resolution diverges'):
                mod.verify(write(d),physical=True)

if __name__=='__main__': unittest.main()

class SourceG1CanonicalClosureTests(unittest.TestCase):
    def test_position8_canonical_range_includes_gate_owned_promises(self):
        data = json.loads((ROOT / 'release/canonical-promise-ledger.json').read_text())
        active = data['active_position']
        self.assertGreaterEqual(active['position'], 8)
        if active['position'] == 8:
            self.assertEqual(active['promise_range'], 'RNX-P374..RNX-P434')
            self.assertEqual(active['production_adopted_count'], 61)
        by = {item['number']: item for item in data['items']}
        for number in range(426, 435):
            item = by[number]
            self.assertEqual(item['status'], 'IMPLEMENTED_AND_PRODUCTION_ADOPTED')
            self.assertIn('source-g1-audit', item['evidence'])
            self.assertGreaterEqual(len(item['evidence']), 3)

    def test_gate_owned_promises_have_real_device_checkpoints(self):
        gate = (ROOT / 'scripts/dev/source_g1_device.py').read_text()
        required = (
            'resolve real external bclone, official rclone and latest NewFuture sources',
            'exercise latest NewFuture resolution -> download -> hash/archive -> qualification -> stage',
            'archive_sha != asset_digest',
            'select, import and qualify a real historical NewFuture baseline A',
            'explicitly activate staged external runtime and prove live process bytes',
            'one-click rollback and prove prior executable bytes restored',
            'exercise adversarial acquisition failures through production update path',
            'snapshot durable source/update authority evidence',
        )
        for token in required:
            self.assertIn(token, gate)
        self.assertNotIn('discover_fuse_helper', gate)
        self.assertIn("module_dir, binary = g1.copy_gate_module(root_dir, built)", gate)
        self.assertIn("managed_fuse_manifest = f'{state}/runtime/helpers/fusermount3/current-v1.json'", gate)
        self.assertIn("'fuse-helper-manifest'", gate)
