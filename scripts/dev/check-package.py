#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
from zipfile import ZipFile
import hashlib
import json
import stat
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
        "system/bin/racctl",
        "system/bin/rclone-nexus",
        "system/bin/rclone-mountctl",
        "system/bin/rclone-doctor",
        "integrity.manifest.json",
        "webroot/platform.json",
        "webroot/index.html",
    }
    missing = sorted(required - names)
    if missing:
        raise SystemExit(f"package missing: {', '.join(missing)}")
    forbidden = [n for n in names if n.rstrip('/').split('/')[-1] in {"rclone", "fusermount", "fusermount3"}]
    if forbidden:
        raise SystemExit(f"package bundles forbidden provider runtime: {forbidden}")
    racctl = zf.getinfo("system/bin/racctl")
    perms = (racctl.external_attr >> 16) & 0o777
    if perms != 0o755:
        raise SystemExit(f"racctl package mode must be 0755, got {perms:o}")

    manifest = json.loads(zf.read("integrity.manifest.json"))
    if manifest.get("schema_version") != 1:
        raise SystemExit("invalid integrity manifest schema")
    for entry in manifest.get("entries", []):
        rel = entry["path"]
        if rel not in names:
            raise SystemExit(f"integrity manifest missing packaged entry: {rel}")
        data = zf.read(rel)
        if hashlib.sha256(data).hexdigest() != entry["sha256"]:
            raise SystemExit(f"integrity hash mismatch: {rel}")
        info = zf.getinfo(rel)
        mode = (info.external_attr >> 16) & 0o777
        if mode != entry["mode"]:
            raise SystemExit(f"integrity mode mismatch: {rel}: {mode:o} != {entry['mode']:o}")
        if len(data) != entry["size"]:
            raise SystemExit(f"integrity size mismatch: {rel}")
print("package contract: OK")
