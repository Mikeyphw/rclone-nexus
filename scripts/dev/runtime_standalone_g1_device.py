#!/usr/bin/env python3
from __future__ import annotations

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
SCHEMA_VERSION = 1
HARNESS_VERSION = 14
DEFAULT_EVIDENCE = ROOT / "release" / "evidence" / "runtime-g1-device-qualification.json"

BOUND_SOURCE_PATHS = [
    "cmd/racctl/main.go",
    "internal/cache/cache.go",
    "internal/cache/cache_test.go",
    "internal/control/engine.go",
    "internal/daemon/scheduler_test.go",
    "internal/mounts/lifecycle.go",
    "internal/mounts/registry.go",
    "internal/paths/paths.go",
    "internal/provider/provider.go",
    "internal/readiness/readiness.go",
    "internal/runtimeactivation/activation.go",
    "internal/runtimeauth/runtime.go",
    "internal/runtimestate/state.go",
    "internal/runtimestore/helper.go",
    "internal/runtimestore/qualify.go",
    "internal/runtimestore/store.go",
    "module/lib/common.sh",
    "module/service.sh",
    "module/webroot/app.js",
    "scripts/dev/release_device_qualification.py",
    "scripts/dev/runtime_standalone_g1_device.py",
    "scripts/dev/runtime_standalone_g1_gate.py",
]

# Exact filesystem surface owned by the G1 replacement overlay while Devtool is
# validating it before commit. This is intentionally separate from
# BOUND_SOURCE_PATHS: evidence binds some production files that this overlay does
# not modify, and a pre-existing edit to one of those files must still fail the
# capture cleanliness check.
OVERLAY_OWNED_PATHS = frozenset({
    ".devtool.toml",
    ".gitignore",
    "CHANGELOG.md",
    "README.md",
    "docs/implementation/RUNTIME-STANDALONE-G1.md",
    "docs/release/COMPATIBILITY.md",
    "docs/release/INSTALLATION.md",
    "docs/release/RELEASE_NOTES_v0.1.0.md",
    "internal/cache/cache.go",
    "internal/cache/cache_test.go",
    "internal/control/engine.go",
    "internal/daemon/scheduler_test.go",
    "internal/mounts/lifecycle.go",
    "internal/paths/paths.go",
    "internal/provider/provider.go",
    "internal/runtimestore/helper.go",
    "internal/runtimestore/qualify.go",
    "module/customize.sh",
    "release/canonical-promise-ledger.json",
    "release/evidence/README.md",
    "release/evidence/schema-v3.json",
    "release/final-seal-policy.json",
    "scripts/dev/cleanup_validation_outputs.py",
    "scripts/dev/release_artifacts.py",
    "scripts/dev/release_device_qualification.py",
    "scripts/dev/runtime_standalone_g1_device.py",
    "scripts/dev/runtime_standalone_g1_gate.py",
    "scripts/dev/runtime_standalone_x02_gate.py",
    "scripts/dev/runtime_standalone_x03_gate.py",
    "tests/test_release_qualification.py",
    "tests/test_runtime_standalone_g1_device.py",
})


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


def source_bindings() -> dict[str, str]:
    out: dict[str, str] = {}
    for rel in BOUND_SOURCE_PATHS:
        path = ROOT / rel
        if not path.is_file():
            raise RuntimeError(f"RUNTIME-G1 bound source is missing: {rel}")
        out[rel] = file_sha256(path)
    return out


def verify_source_bindings(value: object) -> None:
    if not isinstance(value, dict) or set(value) != set(BOUND_SOURCE_PATHS):
        raise RuntimeError("RUNTIME-G1 source binding set is incomplete/stale")
    current = source_bindings()
    for rel in BOUND_SOURCE_PATHS:
        expected = str(value.get(rel, ""))
        if len(expected) != 64 or expected != current[rel]:
            raise RuntimeError(f"RUNTIME-G1 evidence source binding is stale: {rel}")


def run(argv: list[str], *, timeout: int = 60, env: dict[str, str] | None = None, check: bool = False, binary: bool = False):
    try:
        result = subprocess.run(
            argv,
            cwd=ROOT,
            env=env,
            timeout=timeout,
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=not binary,
        )
    except OSError as exc:
        # Android may surface an OSError not only while spawning a subprocess,
        # but also while subprocess.run() is cleaning up a timed-out privileged
        # child. Preserve that distinction so diagnostics do not call a
        # post-timeout SIGKILL failure a "spawn failure".
        executable = str(argv[0]) if argv else '<empty argv>'
        context = exc.__context__
        timed_out = isinstance(context, subprocess.TimeoutExpired)
        if timed_out:
            detail = (
                f"subprocess timeout cleanup failed after {context.timeout}s: "
                f"executable={executable!r}; "
                f"argv={shlex.join(argv) if argv else '<empty>'}; "
                f"cwd={ROOT}; cleanup_error={exc!r}"
            )
        else:
            detail = (
                f"subprocess spawn/OS operation failed: executable={executable!r}; "
                f"argv={shlex.join(argv) if argv else '<empty>'}; "
                f"cwd={ROOT}; original={exc!r}"
            )
        wrapped = OSError(getattr(exc, 'errno', None), detail, executable)
        setattr(wrapped, 'rnx_argv', list(argv))
        setattr(wrapped, 'rnx_cwd', str(ROOT))
        setattr(wrapped, 'rnx_timeout_cleanup', timed_out)
        if timed_out:
            setattr(wrapped, 'rnx_timeout_seconds', context.timeout)
        raise wrapped from exc
    if check and result.returncode != 0:
        out = result.stdout.decode(errors="replace") if binary else result.stdout
        err = result.stderr.decode(errors="replace") if binary else result.stderr
        raise RuntimeError(f"command failed ({result.returncode}): {shlex.join(argv)}\nstdout={out}\nstderr={err}")
    return result


def shell_join_env(env: dict[str, str], argv: list[str]) -> str:
    prefix = " ".join(f"{key}={shlex.quote(value)}" for key, value in sorted(env.items()))
    command = shlex.join(argv)
    return f"{prefix} {command}" if prefix else command


def root_run(argv: list[str], *, env: dict[str, str] | None = None, timeout: int = 90, check: bool = False, binary: bool = False):
    command = shell_join_env(env or {}, argv)
    if os.geteuid() == 0:
        result = run(["/system/bin/sh" if Path("/system/bin/sh").exists() else "sh", "-c", command], timeout=timeout, check=False, binary=binary)
    else:
        su = shutil.which("su")
        if not su:
            raise RuntimeError("RUNTIME-G1 requires rooted Android (su is unavailable)")
        result = run([su, "-c", command], timeout=timeout, check=False, binary=binary)
    if check and result.returncode != 0:
        out = result.stdout.decode(errors="replace") if binary else result.stdout
        err = result.stderr.decode(errors="replace") if binary else result.stderr
        raise RuntimeError(f"root command failed ({result.returncode}): {command}\nstdout={out}\nstderr={err}")
    return result


def prop(name: str) -> str:
    result = run(["getprop", name], timeout=5)
    return result.stdout.strip() if result.returncode == 0 else ""


def require_android_root() -> None:
    if not prop("ro.build.version.sdk"):
        raise RuntimeError("RUNTIME-G1 device qualification must run on Android/Termux")
    result = root_run(["id", "-u"], timeout=5)
    if result.returncode != 0 or result.stdout.strip() != "0":
        raise RuntimeError("RUNTIME-G1 requires a working root shell")


def root_hash(path: str) -> str:
    local = Path(path)
    try:
        if local.is_file():
            return file_sha256(local)
    except OSError:
        pass
    result = root_run(["sha256sum", path], timeout=30)
    if result.returncode != 0 or not result.stdout.strip():
        raise RuntimeError(f"cannot hash root-owned evidence path: {path}: {result.stderr.strip()}")
    value = result.stdout.split()[0].strip().lower()
    if len(value) != 64 or any(ch not in "0123456789abcdef" for ch in value):
        raise RuntimeError(f"invalid sha256sum output for {path}")
    return value


def root_text(path: str) -> str:
    result = root_run(["cat", path], timeout=15)
    if result.returncode != 0:
        raise RuntimeError(f"cannot read root-owned evidence path {path}: {result.stderr.strip()}")
    return result.stdout


def root_json(path: str) -> dict:
    value = json.loads(root_text(path))
    if not isinstance(value, dict):
        raise RuntimeError(f"JSON evidence is not an object: {path}")
    return value


def root_write_from_local(local: Path, remote: str, mode: str = "0600") -> None:
    root_run(["mkdir", "-p", str(Path(remote).parent)], check=True)
    root_run(["cp", str(local), remote], check=True)
    root_run(["chmod", mode, remote], check=True)


def discover_candidate() -> str:
    explicit = os.environ.get("RNEXUS_RUNTIME_G1_CANDIDATE", "").strip()
    candidates: list[str] = [explicit] if explicit else []
    installed_racctl = "/data/adb/modules/rclone_nexus/system/bin/racctl"
    if root_run(["test", "-x", installed_racctl], timeout=5).returncode == 0:
        result = root_run([installed_racctl, "runtime", "executable"], timeout=15)
        if result.returncode == 0 and result.stdout.strip():
            candidates.append(result.stdout.strip().splitlines()[-1])
    stored = root_run(["/system/bin/sh", "-c", "find /data/adb/rclone-nexus/runtimes -mindepth 2 -maxdepth 2 -type f -name rclone -perm /111 -print 2>/dev/null | sort"], timeout=10)
    if stored.returncode == 0:
        candidates.extend(reversed([line.strip() for line in stored.stdout.splitlines() if line.strip()]))
    candidates.extend([
        "/data/adb/modules/rclone/system/vendor/bin/rclone",
        "/data/adb/modules/rclone/vendor/bin/rclone",
        "/data/adb/modules/rclone/system/bin/rclone",
        "/data/adb/modules/rclone/bin/rclone",
        "/data/adb/modules/rclone/rclone",
    ])
    local = shutil.which("rclone")
    if local:
        candidates.append(local)
    seen: set[str] = set()
    for candidate in candidates:
        candidate = candidate.strip()
        if not candidate or candidate in seen:
            continue
        seen.add(candidate)
        if root_run(["test", "-f", candidate], timeout=5).returncode == 0 and root_run(["test", "-x", candidate], timeout=5).returncode == 0:
            return candidate
    raise RuntimeError("no real rclone/bclone candidate was found; set RNEXUS_RUNTIME_G1_CANDIDATE=/absolute/path")


def parse_json_output(result, label: str) -> dict:
    text = result.stdout.strip()
    try:
        value = json.loads(text)
    except Exception as exc:
        raise RuntimeError(f"{label} did not emit JSON: {text[:1000]!r}") from exc
    if not isinstance(value, dict):
        raise RuntimeError(f"{label} emitted non-object JSON")
    return value


def request_payload(name: str, klass: str, args: dict | None = None) -> dict:
    return {
        "schema_version": 1,
        "request_id": "runtime-g1-" + secrets.token_hex(8),
        "client": {"name": "runtime-g1", "version": "1", "protocol": {"min": 1, "max": 1}},
        "operation": {"name": name, "class": klass, "args": args or {}},
    }


def parse_ndjson_response(text: str) -> dict:
    response: dict | None = None
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        value = json.loads(line)
        if isinstance(value, dict) and value.get("kind") == "response":
            response = value
    if not response or response.get("ok") is not True or not isinstance(response.get("result"), dict):
        raise RuntimeError(f"daemon response is missing/failed: {text[:2000]}")
    return response["result"]


def racctl_env(state: str, module_dir: str, provider_dir: str) -> dict[str, str]:
    # Deliberately do not put any fusermount3 donor on PATH. Production Nexus
    # must acquire the canonical helper from NewFuture and inject its managed
    # helper directory into each rclone process itself.
    helper_path = "/system/bin:/system/xbin:/vendor/bin:/apex/com.android.runtime/bin"
    return {
        "RNEXUS_STATE_DIR": state,
        "RNEXUS_MODULE_DIR": module_dir,
        "RNEXUS_PROVIDER_MODULE_DIR": provider_dir,
        "RNEXUS_RUNTIME_MODE": "managed",
        "RNEXUS_RUNTIME_G1_PRODUCTION_MOUNT_GATE": "1",
        "HOME": "/data/local/tmp",
        "TMPDIR": str(Path(state) / "tmp"),
        "PATH": helper_path,
        # Qualification state is intentionally isolated, but GitHub
        # authentication is operator/device configuration. Point isolated
        # root commands at the canonical Nexus-owned token file rather than
        # copying token bytes into qualification state or evidence.
        "RNEXUS_GITHUB_TOKEN_FILE": os.environ.get(
            "RNEXUS_GITHUB_TOKEN_FILE",
            "/data/adb/rclone-nexus/config/github.token",
        ),
    }


def racctl(root_binary: str, args: list[str], env: dict[str, str], *, timeout: int = 180, check: bool = True):
    return root_run([root_binary, *args], env=env, timeout=timeout, check=check)


def runtime_status(root_binary: str, env: dict[str, str]) -> dict:
    return parse_json_output(racctl(root_binary, ["runtime", "status", "--json"], env), "runtime status")


def parse_mount_status_output(result, name: str) -> dict:
    text = result.stdout.strip()
    try:
        value = json.loads(text)
    except Exception:
        value = None
    if isinstance(value, dict):
        return value

    # The production compat mountctl status surface is intentionally backward
    # compatible with the older tabular output, e.g.
    #   runtime-g1\trunning\tpid=27221
    # RUNTIME-G1 must accept the real production status surface instead of
    # requiring a synthetic JSON-only helper response. Keep this parser strict:
    # it only extracts name/state/pid and later checks still require a live PID
    # whose /proc executable hashes to the selected immutable runtime.
    last = ""
    for line in text.splitlines():
        if line.strip():
            last = line.strip()
    if not last:
        raise RuntimeError("mount status did not emit output")
    fields = [field for field in last.replace("\t", " ").split(" ") if field]
    if len(fields) < 2:
        raise RuntimeError(f"mount status did not emit JSON or tabular status: {text[:1000]!r}")
    parsed_name = fields[0]
    state_raw = fields[1]
    pid = 0
    for field in fields[2:]:
        if field.startswith("pid="):
            try:
                pid = int(field.split("=", 1)[1])
            except ValueError:
                pid = 0
    state = state_raw.upper()
    if state == "RUNNING" and pid <= 1:
        raise RuntimeError(f"mount status reported running without a live pid: {text[:1000]!r}")
    return {"name": parsed_name or name, "state": state, "pid": pid, "format": "tabular"}


def mount_status(root_binary: str, env: dict[str, str], name: str) -> dict:
    return parse_mount_status_output(racctl(root_binary, ["compat", "mountctl", "status", name], env), name)


def process_hash(pid: int) -> str:
    if pid <= 1:
        raise RuntimeError("mount status did not expose a live process pid")
    # Android kernels/SELinux policies can deny direct reads of /proc/<pid>/exe
    # even through a rooted shell. Prefer hashing the proc executable itself;
    # when that is denied, hash only the absolute proc symlink target inside the
    # isolated Nexus G1 qualification runtime store. This keeps the proof bound
    # to the live process identity without reopening PATH/provider candidates.
    script = f'''
set -eu
proc=/proc/{pid}/exe
if sha256sum "$proc" 2>/dev/null; then
  exit 0
fi
target=$(readlink "$proc")
target=${{target% (deleted)}}
case "$target" in
  /data/adb/rclone-nexus/qualification/runtime-g1-*/state/runtimes/*/rclone|/data/adb/rclone-nexus/qualification/runtime-g1-*/module/system/bin/racctl) ;;
  *) echo "unexpected process executable target: $target" >&2; exit 44 ;;
esac
sha256sum "$target"
'''
    result = root_run(["/system/bin/sh", "-c", script], timeout=30)
    if result.returncode != 0 or not result.stdout.strip():
        raise RuntimeError(f"cannot hash process executable for pid {pid}: stdout={result.stdout.strip()!r} stderr={result.stderr.strip()!r}")
    value = result.stdout.split()[0].strip().lower()
    if len(value) != 64 or any(ch not in "0123456789abcdef" for ch in value):
        raise RuntimeError(f"invalid process executable hash for pid {pid}: {result.stdout.strip()!r}")
    return value



def mark_step(name: str) -> None:
    print(f"RUNTIME-G1 device step: {name}", file=sys.stderr, flush=True)

def evidence_ref(path: str, kind: str) -> dict:
    return {"kind": kind, "path": path, "sha256": root_hash(path)}


def snapshot_evidence_ref(source_path: str, snapshot_dir: str, filename: str, kind: str) -> dict:
    """Copy mutable production evidence into an immutable gate-owned snapshot.

    Live desired-state, health, and logs can legitimately change during gate cleanup.
    Hashing those live paths and then stopping the isolated mount makes the evidence
    self-invalidating.  Snapshot the exact observed bytes before cleanup and bind the
    reference to that immutable copy instead.
    """
    if not filename or "/" in filename or filename in {".", ".."}:
        raise RuntimeError(f"invalid evidence snapshot filename: {filename!r}")
    destination = f"{snapshot_dir}/{filename}"
    copy_script = (
        "set -eu\n"
        f"mkdir -p {shlex.quote(snapshot_dir)}\n"
        f"cat {shlex.quote(source_path)} > {shlex.quote(destination)}\n"
        f"chmod 0600 {shlex.quote(destination)}\n"
    )
    result = root_run(["/system/bin/sh", "-c", copy_script], timeout=10)
    if result.returncode != 0:
        raise RuntimeError(
            f"cannot snapshot RUNTIME-G1 evidence {kind}: "
            f"source={source_path!r} stdout={result.stdout.strip()!r} stderr={result.stderr.strip()!r}"
        )
    ref = evidence_ref(destination, kind)
    ref["source_path"] = source_path
    return ref


def build_current_racctl(output: Path) -> None:
    go = shutil.which("go")
    if not go:
        raise RuntimeError("Go is required to build the current racctl source for RUNTIME-G1")
    run([go, "build", "-trimpath", "-o", str(output), "./cmd/racctl"], timeout=180, check=True)




def build_linux_arm64_probe(output: Path) -> None:
    # Build a static Linux/arm64 rclone-shaped probe that executes on Android
    # but can never produce a FUSE mount. RNX-P341 requires proving that
    # execution compatibility alone is insufficient for qualification.
    go = shutil.which("go")
    if not go:
        raise RuntimeError("Go is required to build the Linux/arm64 adversarial probe")
    source = output.with_suffix(".go")
    source.write_text(r'''package main

import (
    "fmt"
    "os"
    "strings"
    "time"
)

const flags = `--config --vfs-cache-mode --cache-dir --log-file --log-level --vfs-cache-max-size --vfs-cache-max-age --dir-cache-time --poll-interval --allow-other --read-only --rc --rc-addr --rc-user --rc-pass`

func main() {
    if len(os.Args) < 2 { return }
    switch os.Args[1] {
    case "version":
        fmt.Println("rclone v999.0.0-linux-arm64-probe")
    case "listremotes":
        fmt.Println("nexus_qual:")
    case "help":
        fmt.Println(flags)
    case "--help":
        fmt.Println(flags)
    case "mount":
        if len(os.Args) > 2 && os.Args[2] == "--help" {
            fmt.Println(flags)
            return
        }
        // Deliberately never mounts. Remaining alive long enough for the
        // qualifier to observe /proc/<pid>/exe proves the Linux binary really
        // executed before the FUSE contract rejects it.
        if strings.Contains(strings.Join(os.Args, " "), "nexus_qual:") {
            time.Sleep(30 * time.Second)
        }
    }
}
''', encoding="utf-8")
    env = os.environ.copy()
    env.update({"GOOS": "linux", "GOARCH": "arm64", "CGO_ENABLED": "0"})
    result = run([go, "build", "-trimpath", "-o", str(output), str(source)], timeout=180, env=env)
    if result.returncode != 0:
        raise RuntimeError(f"cannot build Linux/arm64 adversarial probe: {result.stderr.strip()}")
    output.chmod(0o700)


def copy_gate_module(root_dir: str, built: Path) -> tuple[str, str]:
    module = f"{root_dir}/module"
    root_run(["mkdir", "-p", f"{module}/lib", f"{module}/system/bin"], check=True)
    root_run(["cp", str(ROOT / "module/service.sh"), f"{module}/service.sh"], check=True)
    root_run(["cp", str(ROOT / "module/lib/common.sh"), f"{module}/lib/common.sh"], check=True)
    root_run(["cp", str(ROOT / "module/system/bin/rclone-nexus"), f"{module}/system/bin/rclone-nexus"], check=True)
    root_run(["cp", str(built), f"{module}/system/bin/racctl"], check=True)
    root_run(["chmod", "0700", f"{module}/service.sh", f"{module}/system/bin/rclone-nexus", f"{module}/system/bin/racctl"], check=True)
    return module, f"{module}/system/bin/racctl"

def g1_mount_definition(source_dir: str, mountpoint: str) -> str:
    # This gate uses rclone's local backend. Boot/runtime-authority recovery must
    # therefore be independent of the device's current network topology.
    # `network_mode` is the typed authority; `any` would normalize
    # require_network=false back to network-required and make the boot proof
    # test connectivity policy instead of runtime recovery.
    return (
        "enabled=true\n"
        f"remote=runtimeg1:{source_dir}\n"
        f"mountpoint={mountpoint}\n"
        "vfs_cache_mode=off\n"
        "allow_other=false\n"
        "read_only=true\n"
        "log_level=NOTICE\n"
        "require_network=false\n"
        "probe_remote=false\n"
        "network_mode=offline-allowed\n"
        "cache_high_water=90\n"
        "cache_low_water=75\n"
    )


def rewrite_qualification_true(manifest_path: str) -> None:
    manifest = root_json(manifest_path)
    qualification = manifest.get("qualification")
    if not isinstance(qualification, dict):
        qualification = {}
        manifest["qualification"] = qualification
    qualification["qualified"] = True
    qualification["state"] = "qualified"
    with tempfile.TemporaryDirectory(prefix="rnx-g1-manifest-") as raw:
        local = Path(raw) / "manifest.json"
        local.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        root_write_from_local(local, manifest_path)


def capture_dirty_paths(status_text: str) -> set[str]:
    dirty: set[str] = set()
    for line in status_text.splitlines():
        if not line.strip():
            continue
        # Porcelain-v1 uses two status columns plus a space. Preserve paths that
        # themselves contain leading whitespace by slicing the fixed prefix.
        rel = line[3:] if len(line) >= 3 else line
        if " -> " in rel:
            rel = rel.split(" -> ", 1)[1]
        rel = rel.strip()
        if rel:
            dirty.add(rel)
    return dirty


def unexpected_capture_dirty_paths(status_text: str) -> list[str]:
    return sorted(capture_dirty_paths(status_text) - OVERLAY_OWNED_PATHS)


def capture(path: Path) -> dict:
    mark_step("require rooted Android shell")
    require_android_root()
    tracked = run(["git", "status", "--porcelain", "--untracked-files=all"], timeout=5)
    if tracked.returncode != 0:
        raise RuntimeError("cannot inspect repository state before RUNTIME-G1 capture")
    # Devtool validates overlays before creating their commit. Permit exactly
    # the paths this replacement overlay owns. Evidence-bound production files
    # that are not modified by the overlay are deliberately *not* permitted: a
    # pre-existing edit there would make the rooted proof ambiguous.
    unexpected = unexpected_capture_dirty_paths(tracked.stdout)
    if unexpected:
        raise RuntimeError("RUNTIME-G1 capture found unrelated repository changes: " + ", ".join(unexpected))
    session = "runtime-g1-" + secrets.token_hex(8)
    root_dir = f"/data/adb/rclone-nexus/qualification/{session}"
    state = f"{root_dir}/state"
    provider_absent = f"{root_dir}/provider-absent"
    evidence_dir = f"{root_dir}/evidence"
    root_run(["mkdir", "-p", root_dir, state, evidence_dir], check=True)
    root_run(["chmod", "0700", root_dir, state, evidence_dir], check=True)

    with tempfile.TemporaryDirectory(prefix="rnx-runtime-g1-") as raw:
        local_dir = Path(raw)
        built = local_dir / "racctl"
        mark_step("build current racctl")
        build_current_racctl(built)
        mark_step("discover runtime candidate")
        candidate_source = discover_candidate()
        candidate_source_hash = root_hash(candidate_source)
        mark_step("stage providerless qualification module")
        module_dir, root_binary = copy_gate_module(root_dir, built)

        candidate_a = f"{root_dir}/candidate-a-rclone"
        candidate_b = f"{root_dir}/candidate-b-rclone"
        root_run(["cp", candidate_source, candidate_a], check=True)
        root_run(["cp", candidate_source, candidate_b], check=True)
        # ELF loaders ignore trailing bytes. Give B a distinct immutable digest
        # while retaining the same engine behavior, so activation proves a real
        # byte switch rather than only a different provenance/runtime ID.
        root_run(["/system/bin/sh", "-c", f"printf %s {shlex.quote('runtime-g1-candidate-b-' + session)} >> {shlex.quote(candidate_b)}"], check=True)
        root_run(["chmod", "0500", candidate_a, candidate_b], check=True)
        candidate_a_hash = root_hash(candidate_a)
        candidate_b_hash = root_hash(candidate_b)
        if candidate_a_hash != candidate_source_hash:
            raise RuntimeError("candidate A snapshot bytes changed during gate setup")
        if candidate_b_hash == candidate_a_hash:
            raise RuntimeError("candidate B does not have distinct executable bytes")

        env = racctl_env(state, module_dir, provider_absent)
        root_run(["mkdir", "-p", f"{state}/tmp", f"{state}/config/rclone", f"{state}/mounts.d", f"{state}/run"], check=True)

        local_cfg = local_dir / "rclone.conf"
        local_cfg.write_text("[runtimeg1]\ntype = local\n", encoding="utf-8")
        root_write_from_local(local_cfg, f"{state}/config/rclone/rclone.conf")

        source_dir = f"{root_dir}/source"
        mountpoint = f"{root_dir}/mountpoint"
        root_run(["mkdir", "-p", source_dir, mountpoint], check=True)
        root_run(["/system/bin/sh", "-c", f"printf %s {shlex.quote(session)} > {shlex.quote(source_dir + '/proof.txt')}"], check=True)

        mark_step("import and qualify candidate A")
        imported_a = parse_json_output(racctl(root_binary, ["runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", candidate_a], env), "import candidate A")
        mark_step("import and qualify candidate B")
        imported_b = parse_json_output(racctl(root_binary, ["runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", candidate_b], env), "import candidate B")
        a_id = str(imported_a.get("runtime_id", ""))
        b_id = str(imported_b.get("runtime_id", ""))
        if not a_id or not b_id or a_id == b_id:
            raise RuntimeError("gate candidates did not produce two distinct immutable runtime identities")
        if imported_a.get("qualification", {}).get("qualified") is not True or imported_b.get("qualification", {}).get("qualified") is not True:
            raise RuntimeError("real Android/FUSE qualification did not accept both candidates")
        managed_fuse_helper = f"{state}/runtime/helpers/fusermount3/current/fusermount3"
        managed_fuse_manifest = f"{state}/runtime/helpers/fusermount3/current-v1.json"
        helper_manifest = root_json(managed_fuse_manifest)
        if helper_manifest.get("repository") != "NewFuture/rclone-fuse3-magisk" or helper_manifest.get("asset_name") != "magisk-rclone_arm64-v8a.zip":
            raise RuntimeError("managed fusermount3 is not bound to canonical NewFuture helper authority")
        managed_fuse_helper_hash = root_hash(managed_fuse_helper)
        if managed_fuse_helper_hash != str(helper_manifest.get("helper_sha256", "")):
            raise RuntimeError("managed NewFuture fusermount3 bytes do not match helper manifest")
        for manifest in (imported_a, imported_b):
            checks = {str(item.get("name", "")): str(item.get("status", "")) for item in manifest.get("qualification", {}).get("checks", []) if isinstance(item, dict)}
            if checks.get("fuse_helper_authority") != "pass":
                raise RuntimeError("runtime qualification did not prove canonical NewFuture fusermount3 authority")
        if str(imported_a.get("binary_sha256", "")) == str(imported_b.get("binary_sha256", "")):
            raise RuntimeError("real activation candidates unexpectedly share the same binary digest")

        # RNX-P341: a static Linux/arm64 binary can execute on Android and look
        # CLI-compatible while still failing the real FUSE contract.
        linux_probe_local = local_dir / "linux-arm64-rclone-probe"
        build_linux_arm64_probe(linux_probe_local)
        linux_probe_remote = f"{root_dir}/linux-arm64-rclone-probe"
        root_write_from_local(linux_probe_local, linux_probe_remote, "0700")
        mark_step("prove linux-arm64 execute-but-fail-FUSE negative")
        linux_probe = racctl(root_binary, ["runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", linux_probe_remote], env, check=False)
        linux_manifest = parse_json_output(linux_probe, "Linux/arm64 adversarial import")
        linux_id = str(linux_manifest.get("runtime_id", ""))
        linux_q = linux_manifest.get("qualification", {})
        linux_checks = {str(item.get("name", "")): str(item.get("status", "")) for item in linux_q.get("checks", []) if isinstance(item, dict)} if isinstance(linux_q, dict) else {}
        if linux_probe.returncode == 0 or not linux_id or linux_q.get("qualified") is True or linux_q.get("state") != "rejected":
            raise RuntimeError("plain Linux/arm64 adversarial candidate was not rejected")
        if linux_checks.get("android_execution") != "pass" or linux_checks.get("fuse_smoke_mount") != "fail":
            raise RuntimeError("plain Linux/arm64 candidate did not prove execute-but-fail-FUSE boundary")

        mark_step("activate candidate A")
        activate_a = parse_json_output(racctl(root_binary, ["runtime", "activate", a_id], env), "activate candidate A")
        status_a = runtime_status(root_binary, env)
        if status_a.get("mode") != "managed" or status_a.get("canonical") is not True or status_a.get("operational") is not True or status_a.get("active_runtime_id") != a_id:
            raise RuntimeError("candidate A did not become the canonical operational managed runtime")
        if root_run(["test", "-e", provider_absent]).returncode == 0:
            raise RuntimeError("provider-absent proof root unexpectedly exists before providerless operation proof")

        mount_cfg = local_dir / "runtime-g1.conf"
        mount_cfg.write_text(g1_mount_definition(source_dir, mountpoint), encoding="utf-8")
        root_write_from_local(mount_cfg, f"{state}/mounts.d/runtime-g1.conf")
        mark_step("start production compat mount")
        racctl(root_binary, ["compat", "mountctl", "start", "runtime-g1"], env)
        mark_step("read production mount status and process identity")
        mount_a = mount_status(root_binary, env, "runtime-g1")
        pid_a = int(mount_a.get("pid") or 0)
        proc_a_hash = process_hash(pid_a)
        active_hash = str(imported_a.get("binary_sha256", ""))
        if proc_a_hash != active_hash:
            raise RuntimeError("providerless managed mount process bytes do not match candidate A")
        mark_step("read proof file through real FUSE mount")
        proof_read = root_run(["cat", f"{mountpoint}/proof.txt"], check=True)
        if proof_read.stdout.strip() != session:
            raise RuntimeError("real FUSE mount did not expose qualification source bytes")

        # Managed mode must ignore both PATH and a disabled legacy provider binary.
        poison_dir = f"{root_dir}/poison-path"
        poison_provider = f"{root_dir}/poison-provider"
        root_run(["mkdir", "-p", poison_dir, f"{poison_provider}/system/vendor/bin"], check=True)
        bad = local_dir / "poison-rclone"
        bad.write_text("#!/system/bin/sh\nexit 93\n", encoding="utf-8")
        root_write_from_local(bad, f"{poison_dir}/rclone", "0700")
        root_write_from_local(bad, f"{poison_provider}/system/vendor/bin/rclone", "0700")
        root_run(["touch", f"{poison_provider}/disable"], check=True)
        poison_env = dict(env)
        poison_env["RNEXUS_PROVIDER_MODULE_DIR"] = poison_provider
        poison_env["PATH"] = poison_dir + ":/system/bin:/system/xbin:/vendor/bin"
        selected = racctl(root_binary, ["runtime", "executable"], poison_env).stdout.strip()
        if selected != f"{state}/runtimes/{a_id}/rclone":
            raise RuntimeError("PATH/provider state redirected managed runtime selection")

        # Poison the historical active projection too; activation state must remain upstream.
        projection = f"{state}/runtime/active/bin/rclone"
        root_run(["rm", "-f", projection], check=True)
        root_write_from_local(bad, projection, "0700")
        projection_selected = racctl(root_binary, ["runtime", "executable"], env).stdout.strip()
        if projection_selected != f"{state}/runtimes/{a_id}/rclone":
            raise RuntimeError("runtime/active projection became an executable authority")

        mark_step("activate candidate B and verify restart")
        activate_b = parse_json_output(racctl(root_binary, ["runtime", "activate", b_id], env), "activate candidate B")
        status_b = runtime_status(root_binary, env)
        mount_b = mount_status(root_binary, env, "runtime-g1")
        pid_b = int(mount_b.get("pid") or 0)
        proc_b_hash = process_hash(pid_b)
        if status_b.get("active_runtime_id") != b_id or proc_b_hash != str(imported_b.get("binary_sha256", "")) or pid_b == pid_a:
            raise RuntimeError("candidate B activation did not restart the real mount under the selected immutable runtime")
        if proc_b_hash == active_hash:
            raise RuntimeError("candidate B activation did not switch to distinct executable bytes")

        mark_step("rollback to candidate A")
        rollback = parse_json_output(racctl(root_binary, ["runtime", "rollback"], env), "rollback candidate B")
        status_rollback = runtime_status(root_binary, env)
        mount_rollback = mount_status(root_binary, env, "runtime-g1")
        pid_rollback = int(mount_rollback.get("pid") or 0)
        proc_rollback_hash = process_hash(pid_rollback)
        if status_rollback.get("active_runtime_id") != a_id or proc_rollback_hash != active_hash or pid_rollback == pid_b:
            raise RuntimeError("rollback did not restore candidate A and restart the real mount")

        # A forged qualification bit must not bypass activation-time requalification.
        invalid_local = local_dir / "not-rclone"
        invalid_local.write_bytes(b"\x7fELF\x02\x01runtime-g1-invalid\n")
        invalid_remote = f"{root_dir}/invalid-rclone"
        root_write_from_local(invalid_local, invalid_remote, "0700")
        rejected = racctl(root_binary, ["runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", invalid_remote], env, check=False)
        rejected_manifest = parse_json_output(rejected, "rejected import")
        rejected_id = str(rejected_manifest.get("runtime_id", ""))
        if rejected.returncode == 0 or not rejected_id or rejected_manifest.get("qualification", {}).get("qualified") is True:
            raise RuntimeError("invalid candidate was not rejected before synthetic-qualification test")
        rejected_manifest_path = f"{state}/runtimes/{rejected_id}/manifest.json"
        rewrite_qualification_true(rejected_manifest_path)
        forged = racctl(root_binary, ["runtime", "activate", rejected_id], env, check=False)
        if forged.returncode == 0:
            raise RuntimeError("synthetic qualified=true manifest bypassed activation-time real checks")
        forged_after = root_json(rejected_manifest_path)
        if forged_after.get("qualification", {}).get("qualified") is True:
            raise RuntimeError("activation-time verifier did not overwrite forged qualification evidence")

        # Simulate process loss after rollback while preserving desired state.
        # The production service ingress must recover from durable activation
        # state and reconcile a fresh mount under the rolled-back runtime bytes.
        mark_step("simulate mount process loss")
        root_run(["kill", "-KILL", str(pid_rollback)], timeout=5, check=True)
        deadline = time.time() + 8
        while time.time() < deadline and root_run(["kill", "-0", str(pid_rollback)], timeout=2).returncode == 0:
            time.sleep(0.1)

        # Exercise the real boot service ingress, then daemon and WebUI bridge over racd.
        # service.sh may legitimately remain attached to a rooted child while
        # racd/reconcile completes. Do not let Python's non-root subprocess
        # timeout attempt to kill a rooted su child (which surfaces as a raw
        # EPERM). Launch the production service from inside the root shell,
        # persist its pid/rc/logs under the isolated qualification state, and
        # judge success by the actual boot-recovered runtime/mount/daemon
        # evidence below.
        root_run(["touch", f"{state}/run/platform-ready"], check=True)
        mark_step("run production service boot recovery")
        service_pid_file = f"{state}/run/g1-service.pid"
        service_rc_file = f"{state}/run/g1-service.rc"
        service_out = f"{state}/logs/g1-service.stdout"
        service_err = f"{state}/logs/g1-service.stderr"
        service_runner = f"{state}/run/g1-service-runner.sh"
        mark_step("launch production service.sh under root")
        runner_exports = "\n".join(
            f"export {key}={shlex.quote(value)}" for key, value in sorted(env.items())
        )
        runner_body = (
            "#!/system/bin/sh\n"
            "set +e\n"
            f"{runner_exports}\n"
            "cd /\n"
            "exec </dev/null\n"
            f"{shlex.quote(module_dir + '/service.sh')} >{shlex.quote(service_out)} 2>{shlex.quote(service_err)}\n"
            "rc=$?\n"
            f"echo $rc >{shlex.quote(service_rc_file)}\n"
            "exit $rc\n"
        )
        runner_local = local_dir / "g1-service-runner.sh"
        runner_local.write_text(runner_body, encoding="utf-8")
        root_write_from_local(runner_local, service_runner, "0700")
        service_launch = (
            "set -eu\n"
            f"rm -f {shlex.quote(service_pid_file)} {shlex.quote(service_rc_file)}\n"
            f"mkdir -p {shlex.quote(state + '/run')} {shlex.quote(state + '/logs')}\n"
            "detacher=''\n"
            "if command -v setsid >/dev/null 2>&1; then detacher=setsid; "
            "elif /system/bin/toybox setsid /system/bin/true >/dev/null 2>&1; then detacher='/system/bin/toybox setsid'; "
            "elif command -v nohup >/dev/null 2>&1; then detacher=nohup; fi\n"
            f"if [ -n \"$detacher\" ]; then $detacher /system/bin/sh {shlex.quote(service_runner)} </dev/null >/dev/null 2>&1 & else /system/bin/sh {shlex.quote(service_runner)} </dev/null >/dev/null 2>&1 & fi\n"
            f"echo $! >{shlex.quote(service_pid_file)}\n"
            "exit 0\n"
        )
        root_run(["/system/bin/sh", "-c", service_launch], timeout=15, check=True)
        deadline = time.time() + 5
        while time.time() < deadline and root_run(["test", "-s", service_pid_file], timeout=3).returncode != 0:
            time.sleep(0.1)
        if root_run(["test", "-s", service_pid_file], timeout=3).returncode != 0:
            log_tail = root_run(["/system/bin/sh", "-c", f"tail -80 {shlex.quote(service_out)} 2>/dev/null; tail -80 {shlex.quote(service_err)} 2>/dev/null"], timeout=10).stdout.strip()
            raise RuntimeError(f"production service.sh launcher did not persist a pid: logs={log_tail}")
        socket = f"{state}/run/racd.sock"
        mark_step("wait for production racd socket")
        deadline = time.time() + 12
        while time.time() < deadline and root_run(["test", "-S", socket], timeout=3).returncode != 0:
            time.sleep(0.25)
        if root_run(["test", "-S", socket], timeout=3).returncode != 0:
            service_log = f"{state}/logs/service.log"
            log_tail = root_run(["/system/bin/sh", "-c", f"tail -80 {shlex.quote(service_log)} 2>/dev/null; tail -80 {shlex.quote(service_out)} 2>/dev/null; tail -80 {shlex.quote(service_err)} 2>/dev/null; cat {shlex.quote(service_rc_file)} 2>/dev/null || true"], timeout=10).stdout.strip()
            raise RuntimeError(f"boot ingress did not establish the production racd socket: logs={log_tail}")

        # The G1 mount is a local-backend fixture. Prove the normalized typed
        # policy is not accidentally network-gating boot recovery before judging
        # runtime reconciliation. This closes the v14/v15 false attribution where
        # `require_network=false` was paired with authoritative `network_mode=any`.
        mark_step("verify boot fixture policy is offline-allowed")
        boot_policy = parse_json_output(
            racctl(root_binary, ["compat", "nexus", "policy", "runtime-g1"], env),
            "boot fixture policy",
        )
        boot_decision = boot_policy.get("decision") if isinstance(boot_policy.get("decision"), dict) else {}
        if boot_policy.get("network_mode") != "offline-allowed" or boot_decision.get("allowed") is not True:
            raise RuntimeError(f"RUNTIME-G1 local boot fixture is unexpectedly policy-blocked: {boot_policy!r}")

        mark_step("wait for boot-reconciled mount")
        deadline = time.time() + 45
        boot_mount: dict = {}
        last_boot_error = ""
        while time.time() < deadline:
            try:
                boot_mount = mount_status(root_binary, env, "runtime-g1")
            except Exception as exc:
                last_boot_error = str(exc)
                boot_mount = {}
            boot_pid = int(boot_mount.get("pid") or 0)
            if boot_mount.get("state") == "RUNNING" and boot_pid > 1 and boot_pid != pid_rollback:
                break
            time.sleep(0.25)
        boot_pid = int(boot_mount.get("pid") or 0)
        if boot_mount.get("state") != "RUNNING" or boot_pid <= 1 or boot_pid == pid_rollback:
            service_log = f"{state}/logs/service.log"
            mount_log = f"{state}/logs/mount-runtime-g1.log"
            health_diag = racctl(root_binary, ["compat", "nexus", "health", "runtime-g1"], env, check=False, timeout=30)
            policy_diag = racctl(root_binary, ["compat", "nexus", "policy", "runtime-g1"], env, check=False, timeout=30)
            desired_diag = root_run(["cat", f"{state}/desired/runtime-g1.json"], timeout=5)
            log_tail = root_run(["/system/bin/sh", "-c", f"tail -80 {shlex.quote(service_log)} 2>/dev/null; tail -80 {shlex.quote(mount_log)} 2>/dev/null"], timeout=10).stdout.strip()
            raise RuntimeError(
                "boot reconcile did not restart the desired mount after rollback/process loss: "
                f"status={boot_mount!r} last_error={last_boot_error!r} "
                f"health={health_diag.stdout.strip()!r} policy={policy_diag.stdout.strip()!r} "
                f"desired={desired_diag.stdout.strip()!r} logs={log_tail}"
            )
        mark_step("check production service launch did not fail early")
        service_rc_read = root_run(["/system/bin/sh", "-c", f"cat {shlex.quote(service_rc_file)} 2>/dev/null || true"], timeout=5).stdout.strip()
        if service_rc_read and service_rc_read != "0":
            service_log = f"{state}/logs/service.log"
            log_tail = root_run(["/system/bin/sh", "-c", f"tail -80 {shlex.quote(service_log)} 2>/dev/null; tail -80 {shlex.quote(service_out)} 2>/dev/null; tail -80 {shlex.quote(service_err)} 2>/dev/null"], timeout=10).stdout.strip()
            raise RuntimeError(f"production service.sh exited nonzero during boot recovery: rc={service_rc_read} logs={log_tail}")
        mark_step("hash boot-reconciled mount process")
        boot_process_hash = process_hash(boot_pid)
        if boot_process_hash != active_hash:
            raise RuntimeError("boot reconcile restarted the mount under bytes other than the rolled-back runtime")
        mark_step("verify boot supervisor health")
        boot_health = parse_json_output(
            racctl(root_binary, ["compat", "nexus", "health", "runtime-g1"], env),
            "boot supervisor health",
        )
        if boot_health.get("state") != "RUNNING" or boot_health.get("desired") != "running":
            raise RuntimeError(f"boot supervisor did not converge to RUNNING desired state: {boot_health!r}")

        req = request_payload("runtime.status", "query")
        # rpc reads stdin, so issue it through a private request file.
        req_local = local_dir / "request.json"
        req_local.write_text(json.dumps(req) + "\n", encoding="utf-8")
        req_remote = f"{root_dir}/request.json"
        root_write_from_local(req_local, req_remote)
        mark_step("query daemon RPC runtime identity")
        daemon_result = root_run(["/system/bin/sh", "-c", f"cat {shlex.quote(req_remote)} | {shlex.quote(root_binary)} rpc"], env=env, timeout=30, check=True)
        daemon_status = parse_ndjson_response(daemon_result.stdout)

        raw = json.dumps(req, separators=(",", ":")).encode()
        encoded = base64.urlsafe_b64encode(raw).rstrip(b"=").decode()
        mark_step("query WebUI bridge runtime identity")
        web_result = parse_json_output(racctl(root_binary, ["webui", "bridge", "--request-base64", encoded], env), "WebUI bridge")
        response = web_result.get("response")
        web_status = response.get("result") if isinstance(response, dict) and response.get("ok") is True else None
        if not isinstance(web_status, dict):
            raise RuntimeError("WebUI bridge did not return runtime.status")
        boot_status = runtime_status(root_binary, env)
        for label, value in (("boot", boot_status), ("daemon", daemon_status), ("webui", web_status)):
            if value.get("active_runtime_id") != a_id or value.get("canonical") is not True or value.get("operational") is not True:
                raise RuntimeError(f"{label} ingress did not select the same canonical runtime after rollback")

        if root_run(["test", "-e", provider_absent], timeout=5).returncode == 0:
            raise RuntimeError("providerless managed operation created/required a provider module root")

        activation_state = f"{state}/runtime/activation-v1.json"
        state_json = root_json(activation_state)
        tx_dir = f"{state}/runtime/transactions"
        receipts = root_run(["/system/bin/sh", "-c", f"find {shlex.quote(tx_dir)} -maxdepth 1 -type f -name '*.json' -print | sort"], check=True).stdout.splitlines()
        if len(receipts) < 3:
            raise RuntimeError("activation/rollback flow did not persist enough transaction receipts")

        # Snapshot every mutable state/log reference before cleanup.  Cleanup is
        # expected to change desired-state and may append to health/log files, so
        # evidence must bind the exact production bytes observed at the successful
        # boot-recovery boundary rather than live paths that are about to mutate.
        snapshot_dir = f"{root_dir}/evidence/snapshot"
        refs = [
            evidence_ref(root_binary, "gate-racctl"),
            evidence_ref(managed_fuse_helper, "fuse-helper"),
            evidence_ref(managed_fuse_manifest, "fuse-helper-manifest"),
            evidence_ref(f"{state}/runtimes/{a_id}/rclone", "runtime-binary-a"),
            evidence_ref(f"{state}/runtimes/{a_id}/manifest.json", "runtime-manifest-a"),
            evidence_ref(f"{state}/runtimes/{b_id}/rclone", "runtime-binary-b"),
            evidence_ref(f"{state}/runtimes/{b_id}/manifest.json", "runtime-manifest-b"),
            evidence_ref(f"{state}/runtimes/{linux_id}/rclone", "linux-arm64-probe-binary"),
            evidence_ref(f"{state}/runtimes/{linux_id}/manifest.json", "linux-arm64-probe-manifest"),
            snapshot_evidence_ref(activation_state, snapshot_dir, "activation-state.json", "activation-state"),
            snapshot_evidence_ref(f"{state}/mounts.d/runtime-g1.conf", snapshot_dir, "runtime-g1.conf", "mount-config"),
            snapshot_evidence_ref(f"{state}/desired/runtime-g1.json", snapshot_dir, "desired-state.json", "desired-state"),
            snapshot_evidence_ref(f"{state}/health/runtime-g1.json", snapshot_dir, "boot-health.json", "boot-health"),
        ]
        for receipt in receipts:
            refs.append(evidence_ref(receipt, "transaction-receipt"))
        mount_log = f"{state}/logs/mount-runtime-g1.log"
        if root_run(["test", "-f", mount_log]).returncode == 0:
            refs.append(snapshot_evidence_ref(mount_log, snapshot_dir, "mount-runtime-g1.log", "mount-log"))
        service_log = f"{state}/logs/service.log"
        if root_run(["test", "-f", service_log]).returncode == 0:
            refs.append(snapshot_evidence_ref(service_log, snapshot_dir, "service.log", "boot-service-log"))
        for service_artifact, kind, filename in (
            (service_out, "boot-service-stdout", "service.stdout"),
            (service_err, "boot-service-stderr", "service.stderr"),
            (service_pid_file, "boot-service-pid", "service.pid"),
        ):
            if root_run(["test", "-f", service_artifact]).returncode == 0:
                refs.append(snapshot_evidence_ref(service_artifact, snapshot_dir, filename, kind))

        # Stop only the isolated gate mount/daemon after immutable evidence has
        # been captured. Durable snapshots remain under qualification/evidence/.
        racctl(root_binary, ["compat", "mountctl", "stop", "runtime-g1"], env, check=False)
        lock = f"{state}/run/racd.lock"
        if root_run(["test", "-f", lock]).returncode == 0:
            pid_text = root_text(lock).strip()
            if pid_text.isdigit():
                root_run(["kill", "-TERM", pid_text], timeout=5)
        if root_run(["test", "-f", service_pid_file], timeout=5).returncode == 0:
            svc_pid = root_text(service_pid_file).strip()
            if svc_pid.isdigit():
                root_run(["kill", "-TERM", svc_pid], timeout=5)

        data = {
            "schema_version": SCHEMA_VERSION,
            "harness": "scripts/dev/runtime_standalone_g1_device.py",
            "harness_version": HARNESS_VERSION,
            "captured_at": now(),
            "session_id": session,
            "status": "PASS",
            "device": {
                "sdk": prop("ro.build.version.sdk"),
                "release": prop("ro.build.version.release"),
                "fingerprint_sha256": hashlib.sha256(prop("ro.build.fingerprint").encode()).hexdigest(),
            },
            "source": {
                "parent_head": run(["git", "rev-parse", "HEAD"], timeout=5).stdout.strip(),
                "bindings": source_bindings(),
                "source_digest": digest(source_bindings()),
                "built_racctl_sha256": root_hash(root_binary),
                "fuse_helper_sha256": managed_fuse_helper_hash,
                "fuse_helper_repository": str(helper_manifest.get("repository", "")),
                "fuse_helper_release_tag": str(helper_manifest.get("release_tag", "")),
                "fuse_helper_asset_id": int(helper_manifest.get("asset_id") or 0),
                "fuse_helper_archive_sha256": str(helper_manifest.get("archive_sha256", "")),
            },
            "candidate": {
                "source_sha256": candidate_source_hash,
                "runtime_a": a_id, "runtime_b": b_id,
                "binary_sha256": active_hash,
                "binary_a_sha256": str(imported_a.get("binary_sha256", "")),
                "binary_b_sha256": str(imported_b.get("binary_sha256", "")),
            },
            "authority": {
                "provider_absent_during_operation": True,
                "path_provider_poison_ignored": selected == f"{state}/runtimes/{a_id}/rclone",
                "active_projection_poison_ignored": projection_selected == f"{state}/runtimes/{a_id}/rclone",
                "cli_runtime_id": str(status_rollback.get("active_runtime_id", "")),
                "boot_runtime_id": str(boot_status.get("active_runtime_id", "")),
                "daemon_runtime_id": str(daemon_status.get("active_runtime_id", "")),
                "webui_runtime_id": str(web_status.get("active_runtime_id", "")),
            },
            "flow": {
                "activate_a": {"runtime_id": a_id, "transaction_id": activate_a.get("transaction_id", ""), "mount_pid": pid_a, "process_sha256": proc_a_hash},
                "activate_b": {"runtime_id": b_id, "transaction_id": activate_b.get("transaction_id", ""), "mount_pid": pid_b, "process_sha256": proc_b_hash},
                "rollback": {"runtime_id": str(status_rollback.get("active_runtime_id", "")), "transaction_id": rollback.get("transaction_id", ""), "mount_pid": pid_rollback, "process_sha256": proc_rollback_hash},
                "boot_restart": {"runtime_id": str(boot_status.get("active_runtime_id", "")), "mount_pid": boot_pid, "process_sha256": boot_process_hash},
                "boot_recovery_phase": state_json.get("phase", ""),
                "boot_health_state": str(boot_health.get("state", "")),
                "boot_desired_state": str(boot_health.get("desired", "")),
                "boot_policy_network_mode": str(boot_policy.get("network_mode", "")),
                "boot_policy_allowed": boot_decision.get("allowed") is True,
                "synthetic_qualification_rejected": forged.returncode != 0,
                "synthetic_runtime_id": rejected_id,
                "linux_arm64_android_fuse_rejected": linux_probe.returncode != 0 and linux_checks.get("android_execution") == "pass" and linux_checks.get("fuse_smoke_mount") == "fail",
                "linux_arm64_runtime_id": linux_id,
            },
            "evidence": refs,
            "workspace": root_dir,
        }
        data["evidence_digest"] = digest({k: v for k, v in data.items() if k != "evidence_digest"})
        path.parent.mkdir(parents=True, exist_ok=True)
        mark_step("write and validate device evidence")
        path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        path.chmod(0o600)
        validate(path, resolve_files=True)
        return data


def validate(path: Path, *, resolve_files: bool = True) -> dict:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        raise RuntimeError(f"cannot read RUNTIME-G1 evidence: {exc}") from exc
    if not isinstance(data, dict) or data.get("schema_version") != SCHEMA_VERSION or data.get("harness_version") != HARNESS_VERSION:
        raise RuntimeError("RUNTIME-G1 evidence schema/harness mismatch")
    if data.get("status") != "PASS" or not str(data.get("captured_at", "")).strip() or not str(data.get("session_id", "")).strip():
        raise RuntimeError("RUNTIME-G1 evidence is not a completed PASS")
    expected_digest = digest({k: v for k, v in data.items() if k != "evidence_digest"})
    if data.get("evidence_digest") != expected_digest:
        raise RuntimeError("RUNTIME-G1 evidence digest does not bind the document")
    device = data.get("device")
    source = data.get("source")
    authority = data.get("authority")
    flow = data.get("flow")
    candidate = data.get("candidate")
    if not all(isinstance(v, dict) for v in (device, source, authority, flow, candidate)):
        raise RuntimeError("RUNTIME-G1 evidence sections are malformed")
    if not str(device.get("sdk", "")).strip() or len(str(device.get("fingerprint_sha256", ""))) != 64:
        raise RuntimeError("RUNTIME-G1 evidence lacks Android identity")
    if not str(source.get("parent_head", "")).strip() or len(str(source.get("built_racctl_sha256", ""))) != 64 or len(str(source.get("source_digest", ""))) != 64:
        raise RuntimeError("RUNTIME-G1 evidence lacks source/binary identity")
    if len(str(source.get("fuse_helper_sha256", ""))) != 64 or len(str(source.get("fuse_helper_archive_sha256", ""))) != 64:
        raise RuntimeError("RUNTIME-G1 evidence lacks Nexus-owned FUSE helper identity")
    if source.get("fuse_helper_repository") != "NewFuture/rclone-fuse3-magisk" or not str(source.get("fuse_helper_release_tag", "")).strip() or int(source.get("fuse_helper_asset_id") or 0) <= 0:
        raise RuntimeError("RUNTIME-G1 FUSE helper is not bound to immutable NewFuture release/asset authority")
    verify_source_bindings(source.get("bindings"))
    if source.get("source_digest") != digest(source_bindings()):
        raise RuntimeError("RUNTIME-G1 source digest is stale")
    a_id, b_id = str(candidate.get("runtime_a", "")), str(candidate.get("runtime_b", ""))
    a_hash = str(candidate.get("binary_a_sha256", candidate.get("binary_sha256", "")))
    b_hash = str(candidate.get("binary_b_sha256", ""))
    if not a_id or not b_id or a_id == b_id or len(a_hash) != 64 or len(b_hash) != 64 or a_hash == b_hash:
        raise RuntimeError("RUNTIME-G1 candidate identity/digest switch is incomplete")
    ids = [str(authority.get(key, "")) for key in ("cli_runtime_id", "boot_runtime_id", "daemon_runtime_id", "webui_runtime_id")]
    if any(value != a_id for value in ids):
        raise RuntimeError("CLI/boot/daemon/WebUI do not resolve the same rollback runtime")
    if authority.get("provider_absent_during_operation") is not True or authority.get("path_provider_poison_ignored") is not True or authority.get("active_projection_poison_ignored") is not True:
        raise RuntimeError("single-authority/providerless/poison resistance proof is incomplete")
    for key in ("activate_a", "activate_b", "rollback", "boot_restart"):
        value = flow.get(key)
        if not isinstance(value, dict) or int(value.get("mount_pid") or 0) <= 1 or len(str(value.get("process_sha256", ""))) != 64:
            raise RuntimeError(f"RUNTIME-G1 flow evidence is incomplete: {key}")
    if flow.get("synthetic_qualification_rejected") is not True or flow.get("linux_arm64_android_fuse_rejected") is not True or str(flow.get("boot_recovery_phase", "")) not in {"ACTIVE", "RECOVERED", "DEGRADED_RECOVERED"}:
        raise RuntimeError("negative qualification/Linux-arm64 boundary/boot recovery proof is incomplete")
    if flow.get("boot_health_state") != "RUNNING" or flow.get("boot_desired_state") != "running" or flow.get("boot_policy_network_mode") != "offline-allowed" or flow.get("boot_policy_allowed") is not True:
        raise RuntimeError("boot recovery evidence is not bound to a desired/running offline-allowed local fixture")
    expected_binary = a_hash
    if str(flow.get("activate_a", {}).get("process_sha256", "")) != expected_binary or str(flow.get("rollback", {}).get("process_sha256", "")) != expected_binary:
        raise RuntimeError("activation/rollback process evidence does not bind to candidate A bytes")
    if str(flow.get("activate_b", {}).get("process_sha256", "")) != b_hash:
        raise RuntimeError("candidate B process evidence does not bind to distinct candidate B bytes")
    if str(flow.get("boot_restart", {}).get("runtime_id", "")) != a_id or str(flow.get("boot_restart", {}).get("process_sha256", "")) != expected_binary:
        raise RuntimeError("rollback did not survive process-loss/boot reconcile under the same runtime bytes")
    refs = data.get("evidence")
    if not isinstance(refs, list) or len(refs) < 8:
        raise RuntimeError("RUNTIME-G1 does not resolve enough physical evidence references")
    kinds = {str(ref.get("kind", "")) for ref in refs if isinstance(ref, dict)}
    required_kinds = {"gate-racctl", "fuse-helper", "fuse-helper-manifest", "runtime-binary-a", "runtime-manifest-a", "runtime-binary-b", "runtime-manifest-b", "linux-arm64-probe-binary", "linux-arm64-probe-manifest", "activation-state", "mount-config", "desired-state", "boot-health", "transaction-receipt"}
    if not required_kinds.issubset(kinds):
        raise RuntimeError("RUNTIME-G1 physical evidence set is incomplete")
    mutable_snapshot_kinds = {
        "activation-state", "mount-config", "desired-state", "boot-health",
        "mount-log", "boot-service-log", "boot-service-stdout",
        "boot-service-stderr", "boot-service-pid",
    }
    snapshot_prefix = str(data.get("workspace", "")).rstrip("/") + "/evidence/snapshot/"
    for ref in refs:
        if not isinstance(ref, dict):
            raise RuntimeError("RUNTIME-G1 evidence reference is malformed")
        kind = str(ref.get("kind", ""))
        if kind in mutable_snapshot_kinds:
            ref_path = str(ref.get("path", ""))
            source_path = str(ref.get("source_path", ""))
            if not snapshot_prefix.startswith("/") or not ref_path.startswith(snapshot_prefix) or not source_path.startswith("/"):
                raise RuntimeError(f"RUNTIME-G1 mutable evidence must resolve through an immutable gate snapshot: {kind}")
    if resolve_files:
        for ref in refs:
            if not isinstance(ref, dict):
                raise RuntimeError("RUNTIME-G1 evidence reference is malformed")
            ref_path = str(ref.get("path", ""))
            expected = str(ref.get("sha256", ""))
            if not ref_path or len(expected) != 64 or root_hash(ref_path) != expected:
                raise RuntimeError(f"RUNTIME-G1 evidence reference is missing/stale: {ref_path}")
        by_kind = {str(ref.get("kind", "")): str(ref.get("path", "")) for ref in refs if isinstance(ref, dict)}
        desired_snapshot = root_json(by_kind["desired-state"])
        if desired_snapshot.get("state") != "running":
            raise RuntimeError("RUNTIME-G1 desired-state snapshot does not prove running intent at boot recovery")
        health_snapshot = root_json(by_kind["boot-health"])
        if health_snapshot.get("state") != "RUNNING" or health_snapshot.get("desired") != "running":
            raise RuntimeError("RUNTIME-G1 boot-health snapshot does not prove RUNNING desired state")
        activation_snapshot = root_json(by_kind["activation-state"])
        if str(activation_snapshot.get("active_runtime_id", "")) != a_id or str(activation_snapshot.get("phase", "")) not in {"ACTIVE", "RECOVERED", "DEGRADED_RECOVERED"}:
            raise RuntimeError("RUNTIME-G1 activation-state snapshot does not bind the boot-recovered runtime")
        mount_config_snapshot = root_text(by_kind["mount-config"])
        if "network_mode=offline-allowed" not in mount_config_snapshot:
            raise RuntimeError("RUNTIME-G1 mount-config snapshot does not preserve offline-allowed boot fixture policy")
        manifest = root_json(by_kind["runtime-manifest-a"])
        qualification = manifest.get("qualification")
        if not isinstance(qualification, dict) or qualification.get("qualified") is not True or qualification.get("state") != "qualified":
            raise RuntimeError("RUNTIME-G1 runtime A manifest is not really qualified")
        checks = {str(item.get("name", "")): str(item.get("status", "")) for item in qualification.get("checks", []) if isinstance(item, dict)}
        for name in ("architecture", "android_execution", "rc_runtime", "fuse_smoke_mount", "signal_termination", "process_ownership", "post_qualification_hash"):
            if checks.get(name) != "pass":
                raise RuntimeError(f"RUNTIME-G1/X02 real-device qualification check did not pass: {name}={checks.get(name)!r}")
        linux_manifest = root_json(by_kind["linux-arm64-probe-manifest"])
        linux_q = linux_manifest.get("qualification")
        linux_checks = {str(item.get("name", "")): str(item.get("status", "")) for item in linux_q.get("checks", []) if isinstance(item, dict)} if isinstance(linux_q, dict) else {}
        if not isinstance(linux_q, dict) or linux_q.get("qualified") is True or linux_q.get("state") != "rejected" or linux_checks.get("android_execution") != "pass" or linux_checks.get("fuse_smoke_mount") != "fail":
            raise RuntimeError("RNX-P341 Linux/arm64 execute-but-fail-FUSE evidence is not real")
        if root_hash(by_kind["gate-racctl"]) != str(source.get("built_racctl_sha256", "")):
            raise RuntimeError("RUNTIME-G1 built racctl evidence hash is stale")
        if root_hash(by_kind["fuse-helper"]) != str(source.get("fuse_helper_sha256", "")):
            raise RuntimeError("RUNTIME-G1 managed FUSE helper evidence hash is stale")
        helper_manifest = root_json(by_kind["fuse-helper-manifest"])
        if helper_manifest.get("repository") != source.get("fuse_helper_repository") or helper_manifest.get("release_tag") != source.get("fuse_helper_release_tag") or int(helper_manifest.get("asset_id") or 0) != int(source.get("fuse_helper_asset_id") or 0) or helper_manifest.get("helper_sha256") != source.get("fuse_helper_sha256") or helper_manifest.get("archive_sha256") != source.get("fuse_helper_archive_sha256"):
            raise RuntimeError("RUNTIME-G1 managed FUSE helper manifest is stale or not NewFuture-bound")
    return data


def main() -> None:
    parser = argparse.ArgumentParser(description="RUNTIME-G1 real rooted-Android runtime authority qualification")
    sub = parser.add_subparsers(dest="command", required=True)
    capture_p = sub.add_parser("capture", help="execute the isolated real-device import→qualify→activate→mount→rollback gate")
    capture_p.add_argument("--output", type=Path, default=DEFAULT_EVIDENCE)
    validate_p = sub.add_parser("validate", help="validate existing RUNTIME-G1 device evidence and all referenced physical files")
    validate_p.add_argument("--evidence", type=Path, default=DEFAULT_EVIDENCE)
    validate_p.add_argument("--no-resolve-files", action="store_true")
    args = parser.parse_args()
    try:
        if args.command == "capture":
            data = capture(args.output.expanduser().resolve())
            print(json.dumps({"status": "PASS", "evidence": str(args.output), "evidence_digest": data["evidence_digest"]}, sort_keys=True))
        else:
            data = validate(args.evidence.expanduser().resolve(), resolve_files=not args.no_resolve_files)
            print(json.dumps({"status": "PASS", "evidence": str(args.evidence), "evidence_digest": data["evidence_digest"]}, sort_keys=True))
    except Exception as exc:
        print(f"RUNTIME-G1 device qualification: FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)


if __name__ == "__main__":
    main()
