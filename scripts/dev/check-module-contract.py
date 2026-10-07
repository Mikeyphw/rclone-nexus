#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
MODULE = ROOT / "module"
errors: list[str] = []

required = [
    "module.prop",
    "customize.sh",
    "post-fs-data.sh",
    "service.sh",
    "action.sh",
    "uninstall.sh",
    "lib/common.sh",
    "system/bin/rclone-nexus",
    "system/bin/rclone-mountctl",
    "system/bin/rclone-doctor",
    "webroot/index.html",
    "webroot/style.css",
    "webroot/app.js",
    "webroot/bridge.js",
    "webroot/platform.json",
    "webroot/config.json",
    "webroot/icon.png",
]
for rel in required:
    if not (MODULE / rel).is_file():
        errors.append(f"missing required module file: {rel}")
for rel in ["go.mod", "cmd/racctl/main.go", "internal/protocol/types.go", "internal/control/engine.go", "internal/namespace/mutation.go", "internal/namespace/topology.go"]:
    if not (ROOT / rel).is_file():
        errors.append(f"missing native control-plane source: {rel}")

props: dict[str, str] = {}
for line in (MODULE / "module.prop").read_text(encoding="utf-8").splitlines():
    if "=" in line:
        key, value = line.split("=", 1)
        props[key] = value
if props.get("id") != "rclone_nexus":
    errors.append("module.prop id must be rclone_nexus")
if not re.fullmatch(r"[1-9][0-9]*", props.get("versionCode", "")):
    errors.append("module.prop versionCode must be a positive integer")

# Rclone Nexus contract: the package owns one injected rclone-family runtime;
# the tracked module tree must not vendor a FUSE/provider runtime.
for path in MODULE.rglob("*"):
    if path.is_file() and path.name in {"fusermount", "fusermount3", "libfuse.so", "libfuse3.so"}:
        errors.append(f"forbidden bundled FUSE/provider runtime: {path.relative_to(ROOT)}")

common = (MODULE / "lib/common.sh").read_text(encoding="utf-8")
if "RNEXUS_PROVIDER_MODULE_ID=rclone" not in common:
    errors.append("provider module id must remain explicit and centralized")
if "/data/adb/rclone-nexus" not in common:
    errors.append("persistent state must live outside /data/adb/modules")
if "rnexus_racctl_bin" not in common:
    errors.append("common.sh must resolve the native racctl backend")

for launcher in [MODULE / "system/bin/rclone-nexus", MODULE / "system/bin/rclone-mountctl", MODULE / "system/bin/rclone-doctor"]:
    text = launcher.read_text(encoding="utf-8") if launcher.exists() else ""
    if "racctl" not in text or len(text.splitlines()) > 24:
        errors.append(f"{launcher.name} must remain a thin racctl compatibility launcher")

package_script = (ROOT / "scripts/dev/package_module.py").read_text(encoding="utf-8")
customize = (MODULE / "customize.sh").read_text(encoding="utf-8")
service = (MODULE / "service.sh").read_text(encoding="utf-8")
for token in ("RNEXUS_RCLONE_PREBUILT", 'entries["system/bin/rclone"]'):
    if token not in package_script:
        errors.append(f"package_module.py missing static runtime contract token: {token}")
if 'system/bin/rclone' not in customize:
    errors.append("customize.sh must require the bundled system/bin/rclone")
if 'runtime status --json --require-operational' not in service:
    errors.append("service.sh must gate boot on static runtime operational status")

for executable in [
    MODULE / "customize.sh",
    MODULE / "post-fs-data.sh",
    MODULE / "service.sh",
    MODULE / "action.sh",
    MODULE / "uninstall.sh",
    MODULE / "system/bin/rclone-nexus",
    MODULE / "system/bin/rclone-mountctl",
    MODULE / "system/bin/rclone-doctor",
    ROOT / "scripts/dev/build_racctl.py",
    ROOT / "scripts/dev/android_namespace_device_smoke.py",
    ROOT / "scripts/dev/platform_x01_gate.py",
]:
    if executable.exists() and not executable.stat().st_mode & 0o111:
        errors.append(f"expected executable bit: {executable.relative_to(ROOT)}")

if errors:
    for error in errors:
        print(f"ERROR: {error}", file=sys.stderr)
    raise SystemExit(1)
print("module contract: OK")
