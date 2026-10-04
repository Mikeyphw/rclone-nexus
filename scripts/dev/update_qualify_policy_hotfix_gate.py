#!/usr/bin/env python3
from __future__ import annotations
import subprocess, sys
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
def run(argv):
    print('$ '+' '.join(argv), flush=True)
    cp=subprocess.run(argv,cwd=ROOT,check=False)
    if cp.returncode:
        print(f"UPDATE-X01-HOTFIX-02 command failed with exit {cp.returncode}: {' '.join(argv)}", file=sys.stderr, flush=True)
        raise SystemExit(cp.returncode)
def main():
    run([sys.executable,'scripts/dev/check_canonical_scope.py'])
    run(['go','test','-count=1','./internal/runtimestore','-run','^TestAcquirePublishesPendingCandidateWithoutRunningQualifier$'])
    run(['go','test','-count=1','./internal/runtimeupdate','-run','^TestAcquireWithoutAutomaticQualificationStopsAtAcquired$'])
    run(['go','test','./internal/runtimeupdate','./internal/runtimestore','./internal/runtimesource'])
    run([sys.executable,'scripts/dev/runtime_standalone_x02_gate.py'])
    print('UPDATE-X01-HOTFIX-02: PASS — acquire/qualify/stage policy stages are behaviorally independent')
    return 0
if __name__=='__main__': raise SystemExit(main())
