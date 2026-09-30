#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
MODULE_PROP = ROOT / "module" / "module.prop"

TARGETS: dict[str, tuple[str, str, str | None]] = {
    "arm64-v8a": ("android", "arm64", None),
    "armeabi-v7a": ("android", "arm", "7"),
    "x86_64": ("android", "amd64", None),
    "x86": ("android", "386", None),
}


def version() -> str:
    for line in MODULE_PROP.read_text(encoding="utf-8").splitlines():
        if line.startswith("version="):
            return line.split("=", 1)[1].strip()
    raise SystemExit("module.prop has no version=")


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def build(output: Path, *, goos: str, goarch: str, goarm: str | None) -> None:
    env = os.environ.copy()
    env.update(
        {
            "CGO_ENABLED": "0",
            "GOOS": goos,
            "GOARCH": goarch,
            "GOTOOLCHAIN": "local",
            "SOURCE_DATE_EPOCH": "0",
            "TZ": "UTC",
        }
    )
    if goarm:
        env["GOARM"] = goarm
    else:
        env.pop("GOARM", None)
    output.parent.mkdir(parents=True, exist_ok=True)
    ldflags = f"-s -w -buildid= -X=rclone-nexus/internal/buildinfo.Version={version()}"
    command = [
        "go",
        "build",
        "-trimpath",
        "-buildvcs=false",
        "-ldflags",
        ldflags,
        "-o",
        str(output),
        "./cmd/racctl",
    ]
    subprocess.run(command, cwd=ROOT, env=env, check=True)
    output.chmod(0o755)


def main() -> int:
    parser = argparse.ArgumentParser(description="Build deterministic Rclone Nexus racctl binaries")
    parser.add_argument("--abi", choices=sorted(TARGETS), default="arm64-v8a")
    parser.add_argument("--host", action="store_true", help="build for the current host instead of Android")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--verify-reproducible", action="store_true")
    args = parser.parse_args()

    if shutil.which("go") is None:
        raise SystemExit("Go toolchain is required")

    if args.host:
        goos = os.environ.get("GOHOSTOS") or subprocess.check_output(["go", "env", "GOHOSTOS"], text=True).strip()
        goarch = os.environ.get("GOHOSTARCH") or subprocess.check_output(["go", "env", "GOHOSTARCH"], text=True).strip()
        goarm = None
        default_output = ROOT / "build" / "host" / "racctl"
    else:
        goos, goarch, goarm = TARGETS[args.abi]
        default_output = ROOT / "build" / "android" / args.abi / "racctl"

    output = (args.output or default_output).resolve()
    build(output, goos=goos, goarch=goarch, goarm=goarm)
    digest = sha256(output)

    if args.verify_reproducible:
        with tempfile.TemporaryDirectory(prefix="racctl-repro-") as temporary:
            second = Path(temporary) / "racctl"
            build(second, goos=goos, goarch=goarch, goarm=goarm)
            second_digest = sha256(second)
            if second_digest != digest:
                raise SystemExit(f"non-reproducible racctl build: {digest} != {second_digest}")

    print(f"{output.relative_to(ROOT) if output.is_relative_to(ROOT) else output} sha256={digest}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
