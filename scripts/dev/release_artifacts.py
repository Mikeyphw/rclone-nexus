#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
MODULE_PROP = ROOT / "module" / "module.prop"
PRIVATE_EVIDENCE = {
    "release/evidence/device-qualification.json",
    "release/evidence/runtime-g1-device-qualification.json",
}


def props() -> dict[str, str]:
    out: dict[str, str] = {}
    for line in MODULE_PROP.read_text(encoding="utf-8").splitlines():
        if "=" in line:
            k, v = line.split("=", 1)
            out[k] = v
    return out


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def source_digest() -> str:
    roots = [".devtool.toml", "go.mod", "cmd", "internal", "module", "scripts/dev", "tests", "docs", "release", "README.md", "CHANGELOG.md"]
    h = hashlib.sha256()
    files: list[Path] = []
    for item in roots:
        p = ROOT / item
        if p.is_file():
            files.append(p)
        elif p.is_dir():
            files.extend(
                x for x in p.rglob("*")
                if x.is_file()
                and "__pycache__" not in x.parts
                and x.relative_to(ROOT).as_posix() not in PRIVATE_EVIDENCE
            )
    for p in sorted(files, key=lambda x: x.relative_to(ROOT).as_posix()):
        rel = p.relative_to(ROOT).as_posix().encode()
        data = p.read_bytes()
        h.update(len(rel).to_bytes(4, "big")); h.update(rel)
        h.update(len(data).to_bytes(8, "big")); h.update(data)
    return h.hexdigest()


def run() -> None:
    meta = props()
    version = meta.get("version", "").lstrip("v")
    module_id = meta.get("id", "")
    if not version or version.endswith("-dev") or version.endswith("-rc"):
        raise SystemExit(f"release version must be final, got {version!r}")
    if module_id != "rclone_nexus":
        raise SystemExit(f"unexpected module id: {module_id}")

    prebuilt = os.environ.get("RNEXUS_RACCTL_PREBUILT")
    temporary_build: tempfile.TemporaryDirectory[str] | None = None
    try:
        if prebuilt:
            build = Path(prebuilt).expanduser().resolve()
        else:
            # Release qualification must not dirty the tracked build cache.
            # Build into an isolated temporary path and feed package_module via
            # its existing prebuilt contract.
            temporary_build = tempfile.TemporaryDirectory(prefix="rnexus-release-racctl-")
            build = Path(temporary_build.name) / "racctl"
            subprocess.run(
                [sys.executable, "scripts/dev/build_racctl.py", "--abi", "arm64-v8a", "--output", str(build), "--verify-reproducible"],
                cwd=ROOT,
                check=True,
            )
        if not build.is_file():
            raise SystemExit("missing reproducible arm64 racctl")

        env = os.environ.copy()
        env["RNEXUS_RACCTL_PREBUILT"] = str(build)
        expected = ROOT / "dist" / f"rclone-nexus-v{version}.zip"
        first: bytes | None = None
        for idx in range(2):
            subprocess.run([sys.executable, "scripts/dev/package_module.py"], cwd=ROOT, env=env, check=True)
            data = expected.read_bytes()
            if idx == 0:
                first = data
            elif data != first:
                raise SystemExit("release package is not byte-reproducible")
    finally:
        if temporary_build is not None:
            temporary_build.cleanup()

    assert first is not None
    checksum = sha256(first)
    sums = f"{checksum}  {expected.name}\n"
    (ROOT / "dist" / "SHA256SUMS").write_text(sums, encoding="utf-8")

    manifest = {
        "schema_version": 1,
        "module_id": module_id,
        "version": f"v{version}",
        "source_digest": source_digest(),
        "artifacts": [{"name": expected.name, "sha256": checksum, "size": len(first)}],
        "evidence_schema": 3,
    }
    (ROOT / "dist" / "release-manifest.json").write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(json.dumps(manifest, sort_keys=True))


if __name__ == "__main__":
    run()
