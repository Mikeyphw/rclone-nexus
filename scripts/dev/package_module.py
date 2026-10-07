#!/usr/bin/env python3
from __future__ import annotations

import argparse
from contextlib import ExitStack
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo
import hashlib
import json
import os
import stat
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
MODULE = ROOT / "module"
BUILD = ROOT / "build" / "android" / "arm64-v8a" / "racctl"
PROVENANCE_NAME = "runtime.provenance.json"


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser()
    p.add_argument("--runtime-provider", choices=("newfuture", "bclone", "prebuilt"), default="")
    p.add_argument("--runtime-dir", default="", help="consume a directory already materialized by runtime_inputs.py")
    p.add_argument("--newfuture-tag", default="")
    p.add_argument("--newfuture-archive", default="")
    p.add_argument("--bclone-ref", default="")
    return p


def write_entry(zf: ZipFile, rel: str, data: bytes, perms: int) -> None:
    info = ZipInfo(rel)
    info.compress_type = ZIP_DEFLATED
    info.create_system = 3
    info.external_attr = (stat.S_IFREG | perms) << 16
    info.date_time = (2026, 1, 1, 0, 0, 0)
    zf.writestr(info, data, compress_type=ZIP_DEFLATED, compresslevel=9)


def package_props() -> tuple[str, Path]:
    props: dict[str, str] = {}
    for line in (MODULE / "module.prop").read_text(encoding="utf-8").splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            props[key] = value
    version = props.get("version", "v0.0.0").lstrip("v")
    package_out_env = os.environ.get("RNEXUS_PACKAGE_OUT")
    if package_out_env:
        out = Path(package_out_env).expanduser().resolve()
    else:
        out = ROOT / "dist" / f"rclone-nexus-v{version}.zip"
    return version, out


def materialize_runtime(args: argparse.Namespace, stack: ExitStack) -> Path:
    if args.runtime_dir:
        runtime_dir = Path(args.runtime_dir).expanduser().resolve()
    else:
        temp = Path(stack.enter_context(tempfile.TemporaryDirectory(prefix="rnexus-package-runtime-")))
        runtime_dir = temp / "runtime"
        provider = (
            args.runtime_provider
            or os.environ.get("RNEXUS_RUNTIME_PROVIDER", "").strip()
            or ("prebuilt" if os.environ.get("RNEXUS_RCLONE_PREBUILT", "").strip() else "newfuture")
        )
        cmd = [
            sys.executable,
            "scripts/dev/runtime_inputs.py",
            "materialize",
            "--provider", provider,
            "--output-dir", str(runtime_dir),
        ]
        if args.newfuture_tag:
            cmd += ["--newfuture-tag", args.newfuture_tag]
        if args.newfuture_archive:
            cmd += ["--newfuture-archive", args.newfuture_archive]
        if args.bclone_ref:
            cmd += ["--bclone-ref", args.bclone_ref]
        subprocess.run(cmd, cwd=ROOT, check=True)
    subprocess.run(
        [sys.executable, "scripts/dev/runtime_inputs.py", "verify", "--runtime-dir", str(runtime_dir)],
        cwd=ROOT,
        check=True,
    )
    return runtime_dir


def add_runtime_inputs(entries: dict[str, tuple[bytes, int]], runtime_dir: Path) -> None:
    runtime = runtime_dir / "rclone"
    provenance = runtime_dir / "runtime-provenance.json"
    if not runtime.is_file() or not provenance.is_file():
        raise SystemExit(f"incomplete runtime input directory: {runtime_dir}")
    entries["system/bin/rclone"] = (runtime.read_bytes(), 0o755)
    entries[PROVENANCE_NAME] = (provenance.read_bytes(), 0o644)

    nf_root = runtime_dir / "newfuture"
    if not nf_root.is_dir():
        raise SystemExit("runtime inputs are missing NewFuture FUSE payload")
    helper_count = 0
    for path in sorted(p for p in nf_root.rglob("*") if p.is_file()):
        rel = path.relative_to(nf_root).as_posix()
        if rel.startswith("../") or rel.startswith("/"):
            raise SystemExit(f"invalid NewFuture payload path: {rel}")
        if Path(rel).name == "rclone":
            raise SystemExit("NewFuture helper payload may not introduce a second rclone runtime")
        if Path(rel).name == "fusermount3":
            helper_count += 1
        perms = 0o755 if path.stat().st_mode & stat.S_IXUSR else 0o644
        if rel in entries:
            raise SystemExit(f"runtime input collides with tracked module payload: {rel}")
        entries[rel] = (path.read_bytes(), perms)
    if helper_count != 1:
        raise SystemExit(f"runtime input must contain exactly one NewFuture fusermount3, got {helper_count}")


def main() -> int:
    args = parser().parse_args()
    _, out = package_props()
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

    with ExitStack() as stack:
        runtime_dir = materialize_runtime(args, stack)
        entries: dict[str, tuple[bytes, int]] = {}
        for path in sorted(p for p in MODULE.rglob("*") if p.is_file()):
            rel = path.relative_to(MODULE).as_posix()
            mode = path.stat().st_mode
            perms = 0o755 if mode & stat.S_IXUSR else 0o644
            entries[rel] = (path.read_bytes(), perms)
        entries["system/bin/racctl"] = (racctl.read_bytes(), 0o755)
        add_runtime_inputs(entries, runtime_dir)

        # Root managers consume installer-only files such as customize.sh while staging
        # the module. They must remain in the flashable ZIP, but cannot be part of the
        # post-reboot installed-runtime integrity contract.
        install_only_entries = {"customize.sh"}

        manifest_entries = []
        for rel in sorted(entries):
            if rel in install_only_entries:
                continue
            data, perms = entries[rel]
            manifest_entries.append({
                "path": rel,
                "sha256": hashlib.sha256(data).hexdigest(),
                "mode": perms,
                "size": len(data),
            })
        manifest = json.dumps(
            {"schema_version": 1, "entries": manifest_entries},
            sort_keys=True,
            indent=2,
            separators=(",", ": "),
        ).encode("utf-8") + b"\n"
        entries["integrity.manifest.json"] = (manifest, 0o644)

        with ZipFile(out, "w", ZIP_DEFLATED, compresslevel=9) as zf:
            for rel in sorted(entries):
                data, perms = entries[rel]
                write_entry(zf, rel, data, perms)

    print(out.relative_to(ROOT) if out.is_relative_to(ROOT) else out)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
