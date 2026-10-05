from __future__ import annotations
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
from unittest import mock
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('runtime_source_build',ROOT/'scripts/dev/runtime_source_build.py')
mod=importlib.util.module_from_spec(spec); assert spec and spec.loader; spec.loader.exec_module(mod)

class SourceX02TermuxNDKTests(unittest.TestCase):
    def test_native_clang_build_binds_pinned_ndk_target_and_sysroot(self):
        with tempfile.TemporaryDirectory() as raw:
            td=Path(raw); source=td/'src'; out=td/'out'; ndk=td/'ndk'
            source.mkdir(); (source/'go.mod').write_text('module example.invalid/x\n\ngo 1.23\n')
            host=ndk/'toolchains/llvm/prebuilt/linux-x86_64'
            sysroot=host/'sysroot'; sysroot.mkdir(parents=True)
            resource=host/'lib/clang/19'
            runtime=resource/'lib/linux'; (runtime/'aarch64').mkdir(parents=True)
            (runtime/'libclang_rt.builtins-aarch64-android.a').write_bytes(b'builtins')
            (runtime/'aarch64/libunwind.a').write_bytes(b'unwind')
            compiler=td/'clang'; compiler.write_text('#!/bin/sh\nexit 0\n'); compiler.chmod(0o755)
            commit='a'*40
            args=SimpleNamespace(source_dir=str(source),output_dir=str(out),repository='example/x',requested_ref='main',resolved_commit=commit,source_id='bclone',engine='bclone',ndk=str(ndk),ndk_version='28.2.13676358',ndk_host='linux-x86_64',compiler=str(compiler),compiler_mode='native-clang-ndk-sysroot',compiler_resource_dir=str(resource),api_level=21,binary_name='bclone-android-arm64')
            def fake_run(argv,cwd=None,env=None):
                if argv[:3]==['git','rev-parse','HEAD']: return commit
                if argv[:3]==['git','status','--porcelain']: return ''
                if argv[:2]==['go','version']: return 'go version go1.27.1 android/arm64'
                if argv[0]==str(compiler) and argv[1]=='--version': return 'clang version 22.0.0'
                raise AssertionError(argv)
            seen={}
            def fake_subprocess(argv,cwd=None,env=None,check=False,**kwargs):
                self.assertEqual(argv[0:2],['go','build'])
                seen['env']=dict(env)
                binary=Path(argv[argv.index('-o')+1]); binary.parent.mkdir(parents=True,exist_ok=True); binary.write_bytes(b'android-arm64-fixture')
                return SimpleNamespace(returncode=0)
            with mock.patch.object(mod,'run',side_effect=fake_run), mock.patch.object(mod.subprocess,'run',side_effect=fake_subprocess):
                self.assertEqual(mod.build(args),0)
            env=seen['env']; target='--target=aarch64-linux-android21'; sysroot_flag=f'--sysroot={sysroot}'; resource_flag=f'-resource-dir={resource}'
            self.assertEqual(env['CC'],str(compiler.resolve()))
            self.assertIn(target,env['CGO_CFLAGS']); self.assertIn(sysroot_flag,env['CGO_CFLAGS']); self.assertIn(resource_flag,env['CGO_CFLAGS'])
            self.assertIn(target,env['CGO_LDFLAGS']); self.assertIn(resource_flag,env['CGO_LDFLAGS']); self.assertIn('-fuse-ld=lld',env['CGO_LDFLAGS']); self.assertIn('-llog',env['CGO_LDFLAGS'])
            prov=json.loads((out/'provenance.json').read_text())
            self.assertEqual(prov['ndk_host'],'linux-x86_64')
            self.assertEqual(prov['compiler_mode'],'native-clang-ndk-sysroot')
            self.assertEqual(prov['compiler_target'],'aarch64-linux-android21')
            self.assertEqual(prov['ndk_sysroot'],str(sysroot))
            self.assertEqual(prov['compiler_resource_dir'],str(resource))
            self.assertEqual(len(prov['compiler_rt_builtins_sha256']),64)
            self.assertEqual(len(prov['compiler_libunwind_sha256']),64)
            self.assertEqual(prov['android_system_libraries'],['log'])

if __name__=='__main__': unittest.main()
