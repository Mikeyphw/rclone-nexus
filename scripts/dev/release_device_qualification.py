#!/usr/bin/env python3
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[2]
DEFAULT = ROOT / "release" / "evidence" / "device-qualification.json"
CASES = [
    "reboot",
    "root_manager_restart",
    "provider_update_reload",
    "wifi_mobile_offline",
    "doze_screenoff_charging",
    "remote_outage_auth_recovery",
    "stale_fuse_killed_rclone",
    "daemon_crash_restart",
    "storage_remount_low_space",
    "webui_reopen_idle_expiry",
    "android_user_namespace_change",
    "simultaneous_mounts_jobs",
]


def command(argv: list[str], timeout: int = 8) -> str:
    try:
        p = subprocess.run(
            argv,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            timeout=timeout,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        return ""
    return p.stdout.strip() if p.returncode == 0 else ""


def parse_json(text: str):
    try:
        return json.loads(text) if text else None
    except json.JSONDecodeError:
        return None


def racctl_candidates() -> list[str]:
    candidates: list[str] = []
    explicit = os.environ.get("RNEXUS_RACCTL", "").strip()
    if explicit:
        candidates.append(explicit)
    discovered = shutil.which("racctl")
    if discovered:
        candidates.append(discovered)
    candidates.extend([
        "/system/bin/racctl",
        "/data/adb/modules/rclone_nexus/system/bin/racctl",
    ])
    out: list[str] = []
    for candidate in candidates:
        if candidate and candidate not in out:
            out.append(candidate)
    return out


def racctl_text(args: list[str], timeout: int = 12) -> str:
    # Prefer direct execution so a root shell/module namespace needs no extra
    # dependency. Native Termux normally lacks access to /data/adb state, so
    # retry the same fixed argv through su without enabling a shell subprocess mode.
    for binary in racctl_candidates():
        direct = command([binary, *args], timeout=timeout)
        if direct:
            return direct
        if shutil.which("su"):
            rooted = command(["su", "-c", shlex.join([binary, *args])], timeout=timeout)
            if rooted:
                return rooted
    return ""


def racctl_json(args: list[str]):
    return parse_json(racctl_text(args))


def prop(name: str) -> str:
    return command(["getprop", name])


def is_android() -> bool:
    return bool(prop("ro.build.version.sdk"))


def load(path: Path) -> dict:
    if not path.is_file():
        return {}
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
        return value if isinstance(value, dict) else {}
    except Exception:
        return {}


def private_write(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    try:
        path.chmod(0o600)
    except OSError:
        pass


def capture(path: Path, mounts: list[str]) -> None:
    if not is_android():
        raise SystemExit("release device evidence must be captured on Android/Termux")
    old = load(path)
    prior_cases = old.get("endurance_cases", {}) if isinstance(old.get("endurance_cases"), dict) else {}
    cases = {name: prior_cases.get(name, {"status": "pending", "note": ""}) for name in CASES}
    namespaces = {name: racctl_json(["namespace", "inspect", name]) for name in mounts}
    evidence = {
        "schema_version": 1,
        "campaign_position": "REL-X01",
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "device": {
            "manufacturer": prop("ro.product.manufacturer"),
            "model": prop("ro.product.model"),
            "device": prop("ro.product.device"),
            "android_release": prop("ro.build.version.release"),
            "sdk": prop("ro.build.version.sdk"),
            "fingerprint": prop("ro.build.fingerprint"),
            "kernel": platform.release(),
        },
        "nexus": racctl_json(["version", "--json"]),
        "root_manager": racctl_json(["platform", "root-manager"]),
        "provider": racctl_json(["compat", "nexus", "provider"]),
        "doctor": racctl_json(["doctor", "--json"]),
        "namespace_visibility": namespaces,
        "endurance_cases": cases,
    }
    private_write(path, evidence)
    print(path)


def record(path: Path, case: str, status: str, note: str) -> None:
    if case not in CASES:
        raise SystemExit(f"unknown case: {case}")
    data = load(path)
    if data.get("schema_version") != 1:
        raise SystemExit("capture evidence before recording cases")
    data.setdefault("endurance_cases", {})[case] = {
        "status": status,
        "note": note,
        "recorded_at": datetime.now(timezone.utc).isoformat(),
    }
    private_write(path, data)
    print(f"{case}: {status}")


def require_object(data: dict, key: str) -> dict:
    value = data.get(key)
    if not isinstance(value, dict) or not value:
        raise SystemExit(f"device evidence lacks {key}")
    return value


def validate(path: Path, require_complete: bool) -> None:
    data = load(path)
    if data.get("schema_version") != 1:
        raise SystemExit("invalid/missing device evidence")
    if data.get("campaign_position") != "REL-X01":
        raise SystemExit("device evidence campaign position mismatch")
    if not str(data.get("captured_at", "")).strip():
        raise SystemExit("device evidence lacks capture timestamp")

    device = require_object(data, "device")
    if not str(device.get("sdk", "")).strip() or not str(device.get("fingerprint", "")).strip():
        raise SystemExit("device evidence lacks Android SDK/fingerprint")

    nexus = require_object(data, "nexus")
    if nexus.get("version") != "v0.1.0":
        raise SystemExit(f"device evidence is not from Nexus v0.1.0: {nexus.get('version')!r}")

    manager = require_object(data, "root_manager")
    if not manager.get("compatible") or not str(manager.get("kind", "")).strip():
        raise SystemExit("root-manager evidence is not compatible/identified")

    provider = require_object(data, "provider")
    if provider.get("module_id") != "rclone" or not provider.get("ready"):
        raise SystemExit("provider evidence is not release-ready")
    if not provider.get("fuse_helper_ready") or not str(provider.get("rclone_version", "")).strip():
        raise SystemExit("provider evidence lacks FUSE helper/rclone version")

    doctor = require_object(data, "doctor")
    if doctor.get("overall") == "FAIL" or not isinstance(doctor.get("checks"), list):
        raise SystemExit("doctor evidence is failed or malformed")

    namespaces = data.get("namespace_visibility")
    if not isinstance(namespaces, dict):
        raise SystemExit("namespace visibility evidence must be an object")

    cases = data.get("endurance_cases", {})
    if not isinstance(cases, dict):
        raise SystemExit("endurance_cases must be an object")
    unknown = sorted(set(cases) - set(CASES))
    if unknown:
        raise SystemExit("unknown endurance cases: " + ", ".join(unknown))
    failed = [k for k, v in cases.items() if isinstance(v, dict) and v.get("status") == "fail"]
    pending = [k for k in CASES if not isinstance(cases.get(k), dict) or cases[k].get("status") not in {"pass", "skip", "fail"}]
    bad_skip = [
        k for k, v in cases.items()
        if isinstance(v, dict) and v.get("status") == "skip" and not str(v.get("note", "")).strip()
    ]
    if failed:
        raise SystemExit("failed endurance cases: " + ", ".join(failed))
    if bad_skip:
        raise SystemExit("skipped cases require a reason: " + ", ".join(bad_skip))
    if require_complete and pending:
        raise SystemExit("pending endurance cases: " + ", ".join(pending))
    print(json.dumps({"status": "ok", "pending": pending, "complete": not pending}, sort_keys=True))


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--file", type=Path, default=DEFAULT)
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("capture")
    c.add_argument("--mount", action="append", default=[])
    r = sub.add_parser("record")
    r.add_argument("case", choices=CASES)
    r.add_argument("status", choices=["pass", "fail", "skip"])
    r.add_argument("--note", default="")
    v = sub.add_parser("validate")
    v.add_argument("--require-complete", action="store_true")
    ns = ap.parse_args()
    if ns.cmd == "capture":
        capture(ns.file, ns.mount)
    elif ns.cmd == "record":
        record(ns.file, ns.case, ns.status, ns.note)
    else:
        validate(ns.file, ns.require_complete)


if __name__ == "__main__":
    main()
