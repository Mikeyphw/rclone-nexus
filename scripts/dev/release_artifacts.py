#!/usr/bin/env python3
from __future__ import annotations

import argparse
from contextlib import ExitStack
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
MODULE_PROP = ROOT / "module" / "module.prop"
PRIVATE_EVIDENCE = {
    "release/evidence/device-qualification.json",
    "release/evidence/runtime-g1-device-qualification.json",
    "release/evidence/source-g1-supply-chain-qualification.json",
    "release/evidence/runtime-grand-g1-device.json",
    "release/evidence/runtime-grand-g1-seal.json",
}


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser()
    p.add_argument("--runtime-provider", choices=("newfuture", "bclone", "prebuilt"), default="")
    p.add_argument("--runtime-dir", default="")
    p.add_argument("--newfuture-tag", default="")
    p.add_argument("--newfuture-archive", default="")
    p.add_argument("--bclone-ref", default="")
    return p


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


def materialize_runtime(args: argparse.Namespace, stack: ExitStack) -> Path:
    if args.runtime_dir:
        runtime_dir = Path(args.runtime_dir).expanduser().resolve()
    else:
        temp = Path(stack.enter_context(tempfile.TemporaryDirectory(prefix="rnexus-release-runtime-")))
        runtime_dir = temp / "runtime"
        provider = (
            args.runtime_provider
            or os.environ.get("RNEXUS_RUNTIME_PROVIDER", "").strip()
            or ("prebuilt" if os.environ.get("RNEXUS_RCLONE_PREBUILT", "").strip() else "newfuture")
        )
        cmd = [sys.executable, "scripts/dev/runtime_inputs.py", "materialize", "--provider", provider, "--output-dir", str(runtime_dir)]
        if args.newfuture_tag:
            cmd += ["--newfuture-tag", args.newfuture_tag]
        if args.newfuture_archive:
            cmd += ["--newfuture-archive", args.newfuture_archive]
        if args.bclone_ref:
            cmd += ["--bclone-ref", args.bclone_ref]
        subprocess.run(cmd, cwd=ROOT, check=True)
    subprocess.run([sys.executable, "scripts/dev/runtime_inputs.py", "verify", "--runtime-dir", str(runtime_dir)], cwd=ROOT, check=True)
    return runtime_dir


def run_release(args: argparse.Namespace) -> None:
    meta = props()
    version = meta.get("version", "").lstrip("v")
    module_id = meta.get("id", "")
    if not version or version.endswith("-dev") or version.endswith("-rc"):
        raise SystemExit(f"release version must be final, got {version!r}")
    if module_id != "rclone_nexus":
        raise SystemExit(f"unexpected module id: {module_id}")

    with ExitStack() as stack:
        prebuilt = os.environ.get("RNEXUS_RACCTL_PREBUILT")
        if prebuilt:
            build = Path(prebuilt).expanduser().resolve()
        else:
            temporary_build = Path(stack.enter_context(tempfile.TemporaryDirectory(prefix="rnexus-release-racctl-")))
            build = temporary_build / "racctl"
            subprocess.run(
                [sys.executable, "scripts/dev/build_racctl.py", "--abi", "arm64-v8a", "--output", str(build), "--verify-reproducible"],
                cwd=ROOT,
                check=True,
            )
        if not build.is_file():
            raise SystemExit("missing reproducible arm64 racctl")

        runtime_dir = materialize_runtime(args, stack)
        env = os.environ.copy()
        env["RNEXUS_RACCTL_PREBUILT"] = str(build)
        expected = ROOT / "dist" / f"rclone-nexus-v{version}.zip"
        first: bytes | None = None
        for idx in range(2):
            subprocess.run(
                [sys.executable, "scripts/dev/package_module.py", "--runtime-dir", str(runtime_dir)],
                cwd=ROOT,
                env=env,
                check=True,
            )
            data = expected.read_bytes()
            if idx == 0:
                first = data
            elif data != first:
                raise SystemExit("release package is not byte-reproducible")

    assert first is not None
    checksum = sha256(first)
    sums = f"{checksum}  {expected.name}\n"
    (ROOT / "dist" / "SHA256SUMS").write_text(sums, encoding="utf-8")

    runtime_provenance: dict = {}
    from zipfile import ZipFile
    with ZipFile(expected) as zf:
        runtime_provenance = json.loads(zf.read("runtime.provenance.json"))

    manifest = {
        "schema_version": 1,
        "module_id": module_id,
        "version": f"v{version}",
        "source_digest": source_digest(),
        "runtime_provider": (runtime_provenance.get("runtime") or {}).get("provider", ""),
        "runtime_sha256": (runtime_provenance.get("runtime") or {}).get("sha256", ""),
        "fusermount3_source": (runtime_provenance.get("fuse_helper") or {}).get("repository", ""),
        "fusermount3_sha256": (runtime_provenance.get("fuse_helper") or {}).get("helper_sha256", ""),
        "artifacts": [{"name": expected.name, "sha256": checksum, "size": len(first)}],
        "evidence_schema": 3,
    }
    (ROOT / "dist" / "release-manifest.json").write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(json.dumps(manifest, sort_keys=True))


def main() -> int:
    run_release(parser().parse_args())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
