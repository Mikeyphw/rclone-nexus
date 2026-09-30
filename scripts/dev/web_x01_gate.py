#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
import json
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
errors: list[str] = []

def require(cond: bool, message: str) -> None:
    if not cond:
        errors.append(message)

def text(rel: str) -> str:
    return (ROOT / rel).read_text(encoding="utf-8")

required = [
    "internal/webui/server.go",
    "internal/webui/runtime.go",
    "internal/webui/server_test.go",
    "module/webroot/index.html",
    "module/webroot/style.css",
    "module/webroot/app.js",
    "module/webroot/bridge.js",
    "module/webroot/platform.json",
    "module/action.sh",
    "docs/implementation/WEB-X01.md",
]
for rel in required:
    require((ROOT / rel).is_file(), f"missing WEB-X01 surface: {rel}")

html = text("module/webroot/index.html")
app = text("module/webroot/app.js")
bridge = text("module/webroot/bridge.js")
server = text("internal/webui/server.go")
runtime = text("internal/webui/runtime.go")
action = text("module/action.sh")
platform = json.loads(text("module/webroot/platform.json"))

require("Content-Security-Policy" in html, "static WebUI missing CSP")
for bad in ("http://", "https://", "//cdn", "unpkg", "jsdelivr"):
    require(bad not in html.lower(), f"static HTML contains network/CDN reference: {bad}")
for source, owner in ((app, "app.js"), (bridge, "bridge.js")):
    for bad in ("innerHTML", "outerHTML", "insertAdjacentHTML", "eval(", "new Function"):
        require(bad not in source, f"{owner} uses unsafe dynamic primitive: {bad}")
require("textContent" in app, "backend-derived UI text must use textContent")

for bad in ("shell.exec", "rclone.exec", "rc.call", "/api/v1/exec", "/api/v1/shell", "arbitrary-file"):
    require(bad not in (server + runtime + bridge), f"generic privileged WebUI surface found: {bad}")
require("127.0.0.1:0" in server and 'net.Listen("tcp4"' in server, "standalone server must bind ephemeral IPv4 loopback")
for token in ("SameSiteStrictMode", "HttpOnly: true", "X-Rclone-Nexus-CSRF", "Origin", "MaxHeaderBytes", "MaxBytesReader", "IdleTimeout", "X-Frame-Options", "Referrer-Policy", "nosniff"):
    require(token in server, f"standalone security contract missing: {token}")
require("ConstantTimeCompare" in server, "bootstrap/session secret checks must be constant-time")
require("admin_secret" in server and "0600" not in server, "runtime state contract unexpectedly malformed")
require("0o600" in server, "runtime state must be private")
require("processAlive" in server and "IssueFromExisting" in server, "standalone reuse/ownership contract missing")
require("webui start --open" in action, "module Action must start/reuse and open standalone WebUI")

require(platform.get("standalone_transport") is True, "platform metadata missing standalone transport")
require(platform.get("embedded_transport") == "capability-gated", "embedded transport must be capability gated")
for key in ("arbitrary_shell", "generic_exec", "generic_argv", "generic_rc"):
    require(platform.get(key) is False, f"platform metadata must deny {key}")

# Embedded manager transport may call the manager's native exec bridge only with
# one fixed racctl path and a bounded typed request envelope.
require("globalThis.ksu" in bridge and "typeof globalThis.ksu.exec === 'function'" in bridge, "real embedded bridge capability probe missing")
require("EMBEDDED_BINARY = '/data/adb/modules/rclone_nexus/system/bin/racctl'" in bridge, "embedded bridge backend path must be fixed")
require("webui bridge --request-base64" in bridge, "embedded bridge must use typed backend entry point")
require("sh -c" not in bridge and "bash -c" not in bridge, "embedded bridge must not expose shell -c")
require("standaloneProbe" in bridge and "selected = 'standalone'" in bridge, "standalone selection must use capability probe")
require("location.hostname" not in bridge and "localhost" not in bridge.lower(), "transport selection must not infer standalone from localhost hostname")

# Backend-side bridge accepts only protocol envelopes and rechecks descriptor class.
require("protocol.DecodeRequest" in runtime and "engine.Descriptor" in runtime, "embedded backend must revalidate typed protocol registry")
require("MaxRequestBytes" in runtime and "maxBridgeEncodedBytes" in runtime, "embedded request must be bounded")

if errors:
    for error in errors:
        print(f"ERROR: {error}", file=sys.stderr)
    raise SystemExit(1)
print("WEB-X01 audit: OK")
