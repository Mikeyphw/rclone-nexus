#!/usr/bin/env python3
"""PLATFORM-G1 qualification audit.

This gate is intentionally repository/package focused. Real-manager device
qualification is reported separately by release/device evidence; this script
fails closed on platform promises that can be proven from the source tree and
portable lifecycle fixtures.
"""
from __future__ import annotations

import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
errors: list[str] = []


def require(cond: bool, message: str) -> None:
    if not cond:
        errors.append(message)


def text(rel: str) -> str:
    return (ROOT / rel).read_text(encoding="utf-8")


# Re-run the implementation audit first so PLATFORM-G1 is a strict superset.
cp = subprocess.run(
    [sys.executable, "scripts/dev/platform_x01_gate.py"],
    cwd=ROOT,
    text=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.STDOUT,
    check=False,
)
if cp.returncode != 0:
    errors.append("PLATFORM-X01 audit no longer passes: " + cp.stdout.strip())

required = [
    "docs/implementation/PLATFORM-X01.md",
    "docs/implementation/PLATFORM-G1.md",
    "internal/rootmgr/rootmgr.go",
    "internal/diagnostics/log.go",
    "internal/doctor/doctor.go",
    "internal/integrity/integrity.go",
    "internal/platformstate/state.go",
    "internal/platformlifecycle/lifecycle.go",
    "module/customize.sh",
    "module/post-fs-data.sh",
    "module/service.sh",
    "module/uninstall.sh",
]
for rel in required:
    require((ROOT / rel).is_file(), f"missing PLATFORM-G1 surface: {rel}")

post = text("module/post-fs-data.sh")
customize = text("module/customize.sh")
uninstall = text("module/uninstall.sh")
rootmgr = text("internal/rootmgr/rootmgr.go")
cli = text("cmd/racctl/main.go")

# Failed updates must not migrate state before the new module proves its own
# integrity. Missing manifests are a hard failure, not a development warning on
# the install/boot command path.
verify_at = post.find("platform verify-integrity")
migrate_at = post.find("platform migrate")
require(verify_at >= 0 and migrate_at >= 0 and verify_at < migrate_at,
        "post-fs-data must verify module integrity before persistent-state migration")
require("platform validate-upgrade" not in customize and "platform verify-integrity" not in customize,
        "customize.sh must remain staging-safe; installed-runtime checks belong after activation")
install_stack = text("scripts/dev/install_stack.py") if (ROOT / "scripts/dev/install_stack.py").is_file() else ""
require("validate-upgrade" in install_stack and "verify-integrity" in install_stack,
        "post-reboot install verification must own upgrade-state and installed-integrity checks")
package_script = text("scripts/dev/package_module.py")
require('INSTALL_ONLY_ENTRIES = {"customize.sh"}' in package_script,
        "installed-runtime integrity manifest must exclude root-manager-consumed customize.sh")
require("if !result.Available || !result.OK" in cli,
        "platform verify-integrity must fail closed when manifest is unavailable or invalid")

# Unknown-compatible managers may expose a common module root, but that alone
# does not prove lifecycle hooks or an embedded WebUI transport.
compatible_block = rootmgr.rsplit("case KindCompatible:", 1)[1].split("default:", 1)[0]
require("Capabilities{ModuleHooks: true}" in compatible_block,
        "unknown-compatible root manager must retain only proven module-layout capability")
for token in ("ServiceHook: true", "PostFSDataHook: true", "UninstallHook: true", "ActionHook: true", "EmbeddedWebUI: true"):
    require(token not in compatible_block, f"unknown-compatible manager invents unproven capability: {token}")

# Uninstall remains provider-non-invasive and persistent-state preserving by
# default. Direct provider mutations or unconditional state deletion fail gate.
require("platform uninstall-hook" in uninstall, "uninstall hook must route through native lifecycle backend")
require("rm -rf \"$STATE\"" not in uninstall and "rm -rf /data/adb/rclone-nexus" not in uninstall,
        "uninstall script must not unconditionally delete persistent state")
for path in list((ROOT / "module").rglob("*.sh")) + list((ROOT / "scripts").rglob("*.sh")):
    body = path.read_text(encoding="utf-8", errors="replace")
    if re.search(r"\b(rm|mv|cp|sed\s+-i|truncate)\b[^\n]*(modules/\$?RNEXUS_PROVIDER_MODULE_ID|modules/rclone)", body):
        errors.append(f"provider mutation detected: {path.relative_to(ROOT)}")

# Portable shell fixture: a corrupt update must stop after verify-integrity and
# must not invoke migration or create the service readiness marker.
with tempfile.TemporaryDirectory(prefix="rnx-platform-g1-") as td:
    temp = Path(td)
    mod = temp / "module"
    (mod / "lib").mkdir(parents=True)
    (mod / "system/bin").mkdir(parents=True)
    shutil.copy2(ROOT / "module/post-fs-data.sh", mod / "post-fs-data.sh")
    shutil.copy2(ROOT / "module/lib/common.sh", mod / "lib/common.sh")
    fake = mod / "system/bin/racctl"
    fake.write_text(
        "#!/bin/sh\n"
        "printf '%s\\n' \"$*\" >>\"$RNEXUS_TEST_CALLS\"\n"
        "case \"$*\" in\n"
        "  'platform verify-integrity') exit \"${RNEXUS_TEST_VERIFY_EXIT:-0}\" ;;\n"
        "  'platform migrate') : >\"$RNEXUS_TEST_MIGRATED\"; exit 0 ;;\n"
        "  *) exit 0 ;;\n"
        "esac\n",
        encoding="utf-8",
    )
    fake.chmod(0o755)
    env = os.environ.copy()
    env.update({
        "RNEXUS_STATE_DIR": str(temp / "state"),
        "RNEXUS_TEST_CALLS": str(temp / "calls"),
        "RNEXUS_TEST_MIGRATED": str(temp / "migrated"),
        "RNEXUS_TEST_VERIFY_EXIT": "7",
    })
    failed = subprocess.run(["sh", str(mod / "post-fs-data.sh")], env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    require(failed.returncode != 0, "post-fs-data must fail when integrity verification fails")
    require(not (temp / "migrated").exists(), "failed integrity verification must not migrate persistent state")
    require(not (temp / "state/run/platform-ready").exists(), "failed platform preflight must not create readiness marker")
    calls = (temp / "calls").read_text(encoding="utf-8").splitlines() if (temp / "calls").exists() else []
    require(calls == ["platform verify-integrity"], f"unexpected failed-preflight call order: {calls}")

    # Success path must be verify -> migrate and produce a private readiness marker.
    for rel in ("calls", "migrated"):
        try:
            (temp / rel).unlink()
        except FileNotFoundError:
            pass
    env["RNEXUS_TEST_VERIFY_EXIT"] = "0"
    ok = subprocess.run(["sh", str(mod / "post-fs-data.sh")], env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    require(ok.returncode == 0, f"successful platform preflight failed: {ok.stderr.strip()}")
    calls = (temp / "calls").read_text(encoding="utf-8").splitlines()
    require(calls == ["platform verify-integrity", "platform migrate"], f"successful preflight order incorrect: {calls}")
    ready = temp / "state/run/platform-ready"
    require(ready.is_file(), "successful platform preflight missing readiness marker")
    if ready.exists():
        require(stat.S_IMODE(ready.stat().st_mode) == 0o600, "platform readiness marker must be mode 0600")

# Once WEB-X01 exists, its own gate owns transport security. Before that point
# the platform substrate must remain static/unprivileged.
if not (ROOT / "docs/implementation/WEB-X01.md").exists():
    web = text("module/webroot/index.html")
    require(not re.search(r"\b(eval|sh\s+-c|su\s+-c)\b", web, re.I), "pre-WEB webroot exposes shell execution")
    require("fetch(" not in web and "XMLHttpRequest" not in web, "pre-WEB webroot must not invent an HTTP privilege transport")
else:
    require((ROOT / "scripts/dev/web_x01_gate.py").is_file(), "WEB-X01 must own the evolved WebUI trust boundary")

if errors:
    for error in errors:
        print(f"ERROR: {error}", file=sys.stderr)
    raise SystemExit(1)
print("PLATFORM-G1 audit: OK")
