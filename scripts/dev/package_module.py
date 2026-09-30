#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo
import stat

ROOT = Path(__file__).resolve().parents[2]
MODULE = ROOT / "module"
props = {}
for line in (MODULE / "module.prop").read_text(encoding="utf-8").splitlines():
    if "=" in line:
        key, value = line.split("=", 1)
        props[key] = value
version = props.get("version", "v0.0.0").lstrip("v")
out = ROOT / "dist" / f"rclone-nexus-v{version}.zip"
out.parent.mkdir(parents=True, exist_ok=True)
if out.exists():
    out.unlink()

with ZipFile(out, "w", ZIP_DEFLATED, compresslevel=9) as zf:
    for path in sorted(p for p in MODULE.rglob("*") if p.is_file()):
        rel = path.relative_to(MODULE).as_posix()
        info = ZipInfo(rel)
        info.compress_type = ZIP_DEFLATED
        info.create_system = 3
        mode = path.stat().st_mode
        perms = 0o755 if mode & stat.S_IXUSR else 0o644
        info.external_attr = (stat.S_IFREG | perms) << 16
        info.date_time = (2026, 1, 1, 0, 0, 0)
        zf.writestr(info, path.read_bytes(), compress_type=ZIP_DEFLATED, compresslevel=9)

print(out.relative_to(ROOT))
