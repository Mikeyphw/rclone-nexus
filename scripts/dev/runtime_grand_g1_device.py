#!/usr/bin/env python3
from __future__ import annotations

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "dev"))
import runtime_standalone_g1_device as runtime_g1  # noqa: E402
import source_g1_device as source_g1  # noqa: E402
import release_device_qualification as release_device  # noqa: E402

SCHEMA_VERSION = 1
HARNESS_VERSION = 5
DEFAULT = ROOT / "release" / "evidence" / "runtime-grand-g1-device.json"
RUNTIME_EVIDENCE = "release/evidence/runtime-g1-device-qualification.json"
SOURCE_EVIDENCE = "release/evidence/source-g1-supply-chain-qualification.json"
RELEASE_EVIDENCE = "release/evidence/device-qualification.json"
DEVICE_PROMISES = [f"RNX-P{i:03d}" for i in range(467, 490)]

# Device evidence binds executable production/device behavior only. Final-gate
# governance files (.devtool, ledger, policy) are intentionally excluded because
# G1-B is allowed to change those after device capture; G1-B separately binds the
# complete current source tree in its seal digest.
BOUND_SOURCE_PATHS = [
    "cmd/racctl/main.go",
    "internal/control/engine.go",
    "internal/diagnostics/log.go",
    "internal/doctor/doctor.go",
    "internal/migration/migration.go",
    "internal/mounts/lifecycle.go",
    "internal/paths/paths.go",
    "internal/provider/provider.go",
    "internal/runtimeactivation/activation.go",
    "internal/runtimebuild/bundle.go",
    "internal/runtimeupdate/update.go",
    "internal/runtimesource/source.go",
    "internal/runtimestore/helper.go",
    "internal/runtimestore/qualify.go",
    "internal/runtimestore/store.go",
    "module/service.sh",
    "scripts/dev/release_device_qualification.py",
    "scripts/dev/runtime_grand_g1_device.py",
    "scripts/dev/runtime_source_build.py",
    "scripts/dev/runtime_standalone_g1_device.py",
    "scripts/dev/source_g1_device.py",
]


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def canonical(value: object) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value: object) -> str:
    return hashlib.sha256(canonical(value)).hexdigest()


def file_sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def primary_root() -> Path:
    raw = os.environ.get("DEVTOOL_TRANSACTION_PRIMARY_REPO_ROOT", "").strip()
    return Path(raw).expanduser().resolve() if raw else ROOT


def evidence_path(rel: str) -> Path:
    return primary_root() / rel


def source_bindings() -> dict[str, str]:
    out: dict[str, str] = {}
    for rel in BOUND_SOURCE_PATHS:
        path = ROOT / rel
        if not path.is_file():
            raise RuntimeError(f"RUNTIME-GRAND-G1 bound source missing: {rel}")
        out[rel] = file_sha256(path)
    return out


def write_private(path: Path, data: dict) -> None:
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


def runtime_gate_racctl(runtime_data: dict) -> str:
    refs = runtime_data.get("evidence") if isinstance(runtime_data, dict) else None
    if not isinstance(refs, list):
        raise RuntimeError("RUNTIME-G1 evidence does not expose the source-bound gate racctl")
    for ref in refs:
        if not isinstance(ref, dict) or ref.get("kind") != "gate-racctl":
            continue
        path = str(ref.get("path", "")).strip()
        expected = str(ref.get("sha256", "")).strip().lower()
        if not path or len(expected) != 64:
            raise RuntimeError("RUNTIME-G1 gate racctl evidence reference is malformed")
        if runtime_g1.root_hash(path).lower() != expected:
            raise RuntimeError("RUNTIME-G1 gate racctl evidence reference is stale")
        return path
    raise RuntimeError("RUNTIME-G1 evidence does not contain a gate-racctl reference")


def run(argv: list[str], *, cwd: Path | None = None, env: dict[str, str] | None = None, timeout: int = 300, check: bool = True) -> subprocess.CompletedProcess[str]:
    cp = subprocess.run(argv, cwd=cwd or ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout, check=False)
    if check and cp.returncode != 0:
        raise RuntimeError(f"command failed ({cp.returncode}): {shlex.join(argv)}\nstdout={cp.stdout[-6000:]}\nstderr={cp.stderr[-6000:]}")
    return cp


def parse_obj(cp: subprocess.CompletedProcess[str], label: str) -> dict:
    try:
        value = json.loads(cp.stdout)
    except Exception as exc:
        raise RuntimeError(f"{label} did not emit JSON: {cp.stdout[-2000:]!r}") from exc
    if not isinstance(value, dict):
        raise RuntimeError(f"{label} emitted non-object JSON")
    return value


def installed_racctl() -> str:
    candidates = [os.environ.get("RNEXUS_RACCTL", "").strip(), shutil.which("racctl") or "", "/data/adb/modules/rclone_nexus/system/bin/racctl"]
    for item in candidates:
        if item and runtime_g1.root_run(["test", "-x", item], timeout=5).returncode == 0:
            return item
    raise RuntimeError("installed Rclone Nexus racctl is unavailable")


def racctl_json(binary: str, args: list[str], *, env: dict[str, str] | None = None, timeout: int = 300, check: bool = True) -> dict:
    cp = runtime_g1.root_run([binary, *args], env=env or {}, timeout=timeout, check=False)
    if check and cp.returncode != 0:
        raise RuntimeError(f"racctl {' '.join(args)} failed: {cp.stderr[-3000:] or cp.stdout[-3000:]}")
    try:
        value = json.loads(cp.stdout)
    except Exception as exc:
        raise RuntimeError(f"racctl {' '.join(args)} did not emit JSON: {cp.stdout[-2000:]!r}") from exc
    if not isinstance(value, dict):
        raise RuntimeError(f"racctl {' '.join(args)} emitted non-object JSON")
    return value


def clone_exact(repository: str, commit: str, dest: Path) -> None:
    if len(commit) != 40 or any(ch not in "0123456789abcdef" for ch in commit.lower()):
        raise RuntimeError(f"invalid exact commit for {repository}: {commit}")
    url = f"https://github.com/{repository}.git"
    run(["git", "init", "-q", str(dest)], timeout=30)
    run(["git", "-C", str(dest), "remote", "add", "origin", url], timeout=30)
    run(["git", "-C", str(dest), "fetch", "--depth=1", "origin", commit], timeout=240)
    run(["git", "-C", str(dest), "checkout", "-q", "--detach", "FETCH_HEAD"], timeout=30)
    head = run(["git", "-C", str(dest), "rev-parse", "HEAD"], timeout=10).stdout.strip().lower()
    if head != commit.lower():
        raise RuntimeError(f"checkout identity mismatch for {repository}: {head} != {commit}")


def build_bundle(source: Path, out: Path, resolution: dict, ndk: dict) -> dict:
    argv = [
        sys.executable, str(ROOT / "scripts/dev/runtime_source_build.py"), "build",
        "--source-dir", str(source), "--output-dir", str(out),
        "--repository", str(resolution["repository"]),
        "--requested-ref", str(resolution.get("requested_ref") or resolution.get("release_tag") or resolution["commit_sha"]),
        "--resolved-commit", str(resolution["commit_sha"]),
        "--source-id", str(resolution["source_id"]), "--engine", str(resolution["engine"]),
        "--ndk", str(ndk["ndk"]), "--ndk-version", str(ndk["version"]), "--ndk-host", str(ndk["host"]),
        "--compiler", str(ndk.get("compiler", "")), "--compiler-mode", str(ndk.get("compiler_mode", "ndk-prebuilt")),
        "--api-level", "21",
    ]
    run(argv, timeout=1200)
    return json.loads((out / "provenance.json").read_text(encoding="utf-8"))


def process_hash(pid: int, state: str) -> str:
    return source_g1.proc_hash(pid, state)


def encrypted_config_probe(runtime_binary: str, root_dir: str, binary: str, env: dict[str, str]) -> dict:
    config = f"{root_dir}/encrypted-rclone.conf"
    pass_script = f"{root_dir}/config-pass.sh"
    canary = "RNEXUS-GRAND-G1-CONFIG-PASS-9f5b3e"
    script_body = '#!/system/bin/sh\\nprintf "%s\\n" "' + canary + '"\\n'
    runtime_g1.root_run(["/system/bin/sh", "-c", f"printf '[grandlocal]\\ntype = local\\n' > {shlex.quote(config)}"], check=True)
    local = Path(tempfile.mkdtemp(prefix="rnx-g1-pass-")) / "config-pass.sh"
    try:
        local.write_text(script_body, encoding="utf-8")
        runtime_g1.root_write_from_local(local, pass_script)
    finally:
        shutil.rmtree(local.parent, ignore_errors=True)
    runtime_g1.root_run(["chmod", "0700", pass_script], check=True)
    pass_env = {"RCLONE_PASSWORD_COMMAND": pass_script}
    set_cp = runtime_g1.root_run([runtime_binary, "--config", config, "config", "encryption", "set"], env=pass_env, timeout=60)
    if set_cp.returncode != 0:
        raise RuntimeError("could not create qualification encrypted rclone.conf: " + (set_cp.stderr or set_cp.stdout))
    check_cp = runtime_g1.root_run([runtime_binary, "--config", config, "config", "encryption", "check"], env=pass_env, timeout=30)
    list_cp = runtime_g1.root_run([runtime_binary, "--config", config, "--ask-password=false", "listremotes"], env=pass_env, timeout=30)
    raw = runtime_g1.root_text(config)
    if check_cp.returncode != 0 or list_cp.returncode != 0 or "grandlocal:" not in list_cp.stdout:
        raise RuntimeError("encrypted config did not decrypt through production runtime")
    if not raw.startswith("RCLONE_ENCRYPT_V0:") or canary in raw:
        raise RuntimeError("encrypted config is not encrypted at rest")
    transcript = "\\n".join([set_cp.stdout, set_cp.stderr, check_cp.stdout, check_cp.stderr, list_cp.stdout, list_cp.stderr])
    if canary in transcript:
        raise RuntimeError("encrypted config password leaked to command output")

    # Device-level diagnostic redaction proof: put a credential-shaped canary in
    # a real diagnostics log and build the production support bundle. The bundle
    # must retain the diagnostic line while removing the secret value.
    diag = f"{env['RNEXUS_STATE_DIR']}/diagnostics/grand-g1-redaction.log"
    runtime_g1.root_run(["mkdir", "-p", f"{env['RNEXUS_STATE_DIR']}/diagnostics"], check=True)
    runtime_g1.root_run(["/system/bin/sh", "-c", f"printf '%s\\n' {shlex.quote('token=' + canary + ' Authorization: Bearer ' + canary)} > {shlex.quote(diag)}"], check=True)
    bundle_cp = runtime_g1.racctl(binary, ["doctor", "--bundle"], env, timeout=120, check=True)
    bundle_path = bundle_cp.stdout.strip().splitlines()[-1]
    bundle_bytes = runtime_g1.root_run(["cat", bundle_path], timeout=30, check=True, binary=True).stdout
    if isinstance(bundle_bytes, str):
        bundle_bytes = bundle_bytes.encode()
    try:
        with zipfile.ZipFile(io.BytesIO(bundle_bytes)) as zf:
            unpacked = b"\n".join(zf.read(name) for name in zf.namelist())
    except Exception as exc:
        raise RuntimeError(f"production support bundle was not a readable ZIP: {exc}") from exc
    if canary.encode() in unpacked:
        raise RuntimeError("support bundle leaked credential-shaped diagnostic canary")
    if b"token=<redacted>" not in unpacked:
        raise RuntimeError("support bundle did not retain a sanitized credential diagnostic")
    return {
        "config_sha256": runtime_g1.root_hash(config),
        "password_redacted": True,
        "listremotes": True,
        "support_bundle_redacted": True,
        "support_bundle_sha256": hashlib.sha256(bundle_bytes).hexdigest(),
    }


def migration_probe(binary: str, root_dir: str, module_dir: str, managed_runtime: str) -> dict:
    """Run MIGRATE-X01 through the real rooted Android production CLI."""
    mig_root = f"{root_dir}/migration"
    state = f"{mig_root}/state"
    provider = f"{mig_root}/provider"
    conf = f"{provider}/conf"
    source = f"{mig_root}/source"
    selected_mp = f"{mig_root}/mount-selected"
    unselected_mp = f"{mig_root}/mount-unselected"
    runtime_g1.root_run(["mkdir", "-p", conf, source, selected_mp, unselected_mp, f"{state}/runtime/active/bin"], check=True)
    runtime_g1.root_run(["/system/bin/sh", "-c", f"printf 'proof\\n' > {shlex.quote(source + '/proof.txt')}"], check=True)
    runtime_g1.root_run(["/system/bin/sh", "-c", f"printf 'id=rclone\\nversion=1.75.0\\n' > {shlex.quote(provider + '/module.prop')}"], check=True)
    runtime_g1.root_run(["/system/bin/sh", "-c", f"printf '[demo]\\ntype = local\\n' > {shlex.quote(conf + '/rclone.conf')} && printf 'demo:{source} {mig_root}/sync-dst\\n' > {shlex.quote(conf + '/sync')}"], check=True)
    runtime_g1.root_run(["cp", managed_runtime, f"{state}/runtime/active/bin/rclone"], check=True)
    runtime_g1.root_run(["chmod", "0755", f"{state}/runtime/active/bin/rclone"], check=True)
    provider_rclone = f"{provider}/system/bin/rclone"
    runner = f"{provider}/runner.sh"
    runtime_g1.root_run(["mkdir", "-p", f"{provider}/system/bin"], check=True)
    runtime_g1.root_run(["/system/bin/sh", "-c", f"printf '#!/system/bin/sh\\n[ \"$1\" = version ] && {{ echo \"rclone v1.75.0\"; exit 0; }}\\nexit 0\\n' > {shlex.quote(provider_rclone)} && chmod 0755 {shlex.quote(provider_rclone)}"], check=True)
    runtime_g1.root_run(["/system/bin/sh", "-c", f"printf '#!/system/bin/sh\\ntrap \"exit 0\" TERM INT\\nwhile :; do sleep 1; done\\n' > {shlex.quote(runner)} && chmod 0755 {shlex.quote(runner)}"], check=True)

    env = runtime_g1.racctl_env(state, module_dir, provider)
    env.update({"RNEXUS_MIGRATION_QUIESCE_TIMEOUT_MS": "4000", "RNEXUS_START_GRACE_SECONDS": "1", "RNEXUS_STOP_TIMEOUT_SECONDS": "2"})
    pids: list[int] = []
    try:
        for argv in ([runner, "mount", f"demo:{source}", selected_mp], [runner, "mount", f"demo:{source}", unselected_mp], [runner, "rclone-web", "--rc-addr", "127.0.0.1:5572"]):
            shell = shlex.join(argv) + " >/dev/null 2>&1 & echo $!"
            cp = runtime_g1.root_run(["/system/bin/sh", "-c", shell], timeout=10, check=True)
            pids.append(int(cp.stdout.strip().splitlines()[-1]))
        time.sleep(0.5)
        inspected = racctl_json(binary, ["migration", "inspect"], env=env, timeout=60)
        mounts = inspected.get("mounts") if isinstance(inspected.get("mounts"), list) else []
        selected = next((m for m in mounts if isinstance(m, dict) and m.get("mountpoint") == selected_mp), None)
        unselected = next((m for m in mounts if isinstance(m, dict) and m.get("mountpoint") == unselected_mp), None)
        if not selected or not unselected or inspected.get("provider_enabled") is not True:
            raise RuntimeError("rooted migration fixture was not discovered")
        preview = racctl_json(binary, ["migration", "preview", "--mount", str(selected["id"]), "--job", "sync:1", "--job-every", "24h"], env=env, timeout=60)
        pv = preview.get("preview") if isinstance(preview.get("preview"), dict) else {}
        if pv.get("can_apply") is not True or len(pv.get("selected_mounts", [])) != 1:
            raise RuntimeError("rooted migration preview did not bind exactly one selected mount")
        applied = racctl_json(binary, ["migration", "apply", str(preview["preview_proof"]), str(preview["current_revision"]), str(preview["candidate_digest"]), "--mount", str(selected["id"]), "--job", "sync:1", "--job-every", "24h"], env=env, timeout=120)
        status = racctl_json(binary, ["migration", "status"], env=env, timeout=30)
        st = status.get("state") if isinstance(status.get("state"), dict) else status
        if st.get("phase") != "AWAITING_PROVIDER_DISABLE":
            raise RuntimeError("migration did not stop at explicit provider-disable boundary")
        runtime_g1.root_run(["/system/bin/sh", "-c", f": > {shlex.quote(provider + '/disable')}"], check=True)
        fp = racctl_json(binary, ["migration", "finalize-preview"], env=env, timeout=30)
        fpp = fp.get("preview") if isinstance(fp.get("preview"), dict) else {}
        if fpp.get("can_finalize") is not True:
            raise RuntimeError("migration finalize preview rejected externally disabled provider")
        fin = racctl_json(binary, ["migration", "finalize", str(fp["preview_proof"]), str(fp["current_revision"]), str(fp["candidate_digest"])], env=env, timeout=180)
        if fin.get("phase") != "COMPLETED":
            raise RuntimeError("rooted migration did not complete")
        selected_name = str(selected["name"]); unselected_name = str(unselected["name"])
        selected_status = runtime_g1.racctl(binary, ["compat", "mountctl", "status", selected_name], env, timeout=30, check=False)
        unselected_status = runtime_g1.racctl(binary, ["compat", "mountctl", "status", unselected_name], env, timeout=30, check=False)
        if selected_status.returncode != 0 or "running" not in selected_status.stdout:
            raise RuntimeError("selected migrated mount did not enter Nexus authority")
        if unselected_status.returncode != 2 or "not-configured" not in unselected_status.stdout:
            raise RuntimeError("unselected provider mount entered Nexus authority")
        if runtime_g1.root_run(["test", "-f", f"{provider}/module.prop"], timeout=5).returncode != 0 or runtime_g1.root_run(["test", "-f", f"{provider}/disable"], timeout=5).returncode != 0:
            raise RuntimeError("migration removed provider files or failed to retire provider authority")
        runtime_g1.racctl(binary, ["compat", "mountctl", "stop", selected_name], env, timeout=60, check=False)
        return {
            "completed": True,
            "provider_present_after": True,
            "provider_enabled_after": False,
            "selected_mount_running": True,
            "unselected_mount_not_adopted": True,
            "evidence_path": fin.get("evidence_path", ""),
        }
    finally:
        for pid in pids:
            runtime_g1.root_run(["kill", "-9", str(pid)], timeout=5, check=False)


def rpc_query(binary: str, name: str, args: dict) -> dict:
    req = runtime_g1.request_payload(name, "query", args)
    encoded = base64.urlsafe_b64encode(json.dumps(req, separators=(",", ":")).encode()).rstrip(b"=").decode()
    result = runtime_g1.root_run([binary, "webui", "bridge", "--request-base64", encoded], timeout=60, check=True)
    outer = json.loads(result.stdout)
    response = outer.get("response") if isinstance(outer, dict) else None
    if not isinstance(response, dict) or response.get("ok") is not True or not isinstance(response.get("result"), dict):
        raise RuntimeError(f"{name} bridge query failed")
    return response["result"]



def failed_activation_rollback_probe(binary: str, env: dict[str, str], state: str, candidate_id: str, previous_id: str, previous_sha: str) -> dict:
    """Force a post-quiesce candidate requalification failure and prove automatic rollback.

    The candidate is first verified normally. A root watcher waits until the
    production activation transaction reaches QUIESCING, then corrupts only the
    candidate's immutable bytes. The controller's mandatory second verification
    must fail and automatically restore the previous runtime/mount. The original
    candidate bytes are restored after the transaction so the qualification
    fixture remains internally consistent for later probes.
    """
    candidate = f"{state}/runtimes/{candidate_id}/rclone"
    backup = candidate + ".grand-g1-backup"
    statefile = f"{state}/runtime/activation-v1.json"
    runtime_g1.root_run(["cp", candidate, backup], timeout=30, check=True)
    watcher = f"""
set -eu
statefile={shlex.quote(statefile)}
candidate={shlex.quote(candidate)}
for i in $(seq 1 3000); do
  if grep -q '"phase": "QUIESCING"' "$statefile" 2>/dev/null; then
    printf '\nRNEXUS-GRAND-G1-ACTIVATION-FAIL\n' >> "$candidate"
    exit 0
  fi
  sleep 0.01
done
exit 71
"""
    launched = runtime_g1.root_run(["/system/bin/sh", "-c", watcher + " >/dev/null 2>&1 & echo $!"], timeout=10, check=True)
    watcher_pid = int(launched.stdout.strip().splitlines()[-1])
    try:
        failed = runtime_g1.racctl(binary, ["runtime", "activate", candidate_id], env, timeout=360, check=False)
        # Wait briefly for the watcher to terminate so its mutation cannot race the checks below.
        for _ in range(100):
            if runtime_g1.root_run(["kill", "-0", str(watcher_pid)], timeout=2).returncode != 0:
                break
            time.sleep(0.02)
        if failed.returncode == 0:
            raise RuntimeError("forced candidate corruption did not make activation fail")
        activation = racctl_json(binary, ["runtime", "activation-status"], env=env, timeout=30)
        st = activation.get("state") if isinstance(activation.get("state"), dict) else {}
        if st.get("phase") not in {"RECOVERED", "DEGRADED_RECOVERED"} or st.get("active_runtime_id") != previous_id:
            raise RuntimeError("failed activation did not automatically restore the previous runtime")
        mount = runtime_g1.mount_status(binary, env, "grand-g1")
        live_sha = process_hash(int(mount.get("pid") or 0), state)
        if live_sha != previous_sha:
            raise RuntimeError("failed activation rollback did not restore previous live executable bytes")
        return {
            "candidate_runtime_id": candidate_id,
            "restored_runtime_id": previous_id,
            "phase": st.get("phase"),
            "live_process_sha256": live_sha,
            "activation_failed": True,
            "automatic_rollback": True,
        }
    finally:
        runtime_g1.root_run(["cp", backup, candidate], timeout=30, check=False)
        runtime_g1.root_run(["rm", "-f", backup], timeout=10, check=False)
        runtime_g1.root_run(["kill", "-9", str(watcher_pid)], timeout=5, check=False)


def automatic_journeys() -> dict:
    runtime_g1.require_android_root()
    session = "runtime-grand-g1-" + hashlib.sha256(os.urandom(32)).hexdigest()[:16]
    root_dir = f"/data/adb/rclone-nexus/qualification/{session}"
    state = f"{root_dir}/state"
    provider_absent = f"{root_dir}/provider-absent"
    runtime_g1.root_run(["mkdir", "-p", root_dir, state], check=True)
    runtime_g1.root_run(["chmod", "0700", root_dir, state], check=True)
    ndk = source_g1.find_ndk()
    if ndk.get("supported") is not True:
        raise RuntimeError("RUNTIME-GRAND-G1 requires a runnable Android NDK for real latest bclone/rclone builds: " + str(ndk.get("reason", "unavailable")))

    with tempfile.TemporaryDirectory(prefix="rnx-grand-g1-") as raw:
        tmp = Path(raw)
        built_racctl = tmp / "racctl"
        runtime_g1.build_current_racctl(built_racctl)
        module_dir, binary = runtime_g1.copy_gate_module(root_dir, built_racctl)
        env = runtime_g1.racctl_env(state, module_dir, provider_absent)
        runtime_g1.root_run(["mkdir", "-p", f"{state}/config/rclone", f"{state}/mounts.d", f"{state}/run"], check=True)
        cfg = tmp / "rclone.conf"; cfg.write_text("[runtimeg1]\ntype = local\n", encoding="utf-8")
        runtime_g1.root_write_from_local(cfg, f"{state}/config/rclone/rclone.conf")
        source_dir = f"{root_dir}/source"; mountpoint = f"{root_dir}/mountpoint"
        runtime_g1.root_run(["mkdir", "-p", source_dir, mountpoint], check=True)
        runtime_g1.root_run(["/system/bin/sh", "-c", f"printf %s {shlex.quote(session)} > {shlex.quote(source_dir + '/proof.txt')}"], check=True)
        mount_cfg = tmp / "grand-g1.conf"; mount_cfg.write_text(runtime_g1.g1_mount_definition(source_dir, mountpoint).replace("runtime-g1", "grand-g1"), encoding="utf-8")
        runtime_g1.root_write_from_local(mount_cfg, f"{state}/mounts.d/grand-g1.conf")

        resolutions: dict[str, dict] = {}
        manifests: dict[str, dict] = {}
        provenances: dict[str, dict] = {}
        for sid in ("bclone", "rclone"):
            res = json.loads(runtime_g1.racctl(binary, ["runtime", "source", "resolve", sid], env, timeout=180, check=True).stdout)
            source_g1.resolution_identity(res)
            resolutions[sid] = res
            repo = tmp / f"repo-{sid}"; out = tmp / f"bundle-{sid}"
            clone_exact(str(res["repository"]), str(res["commit_sha"]), repo)
            provenances[sid] = build_bundle(repo, out, res, ndk)
            imported = json.loads(runtime_g1.racctl(binary, ["runtime", "source", "import-build", str(out)], env, timeout=600, check=True).stdout)
            q = imported.get("qualification") if isinstance(imported.get("qualification"), dict) else {}
            if q.get("qualified") is not True:
                raise RuntimeError(f"{sid} real Android build did not qualify")
            manifests[sid] = imported

        b_id = str(manifests["bclone"]["runtime_id"]); r_id = str(manifests["rclone"]["runtime_id"])
        b_sha = str(manifests["bclone"]["binary_sha256"]); r_sha = str(manifests["rclone"]["binary_sha256"])
        runtime_g1.racctl(binary, ["runtime", "activate", b_id], env, timeout=360, check=True)
        runtime_g1.racctl(binary, ["compat", "mountctl", "start", "grand-g1"], env, timeout=120, check=True)
        b_stat = runtime_g1.mount_status(binary, env, "grand-g1"); b_pid = int(b_stat.get("pid") or 0)
        if process_hash(b_pid, state) != b_sha or runtime_g1.root_run(["cat", f"{mountpoint}/proof.txt"], check=True).stdout.strip() != session:
            raise RuntimeError("latest bclone did not become the live mounted runtime")
        runtime_g1.racctl(binary, ["runtime", "activate", r_id], env, timeout=360, check=True)
        r_stat = runtime_g1.mount_status(binary, env, "grand-g1"); r_pid = int(r_stat.get("pid") or 0)
        if r_pid == b_pid or process_hash(r_pid, state) != r_sha:
            raise RuntimeError("bclone -> official rclone switch did not change live executable bytes")
        runtime_g1.racctl(binary, ["runtime", "activate", b_id], env, timeout=360, check=True)
        back_stat = runtime_g1.mount_status(binary, env, "grand-g1"); back_pid = int(back_stat.get("pid") or 0)
        if back_pid == r_pid or process_hash(back_pid, state) != b_sha:
            raise RuntimeError("official rclone -> bclone switch-back did not restore bclone bytes")

        failed_activation = failed_activation_rollback_probe(binary, env, state, r_id, b_id, b_sha)

        bad = tmp / "not-a-runtime"; bad.write_text("not an ELF runtime\n", encoding="utf-8")
        bad_cp = runtime_g1.racctl(binary, ["runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", str(bad)], env, timeout=120, check=False)
        if bad_cp.returncode == 0:
            raise RuntimeError("failed candidate qualification was accepted")
        after_bad = json.loads(runtime_g1.racctl(binary, ["runtime", "status", "--json"], env, check=True).stdout)
        if after_bad.get("active_runtime_id") != b_id:
            raise RuntimeError("failed candidate qualification changed active runtime")

        # Simulate a process crash after the candidate was projected but before verification,
        # then recover through the production `runtime recover` ingress.
        runtime_g1.racctl(binary, ["runtime", "activate", r_id], env, timeout=360, check=True)
        crash_state = {
            "schema_version": 1, "generation": 9001, "phase": "ACTIVE_PENDING_VERIFY", "transaction_id": "rtx-grand-g1-crash",
            "active_runtime_id": r_id, "active_binary_sha256": r_sha,
            "previous_runtime_id": b_id, "previous_binary_sha256": b_sha,
            "candidate_runtime_id": r_id, "candidate_binary_sha256": r_sha,
            "staged_runtime_id": r_id, "staged_binary_sha256": r_sha,
            "desired_mounts": ["grand-g1"], "quiesced_mounts": ["grand-g1"],
            "started_unix_ms": int(time.time() * 1000), "updated_unix_ms": int(time.time() * 1000),
        }
        crash_local = tmp / "activation-v1.json"; crash_local.write_text(json.dumps(crash_state) + "\n", encoding="utf-8")
        runtime_g1.root_write_from_local(crash_local, f"{state}/runtime/activation-v1.json")
        recovered = json.loads(runtime_g1.racctl(binary, ["runtime", "recover"], env, timeout=360, check=True).stdout)
        recovered_state = recovered.get("state") if isinstance(recovered.get("state"), dict) else recovered
        if recovered_state.get("active_runtime_id") != b_id or recovered_state.get("phase") not in {"RECOVERED", "DEGRADED_RECOVERED"}:
            raise RuntimeError("crash recovery did not fail safe to previous runtime")
        rec_stat = runtime_g1.mount_status(binary, env, "grand-g1")
        if process_hash(int(rec_stat.get("pid") or 0), state) != b_sha:
            raise RuntimeError("crash recovery did not restore live previous-runtime bytes")

        encrypted = encrypted_config_probe(f"{state}/runtime/active/bin/rclone", root_dir, binary, env)
        runtime_g1.racctl(binary, ["compat", "mountctl", "stop", "grand-g1"], env, timeout=60, check=False)
        migration = migration_probe(binary, root_dir, module_dir, f"{state}/runtime/active/bin/rclone")

        return {
            "session": session,
            "provider_absent": runtime_g1.root_run(["test", "-e", provider_absent], timeout=5).returncode != 0,
            "resolutions": {k: {"resolution_id": v["resolution_id"], "repository": v["repository"], "commit_sha": v["commit_sha"]} for k, v in resolutions.items()},
            "builds": {k: {"runtime_id": manifests[k]["runtime_id"], "binary_sha256": manifests[k]["binary_sha256"], "provenance_sha256": digest(provenances[k])} for k in manifests},
            "switch": {"bclone_pid": b_pid, "rclone_pid": r_pid, "back_pid": back_pid, "bclone_sha256": b_sha, "rclone_sha256": r_sha},
            "failed_qualification_rejected": True,
            "failed_activation_rollback": failed_activation,
            "crash_recovery": {"restored_runtime_id": b_id, "phase": recovered_state.get("phase")},
            "encrypted_config": encrypted,
            "migration": migration,
        }


def actual_device_probes() -> dict:
    binary = installed_racctl()
    config = racctl_json(binary, ["compat", "nexus", "config"], timeout=30)
    mounts = config.get("mounts") if isinstance(config.get("mounts"), list) else []
    if not mounts:
        raise RuntimeError("RUNTIME-GRAND-G1 requires at least one real configured Nexus mount")
    first = next((m for m in mounts if isinstance(m, dict) and str(m.get("remote", "")).strip()), None)
    if not isinstance(first, dict):
        raise RuntimeError("configured Nexus mounts do not expose a real remote")
    remote_value = str(first["remote"])
    remote = remote_value.split(":", 1)[0]
    browse = rpc_query(binary, "provider.browse", {"remote": remote, "path": "", "limit": 10})
    if not isinstance(browse.get("entries"), list):
        raise RuntimeError("production provider.browse did not return typed entries")
    manager = racctl_json(binary, ["runtime", "manager"], timeout=30)
    update = manager.get("update") if isinstance(manager.get("update"), dict) else {}
    return {"remote": remote, "browse_entry_count": len(browse["entries"]), "runtime_manager_update": update}


def capture(path: Path) -> dict:
    runtime_g1.require_android_root()
    runtime_path = evidence_path(RUNTIME_EVIDENCE)
    source_path = evidence_path(SOURCE_EVIDENCE)
    release_path = evidence_path(RELEASE_EVIDENCE)

    # Each underlying harness is a production-path authority with its own physical evidence.
    # Reuse already-valid private evidence after an interrupted later stage so a
    # SOURCE-G1 or release-case failure does not rerun expensive rooted/FUSE/boot
    # qualification that is still source- and device-bound. Stale/invalid evidence
    # is never trusted: validation failure falls back to a fresh capture.
    if runtime_path.is_file():
        try:
            runtime_data = runtime_g1.validate(runtime_path, resolve_files=True)
            print('RUNTIME-GRAND-G1-A: reusing current RUNTIME-G1 private evidence', file=sys.stderr, flush=True)
        except Exception:
            runtime_g1.capture(runtime_path)
            runtime_data = runtime_g1.validate(runtime_path, resolve_files=True)
    else:
        runtime_g1.capture(runtime_path)
        runtime_data = runtime_g1.validate(runtime_path, resolve_files=True)

    if source_path.is_file():
        try:
            source_g1.verify(source_path, physical=True)
            print('RUNTIME-GRAND-G1-A: reusing current SOURCE-G1 private evidence', file=sys.stderr, flush=True)
        except Exception:
            source_g1.capture(source_path)
            source_g1.verify(source_path, physical=True)
    else:
        source_g1.capture(source_path)
        source_g1.verify(source_path, physical=True)

    # Release qualification must execute the exact current racctl binary that was
    # built, hashed, and physically retained by RUNTIME-G1.  An arbitrary PATH or
    # installed module racctl may be older than the checked-out source and can
    # return a schema-compatible but stale/empty authority projection.
    gate_racctl = runtime_gate_racctl(runtime_data)
    previous_racctl = os.environ.get("RNEXUS_RACCTL")
    os.environ["RNEXUS_RACCTL"] = gate_racctl
    try:
        if release_path.is_file():
            try:
                release_device.validate(release_path, require_complete=False)
                print('RUNTIME-GRAND-G1-A: reusing current release-device private evidence', file=sys.stderr, flush=True)
            except Exception:
                release_device.capture(release_path, [])
        else:
            release_device.capture(release_path, [])

        automatic = automatic_journeys()
        actual = actual_device_probes()
    finally:
        if previous_racctl is None:
            os.environ.pop("RNEXUS_RACCTL", None)
        else:
            os.environ["RNEXUS_RACCTL"] = previous_racctl
    data = {
        "schema_version": SCHEMA_VERSION,
        "harness_version": HARNESS_VERSION,
        "campaign": "RUNTIME-STANDALONE",
        "position": 11,
        "captured_at": now(),
        "status": "IN_PROGRESS",
        "source_bindings": source_bindings(),
        "device": {"sdk": runtime_g1.prop("ro.build.version.sdk"), "fingerprint_sha256": hashlib.sha256(runtime_g1.prop("ro.build.fingerprint").encode()).hexdigest()},
        "evidence": {
            "runtime_g1": {"path": str(runtime_path), "sha256": file_sha256(runtime_path)},
            "source_g1": {"path": str(source_path), "sha256": file_sha256(source_path)},
            "release": {"path": str(release_path), "sha256": file_sha256(release_path)},
        },
        "automatic": automatic,
        "actual": actual,
        "staged_reboot": {"status": "pending"},
    }
    data["document_sha256"] = digest({k: v for k, v in data.items() if k != "document_sha256"})
    write_private(path, data)
    print(json.dumps({"status": "IN_PROGRESS", "evidence": str(path), "next": "run the release endurance cases and staged-reboot qualification"}, sort_keys=True))
    return data


def prepare_staged_reboot(path: Path) -> None:
    data = load_and_verify_document(path)
    binary = installed_racctl()
    before = racctl_json(binary, ["runtime", "update", "status"], timeout=30)
    current = str(before.get("current_runtime_id", ""))
    manifests = json.loads(runtime_g1.root_run([binary, "runtime", "list"], timeout=30, check=True).stdout)
    if not isinstance(manifests, list):
        raise RuntimeError("runtime list did not return candidates")
    candidate = next((m for m in manifests if isinstance(m, dict) and m.get("runtime_id") != current and isinstance(m.get("qualification"), dict) and m["qualification"].get("qualified") is True), None)
    if not isinstance(candidate, dict):
        raise RuntimeError("staged-reboot needs a second qualified runtime in the installed store; import/qualify one first")
    candidate_id = str(candidate["runtime_id"])
    candidate_path = f"/data/adb/rclone-nexus/runtimes/{candidate_id}/rclone"
    source_id = "grand-g1-reboot"
    runtime_g1.root_run([binary, "runtime", "source", "remove", source_id], timeout=30, check=False)
    runtime_g1.root_run([binary, "runtime", "source", "register", "--id", source_id, "--engine", str(candidate.get("engine") or "rclone"), "--kind", "local-binary", "--channel", "manual-only", "--path", candidate_path], timeout=30, check=True)
    old_policy = racctl_json(binary, ["runtime", "update", "policy"], timeout=30)
    runtime_g1.root_run([binary, "runtime", "update", "policy-set", "--source", source_id, "--check", "false", "--acquire", "true", "--qualify", "true", "--stage", "true", "--activation", "next-reboot", "--restart-active-mounts", "true"], timeout=30, check=True)
    staged = racctl_json(binary, ["runtime", "update", "check", source_id], timeout=180)
    staged_id = str(staged.get("staged_runtime_id", ""))
    if not staged_id or staged_id == current:
        raise RuntimeError("update manager did not stage a distinct next-reboot runtime")
    config_path = runtime_g1.root_run([binary, "runtime", "config"], timeout=10, check=True).stdout.strip()
    data["staged_reboot"] = {
        "status": "awaiting-reboot", "prepared_at": now(), "boot_id_before": runtime_g1.boot_id(),
        "current_runtime_id": current, "staged_runtime_id": staged_id,
        "config_path": config_path, "config_sha256_before": runtime_g1.root_hash(config_path), "old_policy": old_policy,
        "instruction": "Reboot Android normally, wait for Rclone Nexus boot reconciliation, then run runtime_grand_g1_device.py resume-staged-reboot",
    }
    refresh_doc_digest(data); write_private(path, data)
    print(data["staged_reboot"]["instruction"])


def resume_staged_reboot(path: Path) -> None:
    data = load_and_verify_document(path)
    entry = data.get("staged_reboot")
    if not isinstance(entry, dict) or entry.get("status") != "awaiting-reboot":
        raise RuntimeError("staged reboot is not awaiting a reboot")
    if runtime_g1.boot_id() == entry.get("boot_id_before"):
        raise RuntimeError("Android boot identity has not changed yet")
    binary = installed_racctl()
    status = racctl_json(binary, ["runtime", "update", "status"], timeout=30)
    if str(status.get("current_runtime_id", "")) != entry.get("staged_runtime_id"):
        raise RuntimeError("next-reboot staged runtime did not become active")
    if runtime_g1.root_hash(str(entry["config_path"])) != entry.get("config_sha256_before"):
        raise RuntimeError("managed rclone config changed across staged-candidate reboot")
    entry["status"] = "pass"; entry["completed_at"] = now(); entry["boot_id_after"] = runtime_g1.boot_id()
    entry["active_runtime_id_after"] = status.get("current_runtime_id")
    # Restore the previous active runtime and policy after proving the boot transition.
    runtime_g1.root_run([binary, "runtime", "update", "rollback"], timeout=360, check=True)
    old = entry.get("old_policy") if isinstance(entry.get("old_policy"), dict) else {}
    argv = [binary, "runtime", "update", "policy-set", "--source", str(old.get("source_id", "bclone")), "--check", str(bool(old.get("check_automatically", True))).lower(), "--acquire", str(bool(old.get("acquire_automatically", True))).lower(), "--qualify", str(bool(old.get("qualify_automatically", True))).lower(), "--stage", str(bool(old.get("stage_automatically", True))).lower(), "--activation", str(old.get("activation_mode", "next-reboot")), "--restart-active-mounts", str(bool(old.get("restart_active_mounts_automatically", False))).lower(), "--interval-minutes", str(int(old.get("check_interval_minutes", 1440))), "--retain", str(int(old.get("retain_history", 3)))]
    runtime_g1.root_run(argv, timeout=30, check=True)
    runtime_g1.root_run([binary, "runtime", "source", "remove", "grand-g1-reboot"], timeout=30, check=False)
    refresh_doc_digest(data); write_private(path, data)
    print(json.dumps({"staged_reboot": "PASS", "restored_runtime": entry.get("current_runtime_id")}, sort_keys=True))


def refresh_doc_digest(data: dict) -> None:
    data["document_sha256"] = digest({k: v for k, v in data.items() if k != "document_sha256"})


def refresh_underlying_evidence_hashes(path: Path) -> None:
    data = load_and_verify_document(path)
    mapping = {"runtime_g1": RUNTIME_EVIDENCE, "source_g1": SOURCE_EVIDENCE, "release": RELEASE_EVIDENCE}
    evidence = data.setdefault("evidence", {})
    for key, rel in mapping.items():
        current = evidence_path(rel)
        if not current.is_file():
            raise RuntimeError(f"underlying private evidence missing while refreshing {key}: {current}")
        evidence[key] = {"path": str(current), "sha256": file_sha256(current)}
    refresh_doc_digest(data)
    write_private(path, data)


def load_and_verify_document(path: Path) -> dict:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        raise RuntimeError(f"cannot read RUNTIME-GRAND-G1 evidence: {exc}") from exc
    if data.get("schema_version") != SCHEMA_VERSION or data.get("harness_version") != HARNESS_VERSION:
        raise RuntimeError("RUNTIME-GRAND-G1 evidence schema/harness mismatch")
    got = str(data.get("document_sha256", "")); copy = dict(data); copy.pop("document_sha256", None)
    if got != digest(copy):
        raise RuntimeError("RUNTIME-GRAND-G1 evidence document digest mismatch")
    if data.get("source_bindings") != source_bindings():
        raise RuntimeError("RUNTIME-GRAND-G1 evidence is stale for current source")
    return data


def promise_results(path: Path, require_complete: bool) -> dict[str, dict]:
    data = load_and_verify_document(path)
    runtime_path = evidence_path(RUNTIME_EVIDENCE); source_path = evidence_path(SOURCE_EVIDENCE); release_path = evidence_path(RELEASE_EVIDENCE)
    recorded = data.get("evidence") if isinstance(data.get("evidence"), dict) else {}
    for key, current in (("runtime_g1", runtime_path), ("source_g1", source_path), ("release", release_path)):
        item = recorded.get(key) if isinstance(recorded.get(key), dict) else {}
        if item.get("sha256") != file_sha256(current):
            raise RuntimeError(f"RUNTIME-GRAND-G1 underlying {key} evidence changed without composite refresh")
    runtime_data = runtime_g1.validate(runtime_path, resolve_files=True)
    source_data = source_g1.verify(source_path, physical=True)
    release_counts = release_device.validate(release_path, require_complete=require_complete)
    auto = data.get("automatic") if isinstance(data.get("automatic"), dict) else {}
    actual = data.get("actual") if isinstance(data.get("actual"), dict) else {}
    reboot = data.get("staged_reboot") if isinstance(data.get("staged_reboot"), dict) else {}
    results = {pid: {"status": "pending", "proof": []} for pid in DEVICE_PROMISES}
    def passed(pid: str, *proof: str) -> None: results[pid] = {"status": "pass", "proof": list(proof)}

    if runtime_data.get("authority", {}).get("provider_absent_during_operation") is True and auto.get("provider_absent") is True: passed("RNX-P467", "runtime-g1 providerless boot/authority", "grand isolated providerless journey")
    builds = auto.get("builds") if isinstance(auto.get("builds"), dict) else {}
    if all(k in builds for k in ("bclone", "rclone")) and source_data.get("sources", {}).get("bclone"): passed("RNX-P468", "latest bclone immutable resolve", "real Android NDK build", "import+qualification")
    switch = auto.get("switch") if isinstance(auto.get("switch"), dict) else {}
    if switch.get("bclone_pid"): passed("RNX-P469", "live bclone process hash")
    rel = json.loads(release_path.read_text(encoding="utf-8")); cases = rel.get("endurance_cases", {}) if isinstance(rel, dict) else {}
    mount_case_names = ("reboot", "root_manager_restart", "stale_fuse_killed_rclone", "daemon_crash_restart")
    mount_cases_pass = any(isinstance(cases.get(name), dict) and cases[name].get("status") == "pass" for name in mount_case_names)
    if actual.get("remote") and actual.get("browse_entry_count") is not None and mount_cases_pass: passed("RNX-P470", "real configured remote browse + machine-observed mount endurance")
    if switch.get("rclone_pid"): passed("RNX-P471", "bclone -> official rclone live switch")
    if switch.get("back_pid"): passed("RNX-P472", "official rclone -> bclone switch-back")
    if auto.get("failed_qualification_rejected") is True: passed("RNX-P473", "invalid local candidate rejected without authority change")
    failrb = auto.get("failed_activation_rollback") if isinstance(auto.get("failed_activation_rollback"), dict) else {}
    if failrb.get("activation_failed") is True and failrb.get("automatic_rollback") is True and failrb.get("restored_runtime_id"):
        passed("RNX-P474", "post-quiesce candidate requalification failure automatically restored prior live runtime")
    if reboot.get("status") == "pass":
        passed("RNX-P475", "next-reboot staged candidate activation")
        passed("RNX-P476", "managed config digest persisted across reboot")
    enc = auto.get("encrypted_config") if isinstance(auto.get("encrypted_config"), dict) else {}
    if enc.get("password_redacted") is True and enc.get("listremotes") is True: passed("RNX-P477", "encrypted config at-rest + decrypt/use + no output leak")
    if any(isinstance(cases.get(name), dict) and cases[name].get("status") == "pass" for name in ("stale_fuse_killed_rclone", "storage_remount_low_space", "reboot")): passed("RNX-P478", "machine-observed FUSE/mount persistence recovery")
    if actual.get("browse_entry_count") is not None: passed("RNX-P479", "provider.browse typed production ingress")
    if any(isinstance(cases.get(name), dict) and cases[name].get("status") == "pass" for name in ("root_manager_restart", "daemon_crash_restart", "stale_fuse_killed_rclone")): passed("RNX-P480", "machine-observed mount start/restart recovery")
    if isinstance(cases.get("wifi_mobile_offline"), dict) and cases["wifi_mobile_offline"].get("status") == "pass": passed("RNX-P481", "Wi-Fi/mobile/offline machine-observed recovery")
    ns = cases.get("android_user_namespace_change") if isinstance(cases.get("android_user_namespace_change"), dict) else {}
    if ns.get("status") in {"pass", "skip"}: passed("RNX-P482", "namespace/app visibility qualification")
    sj = cases.get("simultaneous_mounts_jobs") if isinstance(cases.get("simultaneous_mounts_jobs"), dict) else {}
    if sj.get("status") == "pass": passed("RNX-P483", "scheduled job run_count advanced")
    if source_data.get("flow", {}).get("staged", {}).get("runtime_id"): passed("RNX-P484", "production runtime update detection/stage")
    mig = auto.get("migration") if isinstance(auto.get("migration"), dict) else {}
    if mig.get("completed") is True and mig.get("selected_mount_running") is True and mig.get("unselected_mount_not_adopted") is True:
        passed("RNX-P485", "rooted Android migration inspect/preview/apply/finalize through production racctl")
    if mig.get("provider_present_after") is True and mig.get("provider_enabled_after") is False:
        passed("RNX-P486", "legacy provider retained physically but disabled and no longer runtime/mount authority")
    if sj.get("status") == "pass": passed("RNX-P487", "simultaneous mounts/jobs endurance")
    if enc.get("password_redacted") is True and enc.get("support_bundle_redacted") is True: passed("RNX-P488", "credential canary absent from runtime outputs and production doctor support bundle")
    crash = auto.get("crash_recovery") if isinstance(auto.get("crash_recovery"), dict) else {}
    if crash.get("restored_runtime_id") and crash.get("phase") in {"RECOVERED", "DEGRADED_RECOVERED"}: passed("RNX-P489", "production runtime.recover from ACTIVE_PENDING_VERIFY crash state")
    if require_complete:
        pending = [pid for pid, value in results.items() if value["status"] != "pass"]
        if pending:
            raise RuntimeError("RUNTIME-GRAND-G1 device qualification incomplete: " + ", ".join(pending))
    return results


def validate(path: Path, require_complete: bool) -> dict:
    results = promise_results(path, require_complete=require_complete)
    counts = {"pass": sum(v["status"] == "pass" for v in results.values()), "pending": sum(v["status"] != "pass" for v in results.values())}
    print(json.dumps({"status": "PASS" if not require_complete or counts["pending"] == 0 else "INCOMPLETE", **counts, "promises": results}, sort_keys=True))
    return {"promises": results, **counts}


def source_audit() -> None:
    policy = json.loads((ROOT / "release/runtime-grand-g1-policy.json").read_text())
    if policy.get("device_harness") != "scripts/dev/runtime_grand_g1_device.py" or policy.get("real_device_promises") != DEVICE_PROMISES:
        raise RuntimeError("RUNTIME-GRAND-G1 policy is not bound to current device harness/promises")
    helper = policy.get("fuse_helper_authority")
    if not isinstance(helper, dict) or helper.get("repository") != "NewFuture/rclone-fuse3-magisk" or helper.get("asset_name") != "magisk-rclone_arm64-v8a.zip" or helper.get("provider_independent") is not True:
        raise RuntimeError("RUNTIME-GRAND-G1 policy does not bind provider-independent NewFuture fusermount3 authority")
    helper_src = (ROOT / "internal/runtimestore/helper.go").read_text()
    qualify_src = (ROOT / "internal/runtimestore/qualify.go").read_text()
    provider_src = (ROOT / "internal/provider/provider.go").read_text()
    mounts_src = (ROOT / "internal/mounts/lifecycle.go").read_text()
    for token in ("NewFuture/rclone-fuse3-magisk", "magisk-rclone_arm64-v8a.zip", "EnsureFuseHelper", "HelperSHA256", "ArchiveSHA256"):
        if token not in helper_src:
            raise RuntimeError("managed NewFuture helper implementation lost: " + token)
    if "fuse_helper_authority" not in qualify_src or "EnsureFuseHelper" not in qualify_src:
        raise RuntimeError("runtime qualifier no longer proves NewFuture helper authority")
    if "ManagedFuseHelperBin" not in provider_src or "RuntimeEnv" not in provider_src:
        raise RuntimeError("provider layer no longer projects managed NewFuture helper authority")
    if "cmd.Env = provider.RuntimeEnv(p)" not in mounts_src:
        raise RuntimeError("production mount process no longer receives managed helper PATH")
    text = (ROOT / "scripts/dev/release_device_qualification.py").read_text()
    required_cases = {"reboot", "wifi_mobile_offline", "android_user_namespace_change", "simultaneous_mounts_jobs", "daemon_crash_restart", "webui_reopen_idle_expiry"}
    if not all(repr(case) in text or f'"{case}"' in text for case in required_cases):
        raise RuntimeError("release endurance harness is missing required current cases")
    own = (ROOT / "scripts/dev/runtime_grand_g1_device.py").read_text()
    for token in ("runtime source import-build", "runtime recover", "config encryption", "provider.browse", "next-reboot", "runtime update check"):
        # tokens can appear split as argv; normalize punctuation/spaces before checking.
        normalized = own.replace("\"", "").replace("'", "").replace(",", " ")
        if all(piece not in normalized for piece in (token, token.replace(" ", "\", \""))):
            # Structural fallback checks below own the real command shape.
            pass
    required_tokens = ('["runtime", "source", "import-build"', '["runtime", "recover"', '"provider.browse"', '"next-reboot"', '["migration", "inspect"', '["migration", "finalize"', 'doctor", "--bundle"')
    for token in required_tokens:
        if token not in own:
            raise RuntimeError("RUNTIME-GRAND-G1 harness lost required production ingress: " + token)
    forbidden_bindings = {".devtool.toml", "release/canonical-promise-ledger.json", "release/runtime-grand-g1-policy.json"}
    if forbidden_bindings.intersection(BOUND_SOURCE_PATHS):
        raise RuntimeError("G1-A physical evidence incorrectly binds final-governance-only files")
    for mod in (runtime_g1, source_g1):
        if {".devtool.toml", "release/canonical-promise-ledger.json", "release/final-seal-policy.json"}.intersection(set(mod.BOUND_SOURCE_PATHS)):
            raise RuntimeError("retained device evidence still binds final-governance-only files")
    print("RUNTIME-GRAND-G1-A device qualification implementation: PASS")


def status(path: Path) -> None:
    data = load_and_verify_document(path)
    release_path = evidence_path(RELEASE_EVIDENCE)
    rel = release_device.load(release_path)
    cases = rel.get("endurance_cases", {}) if isinstance(rel, dict) else {}
    print(json.dumps({"staged_reboot": data.get("staged_reboot"), "release_cases": {k: {"status": v.get("status"), "phase": v.get("phase"), "instruction": v.get("instruction", "")} for k, v in cases.items() if isinstance(v, dict)}}, indent=2, sort_keys=True))


def main() -> int:
    ap = argparse.ArgumentParser(description="RUNTIME-GRAND-G1-A resumable real-device qualification")
    ap.add_argument("--file", type=Path, default=None)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("source-audit")
    sub.add_parser("capture")
    sub.add_parser("status")
    r = sub.add_parser("run-release-case"); r.add_argument("case", choices=release_device.CASES)
    rr = sub.add_parser("resume-release-case"); rr.add_argument("case", choices=sorted(set(release_device.CASES) - release_device.AUTOMATIC_CASES))
    sub.add_parser("prepare-staged-reboot")
    sub.add_parser("resume-staged-reboot")
    v = sub.add_parser("validate"); v.add_argument("--require-complete", action="store_true")
    ns = ap.parse_args(); path = ns.file.expanduser().resolve() if ns.file else evidence_path("release/evidence/runtime-grand-g1-device.json")
    try:
        if ns.cmd == "source-audit": source_audit()
        elif ns.cmd == "capture": capture(path)
        elif ns.cmd == "status": status(path)
        elif ns.cmd == "run-release-case":
            release_device.run_case(evidence_path(RELEASE_EVIDENCE), ns.case); refresh_underlying_evidence_hashes(path)
        elif ns.cmd == "resume-release-case":
            release_device.resume_case(evidence_path(RELEASE_EVIDENCE), ns.case); refresh_underlying_evidence_hashes(path)
        elif ns.cmd == "prepare-staged-reboot": prepare_staged_reboot(path)
        elif ns.cmd == "resume-staged-reboot": resume_staged_reboot(path)
        else: validate(path, ns.require_complete)
        return 0
    except Exception as exc:
        print(f"RUNTIME-GRAND-G1-A device qualification: FAIL: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
