#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
errors: list[str] = []

required = [
    "internal/rootmgr/rootmgr.go",
    "internal/diagnostics/log.go",
    "internal/doctor/doctor.go",
    "internal/integrity/integrity.go",
    "internal/platformstate/state.go",
    "internal/platformlifecycle/lifecycle.go",
    "module/webroot/index.html",
    "module/webroot/platform.json",
]
for rel in required:
    if not (ROOT / rel).is_file():
        errors.append(f"missing PLATFORM-X01 surface: {rel}")

post = (ROOT / "module/post-fs-data.sh").read_text(encoding="utf-8")
service = (ROOT / "module/service.sh").read_text(encoding="utf-8")
customize = (ROOT / "module/customize.sh").read_text(encoding="utf-8")
uninstall = (ROOT / "module/uninstall.sh").read_text(encoding="utf-8")
action = (ROOT / "module/action.sh").read_text(encoding="utf-8")
web = (ROOT / "module/webroot/index.html").read_text(encoding="utf-8")

action_token = "webui start --open" if (ROOT / "docs/implementation/WEB-X01.md").exists() else "platform action"
for text, token, owner in [
    (customize, "platform validate-upgrade", "customize.sh"),
    (post, "platform migrate", "post-fs-data.sh"),
    (post, "platform verify-integrity", "post-fs-data.sh"),
    (service, "platform-ready", "service.sh"),
    (uninstall, "platform uninstall-hook", "uninstall.sh"),
    (action, action_token, "action.sh"),
]:
    if token not in text:
        errors.append(f"{owner} missing platform lifecycle token: {token}")

if "rm -rf /data/adb/rclone-nexus" in uninstall or "rm -rf \"$STATE\"" in uninstall:
    errors.append("uninstall.sh must not directly purge persistent state")
if re.search(r"\b(eval|sh\s+-c|su\s+-c)\b", web, re.I):
    errors.append("webroot foundation must not expose shell execution")
if "arbitrary_shell" not in (ROOT / "module/webroot/platform.json").read_text(encoding="utf-8"):
    errors.append("webroot platform capability metadata missing arbitrary-shell declaration")

# Provider module remains read-only/non-invasive: no scripts may delete, move or
# rewrite files below the provider module id.
for path in list((ROOT / "module").rglob("*.sh")) + list((ROOT / "scripts").rglob("*.sh")):
    text = path.read_text(encoding="utf-8", errors="replace")
    if re.search(r"\b(rm|mv|cp|sed\s+-i|truncate)\b[^\n]*(modules/\$?RNEXUS_PROVIDER_MODULE_ID|modules/rclone)", text):
        errors.append(f"provider module mutation detected: {path.relative_to(ROOT)}")

package_script = (ROOT / "scripts/dev/package_module.py").read_text(encoding="utf-8")
package_check = (ROOT / "scripts/dev/check-package.py").read_text(encoding="utf-8")
if "integrity.manifest.json" not in package_script or "sha256" not in package_script:
    errors.append("package builder does not emit integrity manifest")
if "integrity.manifest.json" not in package_check or "webroot/index.html" not in package_check:
    errors.append("package contract does not qualify integrity/webroot")

if errors:
    for error in errors:
        print(f"ERROR: {error}", file=sys.stderr)
    raise SystemExit(1)
print("PLATFORM-X01 audit: OK")
