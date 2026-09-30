#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
from zipfile import ZipFile
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
subprocess.run([sys.executable, "scripts/dev/package_module.py"], cwd=ROOT, check=True)
archive = ROOT / "dist" / "rclone-nexus-v0.1.0-dev.zip"
if not archive.is_file():
    raise SystemExit(f"missing artifact: {archive}")
with ZipFile(archive) as zf:
    names = set(zf.namelist())
    required = {
        "module.prop",
        "customize.sh",
        "post-fs-data.sh",
        "service.sh",
        "system/bin/rclone-nexus",
        "system/bin/rclone-mountctl",
        "system/bin/rclone-doctor",
    }
    missing = sorted(required - names)
    if missing:
        raise SystemExit(f"package missing: {', '.join(missing)}")
    forbidden = [n for n in names if n.rstrip('/').split('/')[-1] in {"rclone", "fusermount", "fusermount3"}]
    if forbidden:
        raise SystemExit(f"package bundles forbidden provider runtime: {forbidden}")
print("package contract: OK")
