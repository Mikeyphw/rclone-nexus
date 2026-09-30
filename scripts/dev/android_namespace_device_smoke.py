#!/usr/bin/env python3
"""Devtool-owned Android namespace visibility smoke.

The default path is read-only and emits truthful evidence. A controlled bind
smoke is opt-in through RNEXUS_DEVICE_SMOKE_MOUNT + RNEXUS_DEVICE_SMOKE_APPLY=1
and must run in a privileged Devtool execution destination. The script never
invokes adb/su itself.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys


def emit(**payload: object) -> None:
    print(json.dumps(payload, sort_keys=True))


def is_android() -> bool:
    return Path("/system/build.prop").exists() or bool(os.environ.get("ANDROID_ROOT"))


def racctl_path() -> Path | None:
    explicit = os.environ.get("RNEXUS_DEVICE_RACCTL")
    candidates = [
        Path(explicit) if explicit else None,
        Path("/data/adb/modules/rclone_nexus/system/bin/racctl"),
        Path("/data/adb/modules/rclone_nexus/libexec/racctl"),
    ]
    for candidate in candidates:
        if candidate and candidate.is_file() and os.access(candidate, os.X_OK):
            return candidate
    return None


def run_json(binary: Path, *args: str) -> dict:
    cp = subprocess.run([str(binary), *args], text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False, timeout=30)
    if cp.returncode != 0:
        raise RuntimeError(f"{' '.join(args)} failed: {cp.stderr.strip()}")
    return json.loads(cp.stdout)


def main() -> int:
    if not is_android():
        emit(schema_version=1, status="skipped", reason="not_android_execution_destination")
        return 0
    ns = ""
    try:
        ns = os.readlink("/proc/self/ns/mnt")
        mountinfo_lines = sum(1 for _ in open("/proc/self/mountinfo", "rb"))
    except OSError as exc:
        emit(schema_version=1, status="failed", reason="self_namespace_unreadable", error=str(exc))
        return 1

    binary = racctl_path()
    mount = os.environ.get("RNEXUS_DEVICE_SMOKE_MOUNT", "").strip()
    apply = os.environ.get("RNEXUS_DEVICE_SMOKE_APPLY") == "1"
    if binary is None or not mount:
        emit(
            schema_version=1,
            status="observed",
            namespace=ns,
            mountinfo_lines=mountinfo_lines,
            privileged=os.geteuid() == 0,
            propagation="not_requested",
            reason="set_RNEXUS_DEVICE_SMOKE_MOUNT_to_qualify_a_managed_mount",
        )
        return 0

    try:
        before = run_json(binary, "namespace", "inspect", mount)
        preview = run_json(binary, "namespace", "preview", mount)
        if not apply:
            emit(schema_version=1, status="observed", namespace=ns, mount=mount, preview=preview, before=before, propagation="preview_only")
            return 0
        if os.geteuid() != 0:
            emit(schema_version=1, status="skipped", reason="privileged_devtool_destination_required", mount=mount, preview=preview)
            return 0
        applied = run_json(binary, "namespace", "apply", mount)
        after = run_json(binary, "namespace", "inspect", mount)
        rolled_back = run_json(binary, "namespace", "rollback", mount)
        emit(schema_version=1, status="passed", namespace=ns, mount=mount, preview=preview, applied=applied, after=after, rollback=rolled_back)
        return 0
    except (RuntimeError, json.JSONDecodeError, subprocess.TimeoutExpired) as exc:
        emit(schema_version=1, status="failed", reason="device_namespace_smoke_failed", error=str(exc), mount=mount)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
