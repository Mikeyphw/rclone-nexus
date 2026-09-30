#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo
import os
import stat
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
MODULE = ROOT / "module"
BUILD = ROOT / "build" / "android" / "arm64-v8a" / "racctl"

props: dict[str, str] = {}
for line in (MODULE / "module.prop").read_text(encoding="utf-8").splitlines():
    if "=" in line:
        key, value = line.split("=", 1)
        props[key] = value
version = props.get("version", "v0.0.0").lstrip("v")
out = ROOT / "dist" / f"rclone-nexus-v{version}.zip"
out.parent.mkdir(parents=True, exist_ok=True)
if out.exists():
    out.unlink()

prebuilt = os.environ.get("RNEXUS_RACCTL_PREBUILT")
if prebuilt:
    racctl = Path(prebuilt).expanduser().resolve()
else:
    subprocess.run(
        [sys.executable, "scripts/dev/build_racctl.py", "--abi", "arm64-v8a", "--verify-reproducible"],
        cwd=ROOT,
        check=True,
    )
    racctl = BUILD
if not racctl.is_file():
    raise SystemExit(f"missing racctl build: {racctl}")


def write_entry(zf: ZipFile, rel: str, data: bytes, perms: int) -> None:
    info = ZipInfo(rel)
    info.compress_type = ZIP_DEFLATED
    info.create_system = 3
    info.external_attr = (stat.S_IFREG | perms) << 16
    info.date_time = (2026, 1, 1, 0, 0, 0)
    zf.writestr(info, data, compress_type=ZIP_DEFLATED, compresslevel=9)


with ZipFile(out, "w", ZIP_DEFLATED, compresslevel=9) as zf:
    for path in sorted(p for p in MODULE.rglob("*") if p.is_file()):
        rel = path.relative_to(MODULE).as_posix()
        mode = path.stat().st_mode
        perms = 0o755 if mode & stat.S_IXUSR else 0o644
        write_entry(zf, rel, path.read_bytes(), perms)
    write_entry(zf, "system/bin/racctl", racctl.read_bytes(), 0o755)

print(out.relative_to(ROOT))
