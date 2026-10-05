#!/usr/bin/env python3
from __future__ import annotations
import importlib.util
import subprocess
import sys
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]

def load(name: str):
    path=ROOT/'scripts/dev'/f'{name}.py'
    spec=importlib.util.spec_from_file_location(name,path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f'cannot load {path}')
    mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod); return mod

def main() -> int:
    x02=load('source_x02_gate'); g1=load('source_g1_gate')
    x02.assert_scope(); x02.assert_architecture()
    g1.assert_scope(); g1.assert_architecture(); g1.assert_no_static_only_seal(); g1.assert_position8_gate_promises_are_behavioral()
    source=(ROOT/'scripts/dev/source_g1_device.py').read_text()
    builder=(ROOT/'scripts/dev/runtime_source_build.py').read_text()
    grand=(ROOT/'scripts/dev/runtime_grand_g1_device.py').read_text()
    bundle=(ROOT/'internal/runtimebuild/bundle.go').read_text()
    required=(
        ('native-clang-ndk-sysroot',source),('_probe_native_clang',source),('_ndk_clang_resource_dir',source),
        ('--compiler-mode',grand),('--compiler-resource-dir',grand),('CGO_CFLAGS',builder),('--sysroot=',builder),('-resource-dir=',builder),('-llog',builder),('compiler_resource_dir',builder),('android_system_libraries',builder),
        ('GOMODCACHE',builder),('GOCACHE',builder),('GOWORK',builder),('GOTOOLCHAIN',builder),('"go", "mod", "download", "all"',builder),('-mod=readonly',builder),('go_module_graph_sha256',builder),('isolated-ephemeral',builder),
        ('CompilerMode',bundle),('CompilerTarget',bundle),('CompilerResourceDir',bundle),('CompilerRTBuiltinsSHA256',bundle),('CompilerLibunwindSHA256',bundle),('AndroidSystemLibraries',bundle),('GoModuleGraphSHA256',bundle),('GoModuleCacheScope',bundle),
    )
    for token,text in required:
        if token not in text:
            raise RuntimeError(f'G1-A HOTFIX-06 contract missing {token}')
    subprocess.run([sys.executable,'scripts/dev/check_canonical_scope.py'],cwd=ROOT,check=True)
    print('RUNTIME-GRAND-G1-A HOTFIX-10 isolated Go module authority: PASS')
    return 0

if __name__=='__main__': raise SystemExit(main())
