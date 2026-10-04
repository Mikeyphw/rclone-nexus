#!/usr/bin/env python3
from __future__ import annotations
import subprocess,sys
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
def run(a): print('$ '+' '.join(a)); subprocess.run(a,cwd=ROOT,check=True)
def require(c,m):
    if not c: raise SystemExit('ERROR: '+m)
def main():
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    workflow=(ROOT/'.github/workflows/runtime-source-build.yml').read_text()
    require('runtime-build-v1-' in workflow and 'gh release create' in workflow,'immutable SOURCE-X02 publication missing')
    source=(ROOT/'internal/runtimesource/source.go').read_text()
    tests=(ROOT/'internal/runtimebuild/published_test.go').read_text()
    require('materializeBuildBinary' in source and 'RuntimeSourcesDir, "builds"' in source,'verified source-build bytes are not durably materialized before resolution persistence')
    require('TestPublishedBuildResolutionRemainsReplayableAfterDownloadTempRemoved' in tests,'published build replay regression missing')
    run(['go','test','-count=1','./internal/runtimebuild','./internal/runtimeacquire','./internal/runtimesource','./internal/runtimeupdate','./internal/control','./cmd/racctl'])
    run(['node','scripts/dev/check_runtime_manager_ux.mjs'])
    print('SOURCE/UPDATE-HOTFIX-03: PASS — pinned commits and automatic bclone acquisition use verified SOURCE-X02 build authority')
    return 0
if __name__=='__main__': raise SystemExit(main())
