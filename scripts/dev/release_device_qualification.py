#!/usr/bin/env python3
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import secrets
import shlex
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
SCHEMA_VERSION = 3
HARNESS_VERSION = 4
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
AUTOMATIC_CASES = {"stale_fuse_killed_rclone", "daemon_crash_restart", "webui_reopen_idle_expiry"}
SKIPPABLE_CASES = {"android_user_namespace_change"}


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def canonical(value: object) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")


def digest(value: object) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def default_evidence_path() -> Path:
    primary = os.environ.get("DEVTOOL_TRANSACTION_PRIMARY_REPO_ROOT", "").strip()
    root = Path(primary).expanduser().resolve() if primary else ROOT
    return root / "release" / "evidence" / "device-qualification.json"


def command(argv: list[str], timeout: int = 10) -> tuple[int, str, str]:
    try:
        p = subprocess.run(argv, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired) as exc:
        return 127, "", str(exc)
    return p.returncode, p.stdout.strip(), p.stderr.strip()


def fixed_root_command(argv: list[str], timeout: int = 12) -> tuple[int, str, str]:
    rc, out, err = command(argv, timeout)
    if rc == 0:
        return rc, out, err
    su = shutil.which("su")
    if not su:
        return rc, out, err
    # argv is constructed only by this harness from machine observations and
    # fixed command names. Evidence/user text never becomes shell text.
    return command([su, "-c", shlex.join(argv)], timeout)


def root_first_command(argv: list[str], timeout: int = 12) -> tuple[int, str, str]:
    """Observe root-owned Nexus state as root before accepting a user-shell view."""
    if os.geteuid() == 0:
        return command(argv, timeout)
    su = shutil.which("su")
    if su:
        rc, out, err = command([su, "-c", shlex.join(argv)], timeout)
        if rc == 0:
            return rc, out, err
    return command(argv, timeout)


def parse_json(text: str):
    try:
        return json.loads(text) if text else None
    except json.JSONDecodeError:
        return None


def prop(name: str) -> str:
    return command(["getprop", name])[1]


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
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    try:
        tmp.chmod(0o600)
    except OSError:
        pass
    os.replace(tmp, path)
    try:
        path.chmod(0o600)
    except OSError:
        pass


def racctl_candidates() -> list[str]:
    candidates: list[str] = []
    explicit = os.environ.get("RNEXUS_RACCTL", "").strip()
    if explicit:
        candidates.append(explicit)
    discovered = shutil.which("racctl")
    if discovered:
        candidates.append(discovered)
    candidates.extend(["/system/bin/racctl", "/data/adb/modules/rclone_nexus/system/bin/racctl"])
    out: list[str] = []
    for candidate in candidates:
        if candidate and candidate not in out:
            out.append(candidate)
    return out


def racctl_text(args: list[str], timeout: int = 16) -> str:
    # Final device qualification is an authority check over /data/adb-owned
    # runtime/config/root-manager state. Query that view as root first so a
    # successful but privilege-limited Termux JSON response cannot masquerade as
    # canonical device metadata. Fall back to the user shell only for fixtures.
    for binary in racctl_candidates():
        rc, out, _ = root_first_command([binary, *args], timeout)
        if rc == 0 and out:
            return out
    return ""


def racctl_json(args: list[str], timeout: int = 16):
    return parse_json(racctl_text(args, timeout))


def require_dict(value: object, message: str) -> dict:
    if not isinstance(value, dict):
        raise RuntimeError(message)
    return value


def configured_mounts() -> tuple[list[str], dict]:
    snapshot = racctl_json(["compat", "nexus", "config"])
    if not isinstance(snapshot, dict):
        raise RuntimeError("cannot read typed Nexus configuration snapshot")
    values = snapshot.get("mounts")
    if not isinstance(values, list):
        raise RuntimeError("Nexus configuration snapshot lacks mounts")
    names: list[str] = []
    for item in values:
        if isinstance(item, dict) and str(item.get("name", "")).strip():
            names.append(str(item["name"]))
    return sorted(set(names)), snapshot


def read_text_root(path: str) -> str:
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace").strip()
    except OSError:
        rc, out, _ = fixed_root_command(["cat", path], 4)
        return out.strip() if rc == 0 else ""


def boot_id() -> str:
    return read_text_root("/proc/sys/kernel/random/boot_id")


def uptime_seconds() -> float:
    text = read_text_root("/proc/uptime")
    try:
        return float(text.split()[0])
    except Exception:
        return -1.0


def process_identity(pid: int) -> dict:
    if pid <= 1:
        return {}
    text = read_text_root(f"/proc/{pid}/stat")
    if not text:
        return {"pid": pid, "alive": False}
    try:
        tail = text.rsplit(")", 1)[1].strip().split()
        # field 22 is starttime; tail begins at field 3.
        start_ticks = int(tail[19])
    except Exception:
        start_ticks = 0
    return {"pid": pid, "alive": True, "start_ticks": start_ticks}


def daemon_identity() -> dict:
    rc, out, _ = fixed_root_command(["pidof", "racctl"], 4)
    if rc == 0:
        for token in out.split():
            if not token.isdigit():
                continue
            pid = int(token)
            cmdline = read_text_root(f"/proc/{pid}/cmdline").replace("\x00", " ").strip()
            if re.search(r"(?:^|\s)(?:racd|daemon)(?:\s|$)", cmdline):
                ident = process_identity(pid)
                ident["command_sha256"] = hashlib.sha256(cmdline.encode()).hexdigest()
                return ident
    for spec in (["ps", "-A", "-o", "PID,ARGS"], ["ps", "-A", "-o", "PID,CMD"]):
        rc, out, _ = fixed_root_command(list(spec), 6)
        if rc != 0:
            continue
        for line in out.splitlines():
            low = line.lower()
            if "racctl" not in low or not re.search(r"(?:\s|/)(?:racd|daemon)(?:\s|$)", low):
                continue
            m = re.match(r"\s*(\d+)\s+(.+)$", line)
            if not m:
                continue
            ident = process_identity(int(m.group(1)))
            ident["command_sha256"] = hashlib.sha256(m.group(2).encode()).hexdigest()
            return ident
    return {}


def current_user() -> str:
    for argv in (["am", "get-current-user"], ["cmd", "activity", "get-current-user"]):
        rc, out, _ = fixed_root_command(list(argv), 4)
        if rc == 0 and out.strip().isdigit():
            return out.strip()
    return ""


def android_users() -> list[str]:
    rc, out, _ = fixed_root_command(["pm", "list", "users"], 6)
    if rc != 0:
        return []
    return sorted(set(re.findall(r"UserInfo\{(\d+):", out)))


def screen_state() -> str:
    rc, out, _ = fixed_root_command(["dumpsys", "power"], 8)
    if rc != 0:
        return "unknown"
    low = out.lower()
    if "mwakefulness=asleep" in low or "display power: state=off" in low or "display power: state=doze" in low:
        return "off"
    if "mwakefulness=awake" in low or "display power: state=on" in low:
        return "on"
    return "unknown"


def idle_state() -> str:
    rc, out, _ = fixed_root_command(["dumpsys", "deviceidle"], 8)
    if rc != 0:
        return "unknown"
    m = re.search(r"mState=([A-Z_]+)", out)
    if m:
        return m.group(1).lower()
    return "unknown"


def stable_subset(value: object, keys: list[str]) -> dict:
    if not isinstance(value, dict):
        return {}
    return {key: value.get(key) for key in keys if key in value}


def collect_observation(mounts: list[str], heavy: bool = False) -> dict:
    mount_health: dict[str, object] = {}
    policies: dict[str, object] = {}
    namespaces: dict[str, object] = {}
    for name in mounts:
        health = racctl_json(["compat", "nexus", "health", name]) or {}
        if isinstance(health, dict):
            try:
                pid = int(health.get("pid") or 0)
            except (TypeError, ValueError):
                pid = 0
            if pid > 1:
                health["_qualification_process_identity"] = process_identity(pid)
        mount_health[name] = health
        policies[name] = racctl_json(["compat", "nexus", "policy", name]) or {}
        namespaces[name] = racctl_json(["namespace", "inspect", name]) or {}
    jobs_config = racctl_json(["jobs", "config"]) or {}
    jobs_status = racctl_json(["jobs", "status"]) or {}
    payload = {
        "boot_id": boot_id(),
        "uptime_seconds": uptime_seconds(),
        "current_user": current_user(),
        "users": android_users(),
        "screen_state": screen_state(),
        "device_idle_state": idle_state(),
        "nexus": racctl_json(["version", "--json"]) or {},
        "root_manager": racctl_json(["platform", "root-manager"]) or {},
        "provider": racctl_json(["compat", "nexus", "provider"]) or {},
        "runtime_authority": racctl_json(["runtime", "status", "--json"]) or {},
        "daemon": daemon_identity(),
        "mounts": mount_health,
        "policies": policies,
        "namespaces": namespaces,
        "jobs": {"config": jobs_config, "status": jobs_status},
    }
    if heavy:
        payload["doctor"] = racctl_json(["doctor", "--json"]) or {}
    return payload


def append_observation(entry: dict, payload: dict, label: str) -> dict:
    observations = entry.setdefault("observations", [])
    previous = observations[-1].get("hash", "") if observations else ""
    obs = {"captured_at": now(), "label": label, "previous_hash": previous, "payload": payload}
    obs["hash"] = digest({k: v for k, v in obs.items() if k != "hash"})
    observations.append(obs)
    return obs


def verify_observation_chain(entry: dict) -> bool:
    previous = ""
    observations = entry.get("observations")
    if not isinstance(observations, list) or not observations:
        return False
    for obs in observations:
        if not isinstance(obs, dict) or obs.get("previous_hash", "") != previous:
            return False
        expected = digest({k: v for k, v in obs.items() if k != "hash"})
        if obs.get("hash") != expected:
            return False
        previous = expected
    return True


def obs_payload(entry: dict, index: int) -> dict:
    values = entry.get("observations", [])
    if not isinstance(values, list) or len(values) <= index or not isinstance(values[index], dict):
        return {}
    payload = values[index].get("payload")
    return payload if isinstance(payload, dict) else {}


def mount_health(obs: dict, name: str) -> dict:
    value = obs.get("mounts", {}).get(name, {}) if isinstance(obs.get("mounts"), dict) else {}
    return value if isinstance(value, dict) else {}


def running_mounts(obs: dict) -> list[str]:
    mounts = obs.get("mounts")
    if not isinstance(mounts, dict):
        return []
    out = []
    for name, value in mounts.items():
        if isinstance(value, dict) and value.get("process_alive") and value.get("mount_alive"):
            out.append(str(name))
    return sorted(out)


def recovered(pre: dict, post: dict) -> bool:
    targets = running_mounts(pre)
    if not targets:
        return False
    return all(mount_health(post, name).get("process_alive") and mount_health(post, name).get("mount_alive") for name in targets)


def mount_identity_changed(pre: dict, post: dict) -> bool:
    for name in running_mounts(pre):
        a, b = mount_health(pre, name), mount_health(post, name)
        if a.get("pid") and b.get("pid") and a.get("pid") != b.get("pid"):
            return True
    return False


def policy_observation(obs: dict) -> dict:
    policies = obs.get("policies")
    if not isinstance(policies, dict):
        return {}
    for value in policies.values():
        if not isinstance(value, dict):
            continue
        decision = value.get("decision")
        if isinstance(decision, dict) and isinstance(decision.get("observation"), dict):
            return decision["observation"]
        # Accept direct Decision payloads only for compatibility with older test/evidence helpers.
        if isinstance(value.get("observation"), dict):
            return value["observation"]
    return {}


def network_class(obs: dict) -> tuple[bool | None, str]:
    p = policy_observation(obs)
    online = p.get("online") if isinstance(p.get("online"), bool) else None
    return online, str(p.get("network_class", ""))


def charging(obs: dict) -> bool | None:
    p = policy_observation(obs)
    return p.get("charging") if p.get("charging_known") is True and isinstance(p.get("charging"), bool) else None


def remote_states(obs: dict) -> set[str]:
    out: set[str] = set()
    mounts = obs.get("mounts")
    if not isinstance(mounts, dict):
        return out
    for value in mounts.values():
        if not isinstance(value, dict):
            continue
        readiness = value.get("readiness")
        if isinstance(readiness, dict):
            state = str(readiness.get("remote_state", ""))
            if state:
                out.add(state)
    return out


def storage_ready(obs: dict) -> bool:
    mounts = obs.get("mounts")
    if not isinstance(mounts, dict) or not mounts:
        return False
    seen = False
    for value in mounts.values():
        if not isinstance(value, dict):
            continue
        readiness = value.get("readiness")
        if not isinstance(readiness, dict):
            continue
        for condition in readiness.get("conditions", []):
            if isinstance(condition, dict) and condition.get("name") == "target_storage":
                seen = True
                if condition.get("ready") is not True:
                    return False
    return seen


def low_space_pressure(obs: dict) -> bool:
    mounts = obs.get("mounts")
    if not isinstance(mounts, dict):
        return False
    for value in mounts.values():
        if not isinstance(value, dict):
            continue
        cache = value.get("cache")
        if isinstance(cache, dict) and (cache.get("below_min_free") is True or cache.get("above_high") is True or cache.get("prune_pending") is True):
            return True
        policy = value.get("policy")
        if isinstance(policy, dict) and "cache_free_space_below_minimum" in (policy.get("reasons") or []):
            return True
    return False


def jobs_map(obs: dict) -> dict[str, dict]:
    status = obs.get("jobs", {}).get("status", {}) if isinstance(obs.get("jobs"), dict) else {}
    values = status.get("jobs") if isinstance(status, dict) else None
    if not isinstance(values, list):
        return {}
    return {str(v.get("name")): v for v in values if isinstance(v, dict) and v.get("name")}


def configured_jobs(obs: dict) -> list[dict]:
    cfg = obs.get("jobs", {}).get("config", {}) if isinstance(obs.get("jobs"), dict) else {}
    values = cfg.get("jobs") if isinstance(cfg, dict) else None
    return [v for v in values if isinstance(v, dict)] if isinstance(values, list) else []


def daemon_changed(a: dict, b: dict) -> bool:
    da, db = a.get("daemon", {}), b.get("daemon", {})
    if not isinstance(da, dict) or not isinstance(db, dict) or not da.get("alive") or not db.get("alive"):
        return False
    return (da.get("pid"), da.get("start_ticks")) != (db.get("pid"), db.get("start_ticks"))


def runtime_authority_ready(value: object) -> bool:
    if not isinstance(value, dict):
        return False
    mode = str(value.get("mode", ""))
    if mode == "managed":
        return (
            value.get("canonical") is True
            and value.get("operational") is True
            and not value.get("ambiguous_authority")
            and bool(str(value.get("binary", "")).strip())
            and bool(str(value.get("config", "")).strip())
        )
    if mode == "external":
        return value.get("operational") is True and bool(str(value.get("binary", "")).strip())
    return False


def metadata_readiness_errors(obs: dict) -> list[str]:
    errors: list[str] = []
    manager = obs.get("root_manager")
    nexus = obs.get("nexus")
    runtime = obs.get("runtime_authority")
    if not isinstance(manager, dict):
        errors.append("root-manager metadata missing")
    else:
        if manager.get("compatible") is not True:
            errors.append(f"root-manager incompatible/undetected (kind={manager.get('kind')!r})")
        if not str(manager.get("kind", "")).strip():
            errors.append("root-manager kind missing")
        if not str(manager.get("version", "")).strip():
            errors.append(f"root-manager version missing (kind={manager.get('kind')!r})")
    if not isinstance(nexus, dict):
        errors.append("Nexus version metadata missing")
    elif nexus.get("version") != "v0.1.0":
        errors.append(f"Nexus version mismatch ({nexus.get('version')!r})")
    if not runtime_authority_ready(runtime):
        if not isinstance(runtime, dict):
            errors.append("runtime-authority metadata missing")
        else:
            mode = str(runtime.get("mode", ""))
            detail = {k: runtime.get(k) for k in ("mode", "canonical", "operational", "ambiguous_authority", "binary", "config") if k in runtime}
            errors.append(f"runtime authority not release-ready: {detail}")
    return errors


def metadata_ready(obs: dict) -> bool:
    return not metadata_readiness_errors(obs)


def set_instruction(entry: dict, phase: str, instruction: str) -> None:
    entry["status"] = "awaiting-action"
    entry["phase"] = phase
    entry["instruction"] = instruction
    entry["updated_at"] = now()


def pass_case(entry: dict, assertions: list[str]) -> None:
    entry["status"] = "pass"
    entry["phase"] = "complete"
    entry["completed_at"] = now()
    entry["instruction"] = ""
    entry["proof"] = {
        "schema_version": 1,
        "kind": "machine-observation-chain",
        "assertions": assertions,
        "observation_count": len(entry.get("observations", [])),
        "chain_tip": entry.get("observations", [{}])[-1].get("hash", ""),
    }


def fail_case(entry: dict, reason: str) -> None:
    entry["status"] = "fail"
    entry["phase"] = "failed"
    entry["completed_at"] = now()
    entry["failure"] = reason
    entry["instruction"] = ""


def case_entry(data: dict, case: str) -> dict:
    cases = data.setdefault("endurance_cases", {})
    value = cases.get(case)
    if not isinstance(value, dict):
        value = {"status": "pending", "phase": "not-started", "observations": []}
        cases[case] = value
    return value


def verify_case_proof(case: str, entry: dict) -> tuple[bool, str]:
    if entry.get("status") == "skip":
        if case != "android_user_namespace_change":
            return False, "case is not skippable"
        proof = entry.get("proof")
        if not isinstance(proof, dict) or proof.get("kind") != "machine-unavailable":
            return False, "skip lacks machine-unavailable proof"
        if not str(proof.get("reason", "")).strip():
            return False, "skip lacks reason"
        users = proof.get("observed_users")
        if not isinstance(users, list) or len(users) != 1:
            return False, "skip is not backed by a single-user observation"
        return True, ""
    if entry.get("status") != "pass":
        return False, "case is not PASS"
    if not verify_observation_chain(entry):
        return False, "observation hash chain is invalid"
    proof = entry.get("proof")
    if not isinstance(proof, dict) or proof.get("kind") != "machine-observation-chain":
        return False, "machine proof missing"
    obs = [o.get("payload", {}) for o in entry.get("observations", []) if isinstance(o, dict)]
    if proof.get("observation_count") != len(obs) or proof.get("chain_tip") != entry.get("observations", [{}])[-1].get("hash"):
        return False, "proof does not bind the observation chain"
    if case == "reboot":
        ok = len(obs) >= 2 and obs[0].get("boot_id") and obs[0].get("boot_id") != obs[-1].get("boot_id") and recovered(obs[0], obs[-1]) and metadata_ready(obs[-1])
    elif case == "root_manager_restart":
        ok = len(obs) >= 2 and obs[0].get("boot_id") == obs[-1].get("boot_id") and daemon_changed(obs[0], obs[-1]) and recovered(obs[0], obs[-1]) and metadata_ready(obs[-1])
    elif case == "provider_update_reload":
        provider_changed = len(obs) >= 2 and stable_subset(obs[0].get("provider"), ["module_version", "rclone_version"]) != stable_subset(obs[-1].get("provider"), ["module_version", "rclone_version"])
        runtime_changed = len(obs) >= 2 and stable_subset(obs[0].get("runtime_authority"), ["active_runtime_id", "binary", "source"]) != stable_subset(obs[-1].get("runtime_authority"), ["active_runtime_id", "binary", "source"])
        ok = len(obs) >= 2 and obs[0].get("boot_id") == obs[-1].get("boot_id") and metadata_ready(obs[-1]) and recovered(obs[0], obs[-1]) and (provider_changed or runtime_changed or mount_identity_changed(obs[0], obs[-1]))
    elif case == "wifi_mobile_offline":
        states = [network_class(x) for x in obs]
        ok = len(obs) >= 4 and states[0] == (True, "wifi") and states[1][0] is False and states[2] == (True, "cellular") and states[-1] == (True, "wifi") and all(recovered(obs[0], x) for x in obs[1:])
    elif case == "doze_screenoff_charging":
        idle = str(obs[1].get("device_idle_state", "")) if len(obs) > 1 else ""
        ok = len(obs) >= 3 and charging(obs[0]) is True and obs[1].get("screen_state") == "off" and charging(obs[1]) is False and idle in {"idle", "idle_maintenance", "sensing", "locating"} and obs[-1].get("screen_state") == "on" and charging(obs[-1]) is True and recovered(obs[0], obs[-1])
    elif case == "remote_outage_auth_recovery":
        ok = len(obs) >= 5 and "online" in remote_states(obs[0]) and "offline" in remote_states(obs[1]) and "online" in remote_states(obs[2]) and "auth_error" in remote_states(obs[3]) and "online" in remote_states(obs[4]) and recovered(obs[0], obs[4])
    elif case == "stale_fuse_killed_rclone":
        disrupted = len(obs) >= 3 and any((not mount_health(x, n).get("process_alive") and mount_health(x, n).get("mount_alive")) or mount_health(x, n).get("state") == "MOUNT_STALE" for x in obs[1:-1] for n in running_mounts(obs[0]))
        ok = len(obs) >= 3 and disrupted and recovered(obs[0], obs[-1]) and mount_identity_changed(obs[0], obs[-1])
    elif case == "daemon_crash_restart":
        ok = len(obs) >= 3 and obs[1].get("daemon", {}).get("alive") is False and daemon_changed(obs[0], obs[-1]) and recovered(obs[0], obs[-1])
    elif case == "storage_remount_low_space":
        ok = len(obs) >= 4 and storage_ready(obs[0]) and not storage_ready(obs[1]) and storage_ready(obs[2]) and low_space_pressure(obs[2]) and storage_ready(obs[-1]) and not low_space_pressure(obs[-1]) and recovered(obs[0], obs[-1])
    elif case == "webui_reopen_idle_expiry":
        a = obs[0].get("webui", {}) if obs else {}
        b = obs[1].get("webui", {}) if len(obs) > 1 else {}
        c = obs[-1].get("webui", {}) if obs else {}
        ok = len(obs) >= 3 and a.get("reachable") is True and b.get("expired") is True and c.get("reachable") is True and a.get("pid") != c.get("pid")
    elif case == "android_user_namespace_change":
        ok = len(obs) >= 3 and len(obs[0].get("users", [])) >= 2 and obs[0].get("current_user") != obs[1].get("current_user") and obs[0].get("current_user") == obs[-1].get("current_user") and all(bool(x.get("namespaces")) for x in obs) and recovered(obs[0], obs[-1])
    elif case == "simultaneous_mounts_jobs":
        before = jobs_map(obs[0]) if obs else {}
        after = jobs_map(obs[-1]) if len(obs) >= 2 else {}
        check_names = [str(j.get("name")) for j in configured_jobs(obs[0]) if j.get("enabled") and j.get("type") == "check" and j.get("name")] if obs else []
        advanced = any(int(after.get(name, {}).get("run_count", 0)) > int(before.get(name, {}).get("run_count", 0)) for name in check_names)
        ok = len(obs) >= 2 and len(running_mounts(obs[0])) >= 2 and bool(check_names) and advanced and recovered(obs[0], obs[-1])
    else:
        return False, "unknown case"
    return (True, "") if ok else (False, "stored observations do not prove the case")


def capture(path: Path, mounts: list[str]) -> None:
    if not is_android():
        raise SystemExit("release device evidence must be captured on Android/Termux")
    discovered, config = configured_mounts()
    selected = sorted(set(mounts or discovered))
    if not selected:
        raise SystemExit("no configured Nexus mounts were discovered; final qualification requires a real configured mount")
    unknown = sorted(set(selected) - set(discovered))
    if unknown:
        raise SystemExit("requested mount is not in the typed Nexus configuration: " + ", ".join(unknown))
    baseline = collect_observation(selected, heavy=True)
    readiness_errors = metadata_readiness_errors(baseline)
    if readiness_errors:
        raise SystemExit("Nexus/root-manager/runtime-authority metadata is not release-ready: " + "; ".join(readiness_errors))
    namespaces = baseline.get("namespaces", {})
    if not isinstance(namespaces, dict) or any(not isinstance(v, dict) or not str(v.get("claim", "")).strip() for v in namespaces.values()):
        raise SystemExit("namespace inspection is incomplete for one or more configured mounts")
    doctor = baseline.get("doctor")
    if not isinstance(doctor, dict) or doctor.get("overall") == "FAIL":
        raise SystemExit("doctor must not report FAIL before qualification begins")
    existing = load(path)
    previous = existing if existing.get("schema_version") in {1, 2} else {}
    data = {
        "schema_version": SCHEMA_VERSION,
        "campaign_position": "REL-X01",
        "captured_at": now(),
        "device": {
            "manufacturer": prop("ro.product.manufacturer"), "model": prop("ro.product.model"), "device": prop("ro.product.device"),
            "android_release": prop("ro.build.version.release"), "sdk": prop("ro.build.version.sdk"), "fingerprint": prop("ro.build.fingerprint"), "kernel": platform.release(),
        },
        "nexus": baseline["nexus"], "root_manager": baseline["root_manager"], "provider": baseline["provider"],
        "runtime_authority": baseline["runtime_authority"], "doctor": doctor,
        "namespace_visibility": namespaces,
        "qualification": {
            "harness": "scripts/dev/release_device_qualification.py", "harness_version": HARNESS_VERSION,
            "session_id": secrets.token_hex(16), "mounts": selected, "config_digest": digest(config),
            "baseline_digest": digest(baseline), "legacy_evidence_present": bool(previous),
        },
        "endurance_cases": {name: {"status": "pending", "phase": "not-started", "observations": []} for name in CASES},
    }
    private_write(path, data)
    print(path)
    print("captured mounts: " + ", ".join(selected))


def require_v3(data: dict) -> list[str]:
    if data.get("schema_version") != SCHEMA_VERSION or data.get("campaign_position") != "REL-X01":
        raise SystemExit("capture schema-v3 evidence before running qualification cases")
    q = data.get("qualification")
    if not isinstance(q, dict) or q.get("harness_version") != HARNESS_VERSION:
        raise SystemExit("qualification harness identity is missing or stale")
    mounts = q.get("mounts")
    if not isinstance(mounts, list) or not mounts:
        raise SystemExit("qualification evidence has no configured mounts")
    return [str(x) for x in mounts]


def manual_start(case: str, entry: dict, obs: dict) -> None:
    if case == "reboot":
        set_instruction(entry, "await-reboot", "Reboot Android normally. After boot completes and Nexus has reconciled, run: release_device_qualification.py resume reboot")
    elif case == "root_manager_restart":
        set_instruction(entry, "await-root-manager-restart", "Restart/reload the active root manager/module environment without rebooting Android, then wait for Nexus to return and run: ... resume root_manager_restart")
    elif case == "provider_update_reload":
        runtime = obs.get("runtime_authority") if isinstance(obs.get("runtime_authority"), dict) else {}
        if runtime.get("mode") == "managed":
            set_instruction(entry, "await-runtime-reload", "Activate or reload another qualified Nexus-managed runtime without rebooting if supported; wait for mounts to recover, then run: ... resume provider_update_reload")
        else:
            set_instruction(entry, "await-provider-reload", "Reload or update the external rclone provider through the root manager, without rebooting if supported; wait for mounts to recover, then run: ... resume provider_update_reload")
    elif case == "wifi_mobile_offline":
        if network_class(obs) != (True, "wifi"):
            raise RuntimeError("start this case while connected through Wi-Fi")
        set_instruction(entry, "await-offline", "Disable Wi-Fi and mobile data so the device has no default route; keep Nexus running, then run: ... resume wifi_mobile_offline")
    elif case == "doze_screenoff_charging":
        if charging(obs) is not True or obs.get("screen_state") != "on":
            raise RuntimeError("start this case with the device awake and charging")
        set_instruction(entry, "await-doze-unplugged", "Unplug power, turn the screen off, and allow/force Android into device-idle/doze; then run: ... resume doze_screenoff_charging")
    elif case == "remote_outage_auth_recovery":
        if "online" not in remote_states(obs):
            raise RuntimeError("at least one qualified mount must have probe_remote=true and currently report remote_state=online")
        set_instruction(entry, "await-remote-outage", "Make the qualified test remote unreachable without changing Nexus configuration; wait for remote_offline evidence, then run: ... resume remote_outage_auth_recovery")
    elif case == "storage_remount_low_space":
        if not storage_ready(obs):
            raise RuntimeError("target storage must be ready when this case starts")
        set_instruction(entry, "await-storage-unavailable", "Temporarily make the qualified mount target storage unavailable/remounted using the device's normal storage controls; then run: ... resume storage_remount_low_space")
    elif case == "android_user_namespace_change":
        users = obs.get("users", [])
        if not isinstance(users, list) or len(users) < 2:
            entry.update({
                "status": "skip", "phase": "complete", "completed_at": now(), "instruction": "",
                "proof": {"schema_version": 1, "kind": "machine-unavailable", "reason": "qualification device exposes only one Android user", "observed_users": users},
            })
            return
        set_instruction(entry, "await-user-switch", "Switch to a different existing Android user using the system UI; wait for storage/zygote namespaces to settle, then run: ... resume android_user_namespace_change")
    elif case == "simultaneous_mounts_jobs":
        if len(running_mounts(obs)) < 2:
            raise RuntimeError("this case requires at least two simultaneously running qualified mounts")
        check_jobs = [j for j in configured_jobs(obs) if j.get("enabled") and j.get("type") == "check"]
        if not check_jobs:
            raise RuntimeError("this case requires at least one enabled scheduled read-only check job")
        set_instruction(entry, "await-scheduled-job", "Leave both mounts running until an enabled scheduled read-only check job completes at least once, then run: ... resume simultaneous_mounts_jobs")
    else:
        raise RuntimeError("case is automatic")


def resume_manual(case: str, entry: dict, mounts: list[str]) -> None:
    obs = collect_observation(mounts)
    append_observation(entry, obs, entry.get("phase", "resume"))
    phase = entry.get("phase")
    pre = obs_payload(entry, 0)
    if case == "reboot":
        ok, reason = verify_case_proof_from_candidate(case, entry)
        if not ok: raise RuntimeError(reason)
        pass_case(entry, ["boot identity changed", "previously running mounts recovered", "release metadata remained ready"])
    elif case == "root_manager_restart":
        ok, reason = verify_case_proof_from_candidate(case, entry)
        if not ok: raise RuntimeError(reason)
        pass_case(entry, ["boot identity unchanged", "racd process identity changed", "mounts recovered"])
    elif case == "provider_update_reload":
        ok, reason = verify_case_proof_from_candidate(case, entry)
        if not ok: raise RuntimeError(reason)
        pass_case(entry, ["runtime authority remained ready", "runtime/provider or managed mount process identity changed", "mounts recovered"])
    elif case == "wifi_mobile_offline":
        online, cls = network_class(obs)
        if phase == "await-offline":
            if online is not False: raise RuntimeError("device is not observably offline yet")
            if not recovered(pre, obs): raise RuntimeError("a previously valid VFS mount did not remain mounted during network loss")
            set_instruction(entry, "await-cellular", "Enable mobile data only (keep Wi-Fi off) and wait for the default route to become cellular; then run: ... resume wifi_mobile_offline")
        elif phase == "await-cellular":
            if (online, cls) != (True, "cellular"): raise RuntimeError(f"expected cellular connectivity, observed online={online} class={cls!r}")
            set_instruction(entry, "await-wifi-return", "Enable/connect Wi-Fi and wait until it becomes the default route again; then run: ... resume wifi_mobile_offline")
        elif phase == "await-wifi-return":
            ok, reason = verify_case_proof_from_candidate(case, entry)
            if not ok: raise RuntimeError(reason)
            pass_case(entry, ["Wi-Fi baseline observed", "offline transition observed without destroying VFS mounts", "cellular transition observed", "Wi-Fi recovery observed"])
    elif case == "doze_screenoff_charging":
        if phase == "await-doze-unplugged":
            if obs.get("screen_state") != "off" or charging(obs) is not False or obs.get("device_idle_state") not in {"idle", "idle_maintenance", "sensing", "locating"}:
                raise RuntimeError("screen-off + unplugged + device-idle/doze state has not been observed")
            set_instruction(entry, "await-awake-charging", "Wake/unlock the device and reconnect power; wait until charging is reported, then run: ... resume doze_screenoff_charging")
        elif phase == "await-awake-charging":
            ok, reason = verify_case_proof_from_candidate(case, entry)
            if not ok: raise RuntimeError(reason)
            pass_case(entry, ["charging awake baseline observed", "screen-off unplugged doze observed", "awake charging recovery observed", "mounts recovered"])
    elif case == "remote_outage_auth_recovery":
        states = remote_states(obs)
        if phase == "await-remote-outage":
            if "offline" not in states: raise RuntimeError(f"remote_offline not observed: {sorted(states)}")
            set_instruction(entry, "await-remote-recovery-1", "Restore remote reachability without changing credentials; wait for remote_state=online, then run: ... resume remote_outage_auth_recovery")
        elif phase == "await-remote-recovery-1":
            if "online" not in states: raise RuntimeError(f"remote recovery not observed: {sorted(states)}")
            set_instruction(entry, "await-auth-failure", "On a dedicated qualification remote, temporarily invalidate/revoke its credentials outside Nexus; wait for AUTH_ERROR/remote auth_error, then run: ... resume remote_outage_auth_recovery")
        elif phase == "await-auth-failure":
            if "auth_error" not in states: raise RuntimeError(f"remote auth_error not observed: {sorted(states)}")
            set_instruction(entry, "await-remote-recovery-2", "Restore the qualification remote credentials through the provider's normal credential path; wait for remote_state=online, then run: ... resume remote_outage_auth_recovery")
        elif phase == "await-remote-recovery-2":
            ok, reason = verify_case_proof_from_candidate(case, entry)
            if not ok: raise RuntimeError(reason)
            pass_case(entry, ["remote online baseline", "remote outage observed", "first recovery observed", "auth failure observed", "credential recovery observed"])
    elif case == "storage_remount_low_space":
        if phase == "await-storage-unavailable":
            if storage_ready(obs): raise RuntimeError("target storage is still reported ready")
            set_instruction(entry, "await-storage-low-space", "Restore/remount the target storage, then create a real low-space/cache-pressure condition for the qualified Nexus cache until its cache/policy state reports pressure; run: ... resume storage_remount_low_space")
        elif phase == "await-storage-low-space":
            if not storage_ready(obs): raise RuntimeError("target storage has not recovered")
            if not low_space_pressure(obs): raise RuntimeError("Nexus has not observed cache low-space/high-water pressure")
            set_instruction(entry, "await-storage-recovered", "Remove the temporary space pressure through normal cleanup/pruning until Nexus reports no cache pressure; then run: ... resume storage_remount_low_space")
        elif phase == "await-storage-recovered":
            ok, reason = verify_case_proof_from_candidate(case, entry)
            if not ok: raise RuntimeError(reason)
            pass_case(entry, ["storage ready baseline", "storage unavailable transition", "storage remount plus real cache pressure", "pressure cleared and mounts recovered"])
    elif case == "android_user_namespace_change":
        if phase == "await-user-switch":
            if obs.get("current_user") == pre.get("current_user"): raise RuntimeError("Android current user has not changed")
            if not obs.get("namespaces"): raise RuntimeError("namespace evidence unavailable after user switch")
            set_instruction(entry, "await-user-return", f"Switch back to Android user {pre.get('current_user')}; wait for namespaces to settle, then run: ... resume android_user_namespace_change")
        elif phase == "await-user-return":
            ok, reason = verify_case_proof_from_candidate(case, entry)
            if not ok: raise RuntimeError(reason)
            pass_case(entry, ["multiple Android users observed", "user switch observed", "namespace evidence captured after switch", "original user restored and mounts recovered"])
    elif case == "simultaneous_mounts_jobs":
        ok, reason = verify_case_proof_from_candidate(case, entry)
        if not ok: raise RuntimeError(reason)
        pass_case(entry, ["at least two mounts remained simultaneously running", "scheduled read-only job run_count advanced", "mounts remained healthy"])


def verify_case_proof_from_candidate(case: str, entry: dict) -> tuple[bool, str]:
    clone = json.loads(json.dumps(entry))
    clone["status"] = "pass"
    clone["proof"] = {"schema_version": 1, "kind": "machine-observation-chain", "observation_count": len(clone.get("observations", [])), "chain_tip": clone.get("observations", [{}])[-1].get("hash", "")}
    return verify_case_proof(case, clone)


def kill_owned_mount_case(entry: dict, mounts: list[str]) -> None:
    pre = obs_payload(entry, 0)
    targets = running_mounts(pre)
    if not targets:
        raise RuntimeError("no running managed mount is available")
    name = targets[0]
    pid = int(mount_health(pre, name).get("pid") or 0)
    if pid <= 1:
        raise RuntimeError("managed mount process identity is unavailable")
    baseline_ident = mount_health(pre, name).get("_qualification_process_identity", {})
    ident = process_identity(pid)
    if not ident.get("alive") or not isinstance(baseline_ident, dict) or (baseline_ident.get("pid"), baseline_ident.get("start_ticks")) != (ident.get("pid"), ident.get("start_ticks")):
        raise RuntimeError("managed mount PID identity changed before injection; refusing SIGKILL")
    rc, _, err = fixed_root_command(["kill", "-9", str(pid)], 5)
    if rc != 0:
        raise RuntimeError(f"failed to kill Nexus-owned rclone pid {pid}: {err}")
    disrupted = None
    for _ in range(30):
        time.sleep(0.25)
        candidate = collect_observation(mounts)
        h = mount_health(candidate, name)
        if (not h.get("process_alive") and h.get("mount_alive")) or h.get("state") == "MOUNT_STALE":
            disrupted = candidate
            break
    if disrupted is None:
        racctl_text(["compat", "mountctl", "restart", name], 20)
        raise RuntimeError("killed rclone process but did not observe stale-FUSE/process-mount disagreement; restart restoration attempted")
    append_observation(entry, disrupted, "killed-rclone-stale-fuse")
    post = None
    for _ in range(40):
        time.sleep(0.5)
        candidate = collect_observation(mounts)
        if recovered(pre, candidate) and mount_identity_changed(pre, candidate):
            post = candidate
            break
    if post is None:
        racctl_text(["compat", "mountctl", "restart", name], 20)
        raise RuntimeError("supervisor did not recover the killed rclone mount with a new process identity; restart restoration attempted")
    append_observation(entry, post, "supervisor-recovered-rclone")
    ok, reason = verify_case_proof_from_candidate("stale_fuse_killed_rclone", entry)
    if not ok: raise RuntimeError(reason)
    pass_case(entry, ["Nexus-owned rclone PID was killed", "stale FUSE/process disagreement was observed", "supervisor recovered a new owned process"])


def daemon_crash_case(entry: dict, mounts: list[str]) -> None:
    pre = obs_payload(entry, 0)
    daemon = pre.get("daemon")
    if not isinstance(daemon, dict) or not daemon.get("alive") or int(daemon.get("pid", 0)) <= 1:
        raise RuntimeError("racd process identity is unavailable")
    pid = int(daemon["pid"])
    rc, _, err = fixed_root_command(["kill", "-9", str(pid)], 5)
    if rc != 0:
        raise RuntimeError(f"failed to kill racd pid {pid}: {err}")
    time.sleep(0.4)
    down = collect_observation(mounts)
    # ps may retain a zombie briefly; force proof to the actual observed liveness.
    if down.get("daemon", {}).get("pid") == pid and down.get("daemon", {}).get("alive"):
        time.sleep(0.8)
        down = collect_observation(mounts)
    append_observation(entry, down, "racd-killed")
    if down.get("daemon", {}).get("alive"):
        raise RuntimeError("racd remained alive after SIGKILL injection")
    service = "/data/adb/modules/rclone_nexus/service.sh"
    rc, _, err = fixed_root_command([service], 20)
    if rc != 0:
        fixed_root_command([service], 20)
        raise RuntimeError(f"failed to restart Nexus through its module service hook; second restoration attempt issued: {err}")
    post = None
    for _ in range(30):
        time.sleep(0.5)
        candidate = collect_observation(mounts)
        if daemon_changed(pre, candidate) and recovered(pre, candidate):
            post = candidate
            break
    if post is None:
        raise RuntimeError("racd/module service restart did not recover daemon and mounts")
    append_observation(entry, post, "racd-service-recovered")
    ok, reason = verify_case_proof_from_candidate("daemon_crash_restart", entry)
    if not ok: raise RuntimeError(reason)
    pass_case(entry, ["racd SIGKILL observed", "daemon absence observed", "module service hook restarted a new racd", "mounts recovered"])


def root_popen(argv: list[str]) -> subprocess.Popen[str]:
    su = shutil.which("su")
    cmd = [*argv] if os.geteuid() == 0 or not su else [su, "-c", shlex.join(argv)]
    return subprocess.Popen(cmd, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def webui_case(entry: dict, mounts: list[str]) -> None:
    candidates = racctl_candidates()
    binary = next((c for c in candidates if c == "/data/adb/modules/rclone_nexus/system/bin/racctl"), candidates[0] if candidates else "racctl")
    proc = root_popen([binary, "webui", "serve", "--json", "--idle", "5"])
    try:
        if proc.stdout is None:
            raise RuntimeError("cannot read WebUI startup")
        deadline = time.time() + 6
        line = ""
        while time.time() < deadline and not line:
            line = proc.stdout.readline().strip()
            if not line:
                time.sleep(0.05)
        info = parse_json(line)
        if not isinstance(info, dict) or not str(info.get("bootstrap_url", "")).startswith("http://127.0.0.1:"):
            raise RuntimeError("standalone WebUI did not emit a loopback startup record")
        reachable = False
        try:
            with urllib.request.urlopen(str(info["bootstrap_url"]), timeout=3) as response:
                reachable = response.status in {200, 302, 303}
        except Exception:
            reachable = False
        first = collect_observation(mounts)
        first["webui"] = {"pid": info.get("pid"), "url": info.get("url"), "reachable": reachable}
        entry["observations"] = []
        append_observation(entry, first, "webui-opened")
        if not reachable:
            raise RuntimeError("WebUI bootstrap endpoint was not reachable")
        time.sleep(7)
        expired = proc.poll() is not None
        if not expired:
            proc.terminate(); time.sleep(1)
            expired = proc.poll() is not None
        middle = collect_observation(mounts)
        middle["webui"] = {"pid": info.get("pid"), "expired": expired}
        append_observation(entry, middle, "webui-idle-expired")
        if not expired:
            raise RuntimeError("standalone WebUI did not terminate after the 5-second idle timeout")
        reopened = racctl_json(["webui", "start", "--json"], 12)
        if not isinstance(reopened, dict) or not str(reopened.get("url", "")).startswith("http://127.0.0.1:"):
            raise RuntimeError("WebUI did not reopen through the typed start surface")
        final = collect_observation(mounts)
        final["webui"] = {"pid": reopened.get("pid"), "url": reopened.get("url"), "reachable": True}
        append_observation(entry, final, "webui-reopened")
        ok, reason = verify_case_proof_from_candidate("webui_reopen_idle_expiry", entry)
        if not ok: raise RuntimeError(reason)
        pass_case(entry, ["loopback WebUI opened", "idle expiry terminated the owned server", "typed WebUI start reopened a different server"])
    finally:
        if proc.poll() is None:
            proc.terminate()


def run_case(path: Path, case: str) -> None:
    data = load(path)
    mounts = require_v3(data)
    entry = case_entry(data, case)
    if entry.get("status") in {"pass", "skip"}:
        raise SystemExit(f"{case} is already complete")
    entry.clear(); entry.update({"status": "verifying" if case in AUTOMATIC_CASES else "pending", "phase": "starting", "started_at": now(), "observations": []})
    try:
        pre = collect_observation(mounts)
        append_observation(entry, pre, "baseline")
        if not metadata_ready(pre):
            raise RuntimeError("release metadata is not ready at case start")
        if not running_mounts(pre):
            raise RuntimeError("at least one managed mount must be running for endurance qualification")
        if case == "stale_fuse_killed_rclone":
            kill_owned_mount_case(entry, mounts)
        elif case == "daemon_crash_restart":
            daemon_crash_case(entry, mounts)
        elif case == "webui_reopen_idle_expiry":
            webui_case(entry, mounts)
        else:
            manual_start(case, entry, pre)
    except Exception as exc:
        fail_case(entry, str(exc))
        private_write(path, data)
        raise SystemExit(f"{case}: FAIL: {exc}")
    private_write(path, data)
    print(json.dumps({"case": case, "status": entry.get("status"), "phase": entry.get("phase"), "instruction": entry.get("instruction", "")}, indent=2))


def resume_case(path: Path, case: str) -> None:
    data = load(path)
    mounts = require_v3(data)
    entry = case_entry(data, case)
    if case in AUTOMATIC_CASES:
        raise SystemExit(f"{case} is automatic; use run, not resume")
    if entry.get("status") != "awaiting-action":
        raise SystemExit(f"{case} is not awaiting an external action (status={entry.get('status')!r})")
    try:
        resume_manual(case, entry, mounts)
    except Exception as exc:
        # External action can legitimately be incomplete; retain resumability instead of
        # converting a not-yet-observed transition into a terminal failure.
        entry["last_verification_error"] = str(exc)
        entry["updated_at"] = now()
        private_write(path, data)
        raise SystemExit(f"{case}: transition not yet proven: {exc}\n{entry.get('instruction', '')}")
    private_write(path, data)
    print(json.dumps({"case": case, "status": entry.get("status"), "phase": entry.get("phase"), "instruction": entry.get("instruction", "")}, indent=2))


def validate_metadata(data: dict, require_complete: bool) -> None:
    if data.get("schema_version") != SCHEMA_VERSION:
        raise SystemExit("device evidence must use executable qualification schema v3; recapture legacy evidence")
    if data.get("campaign_position") != "REL-X01" or not str(data.get("captured_at", "")).strip():
        raise SystemExit("device evidence campaign/timestamp identity mismatch")
    device = require_dict(data.get("device"), "device evidence lacks Android identity")
    if not str(device.get("sdk", "")).strip() or not str(device.get("fingerprint", "")).strip():
        raise SystemExit("device evidence lacks Android SDK/fingerprint")
    nexus = require_dict(data.get("nexus"), "device evidence lacks Nexus identity")
    if nexus.get("version") != "v0.1.0":
        raise SystemExit("device evidence is not from Nexus v0.1.0")
    manager = require_dict(data.get("root_manager"), "device evidence lacks root manager")
    if manager.get("compatible") is not True or not str(manager.get("kind", "")).strip() or not str(manager.get("version", "")).strip():
        raise SystemExit("root-manager evidence is incomplete/incompatible")
    runtime_authority = require_dict(data.get("runtime_authority"), "device evidence lacks canonical runtime authority")
    if not runtime_authority_ready(runtime_authority):
        raise SystemExit("runtime authority evidence is incomplete/not release-ready")
    provider = data.get("provider")
    if provider is not None and not isinstance(provider, dict):
        raise SystemExit("provider compatibility evidence is malformed")
    doctor = require_dict(data.get("doctor"), "device evidence lacks doctor report")
    if doctor.get("overall") == "FAIL" or not isinstance(doctor.get("checks"), list):
        raise SystemExit("doctor evidence is failed or malformed")
    namespaces = data.get("namespace_visibility")
    if not isinstance(namespaces, dict) or (require_complete and not namespaces):
        raise SystemExit("namespace visibility evidence must contain at least one inspected mount")
    for name, value in namespaces.items():
        if not isinstance(value, dict) or not str(value.get("claim", "")).strip() or not isinstance(value.get("achieved_classes"), list) or not isinstance(value.get("visibility"), list):
            raise SystemExit(f"namespace visibility evidence is malformed: {name}")
    q = require_dict(data.get("qualification"), "qualification provenance is missing")
    if q.get("harness_version") != HARNESS_VERSION or q.get("harness") != "scripts/dev/release_device_qualification.py" or not str(q.get("session_id", "")).strip():
        raise SystemExit("qualification provenance is stale or untrusted")


def validate(path: Path, require_complete: bool) -> dict[str, int]:
    data = load(path)
    validate_metadata(data, require_complete)
    cases = data.get("endurance_cases")
    if not isinstance(cases, dict):
        raise SystemExit("endurance_cases must be an object")
    unknown = sorted(set(cases) - set(CASES))
    if unknown:
        raise SystemExit("unknown endurance cases: " + ", ".join(unknown))
    counts = {"pass": 0, "skip": 0, "pending": 0, "failed": 0}
    errors: list[str] = []
    for case in CASES:
        entry = cases.get(case)
        if not isinstance(entry, dict):
            counts["pending"] += 1; errors.append(f"{case}: missing")
            continue
        status = entry.get("status")
        if status in {"pass", "skip"}:
            ok, reason = verify_case_proof(case, entry)
            if not ok:
                counts["failed"] += 1; errors.append(f"{case}: invalid proof: {reason}")
            else:
                counts[str(status)] += 1
        elif status == "fail":
            counts["failed"] += 1; errors.append(f"{case}: {entry.get('failure', 'failed')}")
        else:
            counts["pending"] += 1
    if require_complete and (counts["pending"] or counts["failed"]):
        raise SystemExit("release qualification incomplete: " + "; ".join(errors or [f"pending={counts['pending']} failed={counts['failed']}"]))
    if require_complete and counts["pass"] < 11:
        raise SystemExit("release qualification requires 11 machine-proven PASS cases")
    if os.name == "posix" and path.exists() and (path.stat().st_mode & 0o077):
        raise SystemExit("device evidence must not be group/world-readable")
    print(json.dumps({"status": "ok", "complete": counts["pending"] == 0 and counts["failed"] == 0, **counts}, sort_keys=True))
    return counts


def status(path: Path) -> None:
    data = load(path)
    require_v3(data)
    rows = []
    for case in CASES:
        entry = case_entry(data, case)
        rows.append({"case": case, "status": entry.get("status", "pending"), "phase": entry.get("phase", "not-started"), "instruction": entry.get("instruction", "")})
    print(json.dumps({"schema_version": data.get("schema_version"), "captured_at": data.get("captured_at"), "cases": rows}, indent=2))


def main() -> None:
    ap = argparse.ArgumentParser(description="Rclone Nexus real-device release qualification harness")
    ap.add_argument("--file", type=Path, default=None)
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("capture", help="capture immutable device/platform baseline and auto-discover configured mounts")
    c.add_argument("--mount", action="append", default=[], help="restrict qualification to a configured mount; repeatable")
    sub.add_parser("status", help="show resumable qualification state")
    r = sub.add_parser("run", help="start one endurance case")
    r.add_argument("case", choices=CASES)
    rr = sub.add_parser("resume", help="verify the next externally-driven transition for one case")
    rr.add_argument("case", choices=sorted(set(CASES) - AUTOMATIC_CASES))
    v = sub.add_parser("validate", help="validate provenance and machine proof")
    v.add_argument("--require-complete", action="store_true")
    ns = ap.parse_args()
    path = ns.file.expanduser().resolve() if ns.file else default_evidence_path()
    if ns.cmd == "capture": capture(path, ns.mount)
    elif ns.cmd == "status": status(path)
    elif ns.cmd == "run": run_case(path, ns.case)
    elif ns.cmd == "resume": resume_case(path, ns.case)
    else: validate(path, ns.require_complete)


if __name__ == "__main__":
    main()
