#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
from zipfile import ZipFile
import argparse
import hashlib
import json
import os
import stat
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
props = dict(line.split("=", 1) for line in (ROOT / "module" / "module.prop").read_text().splitlines() if "=" in line)
version = props.get("version", "v0.0.0").lstrip("v")
parser = argparse.ArgumentParser()
parser.add_argument("--ephemeral", action="store_true", help="write the package outside the repository for nested artifact validation")
args = parser.parse_args()

ephemeral_dir = tempfile.TemporaryDirectory(prefix="rnexus-package-output-") if args.ephemeral else None
archive = (Path(ephemeral_dir.name) / f"rclone-nexus-v{version}.zip") if ephemeral_dir else (ROOT / "dist" / f"rclone-nexus-v{version}.zip")

# Package-contract qualification must not access the network or materialize the
# tracked build cache. Inject a deterministic rclone-family fixture and a local
# NewFuture-shaped archive. The helper provenance remains NewFuture even in this
# hermetic contract test.
runtime_bytes = b"#!/system/bin/sh\n# deterministic package-contract runtime fixture\nexit 0\n"
helper_bytes = b"#!/system/bin/sh\n# deterministic NewFuture fusermount3 fixture\nexit 0\n"
libfuse_bytes = b"deterministic-NewFuture-libfuse3-fixture\n"
with tempfile.TemporaryDirectory(prefix="rnexus-package-contract-") as td:
    td = Path(td)
    racctl = td / "racctl"
    rclone = td / "rclone"
    newfuture = td / "magisk-rclone_arm64-v8a.zip"
    subprocess.run(
        [sys.executable, "scripts/dev/build_racctl.py", "--abi", "arm64-v8a", "--output", str(racctl), "--verify-reproducible"],
        cwd=ROOT,
        check=True,
    )
    rclone.write_bytes(runtime_bytes)
    rclone.chmod(0o755)
    with ZipFile(newfuture, "w") as zf:
        zf.writestr("system/vendor/bin/fusermount3", helper_bytes)
        zf.writestr("system/vendor/lib64/libfuse3.so.3", libfuse_bytes)
    env = os.environ.copy()
    env["RNEXUS_RACCTL_PREBUILT"] = str(racctl)
    env["RNEXUS_RCLONE_PREBUILT"] = str(rclone)
    env["RNEXUS_NEWFUTURE_ARCHIVE"] = str(newfuture)
    env["RNEXUS_NEWFUTURE_TAG"] = "fixture"
    env["RNEXUS_ALLOW_RUNTIME_TEST_FIXTURES"] = "1"
    if args.ephemeral:
        env["RNEXUS_PACKAGE_OUT"] = str(archive)
    subprocess.run(
        [sys.executable, "scripts/dev/package_module.py", "--runtime-provider", "prebuilt"],
        cwd=ROOT,
        env=env,
        check=True,
    )
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
        "system/bin/rclone",
        "system/vendor/bin/fusermount3",
        "runtime.provenance.json",
        "system/bin/rclone-nexus",
        "system/bin/rclone-mountctl",
        "system/bin/rclone-doctor",
        "integrity.manifest.json",
        "webroot/platform.json",
        "webroot/config.json",
        "webroot/icon.png",
        "webroot/index.html",
        "webroot/style.css",
        "webroot/app.js",
        "webroot/bridge.js",
    }
    missing = sorted(required - names)
    if missing:
        raise SystemExit(f"package missing: {', '.join(missing)}")
    helper_names = [n for n in names if n.rstrip('/').split('/')[-1] == "fusermount3"]
    if helper_names != ["system/vendor/bin/fusermount3"]:
        raise SystemExit(f"package must contain exactly one canonical NewFuture fusermount3, got {helper_names}")
    if zf.read("system/vendor/bin/fusermount3") != helper_bytes:
        raise SystemExit("package did not preserve NewFuture fusermount3 bytes")
    if zf.read("system/vendor/lib64/libfuse3.so.3") != libfuse_bytes:
        raise SystemExit("package did not preserve NewFuture libfuse payload bytes")
    provenance = json.loads(zf.read("runtime.provenance.json"))
    fuse = provenance.get("fuse_helper", {})
    runtime_prov = provenance.get("runtime", {})
    if fuse.get("repository") != "NewFuture/rclone-fuse3-magisk" or fuse.get("provider_invariant") != "newfuture":
        raise SystemExit("fusermount3 provenance is not fixed to NewFuture")
    if runtime_prov.get("provider") != "prebuilt":
        raise SystemExit("package-contract runtime provider provenance mismatch")
    if fuse.get("helper_sha256") != hashlib.sha256(helper_bytes).hexdigest():
        raise SystemExit("fusermount3 provenance hash mismatch")
    if zf.read("system/bin/rclone") != runtime_bytes:
        raise SystemExit("package did not preserve the exact injected rclone-family runtime bytes")
    for executable in ("system/bin/racctl", "system/bin/rclone", "system/vendor/bin/fusermount3"):
        info = zf.getinfo(executable)
        perms = (info.external_attr >> 16) & 0o777
        if perms != 0o755:
            raise SystemExit(f"{executable} package mode must be 0755, got {perms:o}")

    manifest = json.loads(zf.read("integrity.manifest.json"))
    if manifest.get("schema_version") != 1:
        raise SystemExit("invalid integrity manifest schema")
    manifest_paths = {entry.get("path") for entry in manifest.get("entries", [])}
    install_only = {"customize.sh"}
    leaked = sorted(install_only & manifest_paths)
    if leaked:
        raise SystemExit(f"installer-only files must not be in installed-runtime integrity manifest: {leaked}")
    runtime_expected = names - install_only - {"integrity.manifest.json"}
    missing_runtime = sorted(runtime_expected - manifest_paths)
    if missing_runtime:
        raise SystemExit(f"integrity manifest missing runtime entry: {missing_runtime}")
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
print("package contract: OK" + (" (ephemeral)" if args.ephemeral else ""))
if ephemeral_dir is not None:
    ephemeral_dir.cleanup()
