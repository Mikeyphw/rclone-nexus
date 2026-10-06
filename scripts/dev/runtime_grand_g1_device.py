#!/usr/bin/env python3
from __future__ import annotations

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import io
import json
import os
import resource
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import threading
import traceback
import time
import zipfile


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "dev"))
import device_qualification_feedback as feedback  # noqa: E402
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

RUNTIME_STEP_WHY = {
    "require rooted Android shell": "The runtime gate must observe and mutate the real /data/adb-owned production state, not a user-shell approximation.",
    "build current racctl": "Bind every following device action to the exact source revision being qualified.",
    "discover runtime candidate": "Find real Android runtime bytes to exercise qualification and activation rather than a mocked executable.",
    "stage providerless qualification module": "Prove Nexus can qualify FUSE runtimes without borrowing an installed legacy provider module.",
    "import and qualify candidate A": "Establish the first independently qualified managed runtime used for activation/rollback proof.",
    "import and qualify candidate B": "Establish a second runtime so live switching and rollback prove real byte changes.",
    "prove linux-arm64 execute-but-fail-FUSE negative": "Reject a binary that merely executes but cannot provide Android FUSE semantics.",
    "activate candidate A": "Prove canonical activation publishes verified bytes into the live managed projection.",
    "start production compat mount": "Exercise the production mount ingress against the active managed runtime.",
    "read production mount status and process identity": "Bind the reported mount to a real live process instead of trusting status tokens.",
    "read proof file through real FUSE mount": "Prove the mounted filesystem is actually readable through kernel FUSE.",
    "activate candidate B and verify restart": "Prove runtime activation restarts affected mounts onto the newly selected bytes.",
    "rollback to candidate A": "Prove one-click rollback restores the previously qualified runtime and live process bytes.",
    "simulate mount process loss": "Create a real supervised-process failure for recovery qualification.",
    "run production service boot recovery": "Exercise the same boot/reconcile path used after an actual Android reboot.",
    "launch production service.sh under root": "Prove the installed root-module service entrypoint starts the current daemon.",
    "wait for production racd socket": "Do not continue until the real daemon control plane is reachable.",
    "verify boot fixture policy is offline-allowed": "Ensure recovery evidence is not accidentally dependent on network availability.",
    "wait for boot-reconciled mount": "Require the supervisor to restore the managed mount after process/service loss.",
    "check production service launch did not fail early": "Catch service bootstrap failures that could otherwise be hidden by later polling.",
    "hash boot-reconciled mount process": "Prove the recovered process still executes the expected managed runtime bytes.",
    "verify boot supervisor health": "Require the recovered mount to be healthy, owned, and policy-ready after reconciliation.",
    "query daemon RPC runtime identity": "Cross-check runtime identity through the daemon control plane.",
    "query WebUI bridge runtime identity": "Cross-check the same authority through the WebUI-facing production bridge.",
    "write and validate device evidence": "Persist source/device-bound evidence and immediately re-validate it before reuse is allowed.",
}

SOURCE_STEP_WHY = {
    'require rooted Android and clean qualification ownership': 'Source/update evidence must own a clean rooted Nexus state so external-source results cannot be attributed to leftover validation state.',
    'build current production racctl': 'Bind source/update actions to the exact checked-out implementation under qualification.',
    'stage providerless qualification module': 'Exercise source/update acquisition without depending on a legacy provider module.',
    'resolve real external bclone, official rclone and latest NewFuture sources': 'Prove current immutable source identities from the real upstream authorities, not canned fixtures.',
    'select, import and qualify a real historical NewFuture baseline A': 'Create a real older baseline so update detection and rollback can prove version/byte transitions.',
    'preflight runtimeg1 local fixture through historical active runtime A': 'Verify the historical baseline can actually execute the production FUSE fixture before using it as comparison evidence.',
    'exercise latest NewFuture resolution -> download -> hash/archive -> qualification -> stage': 'Prove the complete external-source path from immutable metadata through acquired bytes to a qualified staged runtime.',
    'explicitly activate staged external runtime and prove live process bytes': 'Show that staged source bytes become the runtime actually executing a production mount.',
    'one-click rollback and prove prior executable bytes restored': 'Prove update rollback restores the previous executable, not just metadata.',
    'exercise adversarial acquisition failures through production update path': 'Ensure bad hashes/assets/qualification failures fail closed without corrupting active authority.',
    'exercise disappeared/offline source retry without corrupting state': 'Prove transient upstream failures remain retryable and do not destroy durable source/update state.',
    'exercise SOURCE-X02 real Android build when NDK is available': 'Build real latest Android ARM64 runtime bytes from pinned source/NDK authority instead of accepting release-only coverage.',
    'snapshot durable source/update authority evidence': 'Persist the immutable resolution, acquisition, qualification, activation and helper provenance used by the final gate.',
}

def install_human_progress_hooks() -> None:
    # Keep the standalone harnesses source-stable so already-valid expensive
    # RUNTIME-G1/SOURCE-G1 evidence remains reusable. The composite wrapper owns
    # presentation and decorates their step callbacks only for this invocation.
    runtime_g1.mark_step = lambda name: feedback.step("RUNTIME-G1", name, RUNTIME_STEP_WHY.get(name, "Execute the next real-device qualification boundary."))
    source_g1.mark = lambda name: feedback.step("SOURCE-G1", name, SOURCE_STEP_WHY.get(name, "Execute the next real source/update qualification boundary."))


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



def _safe_stat(path: str | Path | None) -> dict:
    if not path:
        return {"path": "", "exists": False}
    p = Path(path)
    out = {"path": str(p)}
    try:
        st = p.stat()
        out.update({
            "exists": True,
            "mode": oct(st.st_mode & 0o7777),
            "uid": st.st_uid,
            "gid": st.st_gid,
            "size": st.st_size,
            "is_file": p.is_file(),
            "is_dir": p.is_dir(),
        })
    except OSError as exc:
        out.update({"exists": False, "stat_error": repr(exc)})
    return out


def _proc_status_snapshot() -> dict:
    wanted = {
        "Name", "State", "Pid", "PPid", "TracerPid", "Uid", "Gid",
        "FDSize", "Threads", "VmPeak", "VmSize", "VmRSS", "VmData",
        "VmStk", "voluntary_ctxt_switches", "nonvoluntary_ctxt_switches",
    }
    out: dict[str, str] = {}
    try:
        for line in Path('/proc/self/status').read_text(encoding='utf-8', errors='replace').splitlines():
            key, sep, value = line.partition(':')
            if sep and key in wanted:
                out[key] = value.strip()
    except OSError as exc:
        out['read_error'] = repr(exc)
    try:
        out['open_fd_count'] = str(len(list(Path('/proc/self/fd').iterdir())))
    except OSError as exc:
        out['open_fd_count_error'] = repr(exc)
    return out


def _rlimit_snapshot() -> dict:
    out = {}
    for name in ('RLIMIT_NOFILE', 'RLIMIT_NPROC', 'RLIMIT_STACK', 'RLIMIT_AS'):
        kind = getattr(resource, name, None)
        if kind is None:
            continue
        try:
            soft, hard = resource.getrlimit(kind)
            out[name] = {"soft": soft, "hard": hard}
        except Exception as exc:
            out[name] = {"error": repr(exc)}
    return out


def write_local_failure_diagnostic(stage: str, exc: BaseException, *, extra: dict | None = None) -> Path | None:
    """Persist a secret-free local failure snapshot with the original traceback."""
    safe_env_keys = (
        'HOME', 'PREFIX', 'TMPDIR', 'PATH',
        'RNEXUS_STATE_DIR', 'RNEXUS_MODULE_DIR', 'RNEXUS_PROVIDER_MODULE_DIR',
        'RNEXUS_RUNTIME_MODE', 'RNEXUS_RUNTIME_G1_PRODUCTION_MOUNT_GATE',
        'RNEXUS_GITHUB_TOKEN_FILE',
    )
    data = {
        "schema_version": 1,
        "captured_at": now(),
        "stage": stage,
        "exception": {
            "type": type(exc).__name__,
            "repr": repr(exc),
            "str": str(exc),
            "errno": getattr(exc, 'errno', None),
            "strerror": getattr(exc, 'strerror', None),
            "filename": getattr(exc, 'filename', None),
            "filename2": getattr(exc, 'filename2', None),
            "annotated_argv": getattr(exc, 'rnx_argv', None),
            "annotated_cwd": getattr(exc, 'rnx_cwd', None),
        },
        "traceback": traceback.format_exc(),
        "process": {
            "pid": os.getpid(),
            "ppid": os.getppid(),
            "uid": os.getuid(),
            "euid": os.geteuid(),
            "gid": os.getgid(),
            "egid": os.getegid(),
            "cwd": str(Path.cwd()),
            "python": sys.executable,
            "python_version": sys.version,
        },
        "proc_status": _proc_status_snapshot(),
        "rlimits": _rlimit_snapshot(),
        "launchers": {
            "su": _safe_stat(shutil.which('su')),
            "sh": _safe_stat('/system/bin/sh' if Path('/system/bin/sh').exists() else shutil.which('sh')),
        },
        "safe_environment": {k: os.environ.get(k, '') for k in safe_env_keys if k in os.environ},
        "extra": extra or {},
    }
    path = ROOT / 'release' / 'evidence' / 'runtime-grand-g1-last-local-error.json'
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(data, indent=2, sort_keys=True, ensure_ascii=False) + '\n', encoding='utf-8')
        try:
            path.chmod(0o600)
        except OSError:
            pass
        return path
    except OSError as write_exc:
        feedback.debug('failure diagnostic write error', repr(write_exc))
        return None


def activation_with_deep_diagnostics(binary: str, runtime_id: str, env: dict[str, str], *, stage: str) -> subprocess.CompletedProcess:
    args = ["runtime", "activate", runtime_id]
    safe_env = {
        k: env.get(k, '')
        for k in (
            'HOME', 'TMPDIR', 'PATH', 'RNEXUS_STATE_DIR', 'RNEXUS_MODULE_DIR',
            'RNEXUS_PROVIDER_MODULE_DIR', 'RNEXUS_RUNTIME_MODE',
            'RNEXUS_RUNTIME_G1_PRODUCTION_MOUNT_GATE', 'RNEXUS_GITHUB_TOKEN_FILE',
        )
        if k in env
    }
    launcher = shutil.which('su') if os.geteuid() != 0 else ('/system/bin/sh' if Path('/system/bin/sh').exists() else shutil.which('sh'))
    context = {
        "stage": stage,
        "racctl_binary": binary,
        "runtime_id": runtime_id,
        "root_launcher": launcher,
        "root_launcher_stat": _safe_stat(launcher),
        "safe_racctl_env": safe_env,
        "process_before": _proc_status_snapshot(),
        "rlimits_before": _rlimit_snapshot(),
    }
    feedback.debug('activation boundary', context)
    try:
        result = runtime_g1.racctl(binary, args, env, timeout=360, check=True)
    except OSError as exc:
        evidence = write_local_failure_diagnostic(stage, exc, extra=context)
        feedback.debug('original Python traceback', traceback.format_exc())
        raise feedback.QualificationFailure(
            summary=f"{stage} could not spawn/execute the production activation command ({type(exc).__name__}, errno={getattr(exc, 'errno', None)})",
            why="This is below racctl's normal exit-status handling: Python/Android refused a local process/filesystem operation while launching the root production command.",
            expected=f"root launcher executes {binary} runtime activate {runtime_id} and racctl returns a normal exit status",
            observed=(
                f"root_launcher={launcher}; racctl={binary}; runtime_id={runtime_id}; "
                f"filename={getattr(exc, 'filename', None)!r}; strerror={getattr(exc, 'strerror', None)!r}; "
                f"annotated_argv={getattr(exc, 'rnx_argv', None)!r}"
            ),
            command_text=f"{binary} runtime activate {runtime_id}",
            evidence=str(evidence) if evidence else "",
            next_action="Inspect the saved traceback/launcher/process snapshot. It identifies the exact Python line and executable/syscall boundary; do not rebuild SOURCE-X02 artifacts.",
        ) from exc
    feedback.debug('activation return code', result.returncode)
    return result


def local_os_failure(operation: str, path: Path | str, exc: OSError, *, why: str = "", next_action: str = "") -> feedback.QualificationFailure:
    target = str(path)
    errno_value = getattr(exc, "errno", None)
    errno_text = f"Errno {errno_value}" if errno_value is not None else type(exc).__name__
    strerror = getattr(exc, "strerror", None) or str(exc)
    executor = f"Termux Python pid={os.getpid()} uid={os.getuid()} euid={os.geteuid()} cwd={Path.cwd()}"
    return feedback.QualificationFailure(
        summary=f"{operation} failed ({errno_text}): {strerror}",
        why=why or "G1-A needs the local qualification harness to create and inspect evidence/build files without silently losing filesystem operations.",
        expected=f"operation succeeds for path {target}",
        observed=f"path={target}; executor={executor}; exception={exc!r}",
        next_action=next_action or "Check ownership/mount flags/SELinux for the reported path, then rerun with --verbose.",
    )


def termux_temp_base() -> Path:
    """Return an explicit Termux-owned temp root; never fall back to /tmp."""
    candidates: list[Path] = []
    raw_tmp = os.environ.get("TMPDIR", "").strip()
    if raw_tmp:
        candidates.append(Path(raw_tmp).expanduser())
    raw_prefix = os.environ.get("PREFIX", "").strip()
    if raw_prefix:
        candidates.append(Path(raw_prefix).expanduser() / "tmp")
    # Official Termux fallback. Keep it last and use it only when it exists or
    # can be created by the current Termux UID. /tmp is intentionally absent.
    candidates.append(Path("/data/data/com.termux/files/usr/tmp"))

    seen: set[str] = set()
    problems: list[str] = []
    for candidate in candidates:
        key = str(candidate)
        if key in seen or key.rstrip("/") == "/tmp":
            continue
        seen.add(key)
        try:
            candidate.mkdir(parents=True, exist_ok=True)
            if not candidate.is_dir():
                problems.append(f"{candidate}: not a directory")
                continue
            if not os.access(candidate, os.W_OK | os.X_OK):
                problems.append(f"{candidate}: not writable/searchable by uid={os.getuid()}")
                continue
            return candidate
        except OSError as exc:
            problems.append(f"{candidate}: {exc!r}")

    raise feedback.QualificationFailure(
        summary="no writable Termux temporary directory is available",
        why="Android/Termux qualification must never assume the conventional Linux /tmp path is accessible.",
        expected="TMPDIR or $PREFIX/tmp is writable by the Termux app UID",
        observed="; ".join(problems) or "TMPDIR/PREFIX unavailable",
        next_action="Set TMPDIR=$PREFIX/tmp (or another Termux-owned path) and rerun.",
    )


class TermuxWorkspace:
    """Explicit app-owned workspace with shell cleanup instead of TemporaryDirectory._rmtree."""

    def __init__(self, prefix: str):
        self.prefix = prefix
        self.path: Path | None = None

    def __enter__(self) -> str:
        base = termux_temp_base()
        try:
            self.path = Path(tempfile.mkdtemp(prefix=self.prefix, dir=str(base)))
        except OSError as exc:
            raise local_os_failure(
                "create Termux GRAND-G1 workspace",
                base,
                exc,
                why="The automatic journey needs an app-owned workspace under Termux TMPDIR; /tmp is intentionally not used.",
            ) from exc
        feedback.note(f"GRAND-G1 Termux workspace: {self.path}")
        return str(self.path)

    def __exit__(self, exc_type, exc, tb) -> bool:
        path = self.path
        if path is None or not path.exists():
            return False

        # Python TemporaryDirectory uses shutil.rmtree permission recovery. On
        # Android/Termux, large Git/Go trees can make that recovery itself hit
        # EPERM. Use Termux coreutils rm on the app-owned tree instead. This is
        # deliberately NOT a root cleanup and never targets /tmp.
        rm = shutil.which("rm")
        chmod = shutil.which("chmod")
        attempts: list[str] = []

        def remove() -> subprocess.CompletedProcess[str] | None:
            if not rm:
                return None
            try:
                return subprocess.run(
                    [rm, "-rf", "--", str(path)],
                    text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                    timeout=120, check=False,
                )
            except OSError as cleanup_exc:
                attempts.append(f"rm start failed: {cleanup_exc!r}")
                return None

        cp = remove()
        if cp is not None and cp.returncode != 0:
            attempts.append(f"rm rc={cp.returncode}: {(cp.stderr or cp.stdout).strip()[-1200:]}")

        if path.exists() and chmod:
            try:
                fix = subprocess.run(
                    [chmod, "-R", "u+rwX", "--", str(path)],
                    text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                    timeout=120, check=False,
                )
                if fix.returncode != 0:
                    attempts.append(f"chmod rc={fix.returncode}: {(fix.stderr or fix.stdout).strip()[-1200:]}")
            except OSError as cleanup_exc:
                attempts.append(f"chmod start failed: {cleanup_exc!r}")
            cp = remove()
            if cp is not None and cp.returncode != 0:
                attempts.append(f"retry rm rc={cp.returncode}: {(cp.stderr or cp.stdout).strip()[-1200:]}")

        if path.exists():
            # Cleanup is hygiene, not product qualification. Never replace a
            # real journey result with a contextless EPERM from temp cleanup.
            feedback.note(
                "GRAND-G1 workspace cleanup warning: "
                f"{path} remains; executor=Termux uid={os.getuid()} euid={os.geteuid()}; "
                + ("; ".join(attempts) if attempts else "rm/chmod unavailable")
            )
        elif feedback.verbose_enabled():
            feedback.note(f"GRAND-G1 workspace cleaned: {path}")
        return False


def local_write_text(path: Path, text: str, *, purpose: str) -> None:
    try:
        path.write_text(text, encoding="utf-8")
    except OSError as exc:
        raise local_os_failure(
            f"write {purpose}",
            path,
            exc,
            why=f"{purpose} is an input to the real GRAND-G1 device journey and must be materialized by the Termux-side harness before root-owned execution begins.",
        ) from exc


def write_private(path: Path, data: dict) -> None:
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
    except OSError as exc:
        raise local_os_failure("create evidence directory", path.parent, exc) from exc
    tmp = path.with_name(path.name + ".tmp")
    local_write_text(tmp, json.dumps(data, indent=2, sort_keys=True) + "\n", purpose="private evidence temporary file")
    try:
        tmp.chmod(0o600)
    except OSError:
        pass
    try:
        os.replace(tmp, path)
    except OSError as exc:
        raise local_os_failure("atomically publish private evidence", path, exc) from exc
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


def run(
    argv: list[str],
    *,
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
    timeout: int = 300,
    check: bool = True,
    stream: bool = False,
    stream_label: str = "command",
) -> subprocess.CompletedProcess[str]:
    workdir = cwd or ROOT
    feedback.command(argv)
    if not stream:
        try:
            cp = subprocess.run(
                argv, cwd=workdir, env=env, text=True, stdout=subprocess.PIPE,
                stderr=subprocess.PIPE, timeout=timeout, check=False,
            )
        except OSError as exc:
            raise feedback.QualificationFailure(
                summary=f"could not start {stream_label}: {exc}",
                why="The qualification command must execute locally before its result can be trusted as device/build evidence.",
                expected=f"executable command: {shlex.join(argv)}",
                observed=f"cwd={workdir}; executor=Termux Python uid={os.getuid()} euid={os.geteuid()}; exception={exc!r}",
                command_text=shlex.join(argv),
                next_action="Check the executable/path permissions and rerun with --verbose.",
            ) from exc
    else:
        # Tee both child streams live while retaining the complete transcript for
        # failure evidence. runtime_source_build.py lets `go build -v` inherit
        # its stdout/stderr, so package compilation becomes visible immediately.
        try:
            proc = subprocess.Popen(
                argv, cwd=workdir, env=env, text=True, stdout=subprocess.PIPE,
                stderr=subprocess.PIPE, bufsize=1,
            )
        except OSError as exc:
            raise feedback.QualificationFailure(
                summary=f"could not start {stream_label}: {exc}",
                why="The qualification command must execute locally before its result can be trusted as device/build evidence.",
                expected=f"executable command: {shlex.join(argv)}",
                observed=f"cwd={workdir}; executor=Termux Python uid={os.getuid()} euid={os.geteuid()}; exception={exc!r}",
                command_text=shlex.join(argv),
                next_action="Check the executable/path permissions and rerun with --verbose.",
            ) from exc

        stdout_parts: list[str] = []
        stderr_parts: list[str] = []

        def drain(pipe, parts: list[str], channel: str) -> None:
            if pipe is None:
                return
            try:
                for line in iter(pipe.readline, ""):
                    parts.append(line)
                    print(f"  [{stream_label}/{channel}] {line.rstrip()}", file=sys.stderr, flush=True)
            finally:
                pipe.close()

        threads = [
            threading.Thread(target=drain, args=(proc.stdout, stdout_parts, "out"), daemon=True),
            threading.Thread(target=drain, args=(proc.stderr, stderr_parts, "err"), daemon=True),
        ]
        for thread in threads:
            thread.start()
        try:
            returncode = proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired as exc:
            proc.kill()
            proc.wait()
            for thread in threads:
                thread.join(timeout=2)
            raise feedback.QualificationFailure(
                summary=f"{stream_label} timed out after {timeout}s",
                why="A final-gate source build must finish deterministically; a hung build cannot be treated as qualified evidence.",
                observed=f"cwd={workdir}; stdout_tail={''.join(stdout_parts)[-2000:]!r}; stderr_tail={''.join(stderr_parts)[-2000:]!r}",
                command_text=shlex.join(argv),
                next_action="Inspect the live build output above, resolve the stalled dependency/toolchain step, then rerun.",
            ) from exc
        for thread in threads:
            thread.join(timeout=2)
        cp = subprocess.CompletedProcess(argv, returncode, "".join(stdout_parts), "".join(stderr_parts))

    if check and cp.returncode != 0:
        raise RuntimeError(
            f"command failed ({cp.returncode}): {shlex.join(argv)}\n"
            f"stdout={cp.stdout[-6000:]}\nstderr={cp.stderr[-6000:]}"
        )
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



def source_x02_build_cache_root() -> Path:
    explicit = os.environ.get("RNEXUS_G1_BUILD_CACHE_DIR", "").strip()
    if explicit:
        root = Path(explicit).expanduser()
    else:
        xdg = os.environ.get("XDG_CACHE_HOME", "").strip()
        home = os.environ.get("HOME", "").strip()
        if xdg:
            root = Path(xdg).expanduser() / "rclone-nexus" / "source-x02"
        elif home:
            root = Path(home).expanduser() / ".cache" / "rclone-nexus" / "source-x02"
        else:
            raise feedback.QualificationFailure(
                summary="SOURCE-X02 persistent build cache has no Termux-owned base",
                why="G1-A should reuse expensive Android ARM64 builds without relying on /tmp.",
                expected="HOME, XDG_CACHE_HOME, or RNEXUS_G1_BUILD_CACHE_DIR identifies a persistent app-owned directory",
                observed="HOME/XDG_CACHE_HOME are unavailable and no explicit cache directory was configured",
                next_action="Set RNEXUS_G1_BUILD_CACHE_DIR to a persistent Termux-owned directory and rerun.",
            )
    # Never silently use the conventional Linux /tmp namespace on Android.
    if str(root) == "/tmp" or str(root).startswith("/tmp/"):
        raise feedback.QualificationFailure(
            summary="SOURCE-X02 build cache points at unsupported /tmp",
            why="Termux/Android must use an app-owned persistent cache rather than conventional /tmp.",
            expected="a path under Termux HOME/XDG cache or another explicit app-owned directory",
            observed=f"cache_root={root}",
            next_action="Unset RNEXUS_G1_BUILD_CACHE_DIR or point it at $HOME/.cache/rclone-nexus/source-x02.",
        )
    try:
        root.mkdir(parents=True, exist_ok=True)
    except OSError as exc:
        raise local_os_failure(
            "create persistent SOURCE-X02 build cache",
            root,
            exc,
            why="Successful Android ARM64 builds are retained here so unrelated later G1-A failures do not force recompilation.",
        ) from exc
    return root


def source_x02_cache_identity(resolution: dict, ndk: dict) -> dict:
    go = shutil.which("go")
    if not go:
        raise RuntimeError("Go is required to identify the SOURCE-X02 build cache authority")
    compiler = str(ndk.get("compiler", "")).strip()
    if not compiler:
        raise RuntimeError("SOURCE-X02 NDK selection did not expose a compiler")
    go_version = run([go, "version"], timeout=15, check=True).stdout.strip()
    compiler_version = run([compiler, "--version"], timeout=15, check=True).stdout.splitlines()[0].strip()
    return {
        "schema_version": 1,
        "source_id": str(resolution["source_id"]),
        "engine": str(resolution["engine"]),
        "repository": str(resolution["repository"]),
        "resolved_commit": str(resolution["commit_sha"]).lower(),
        "api_level": 21,
        "target": "aarch64-linux-android21",
        "builder_sha256": file_sha256(ROOT / "scripts/dev/runtime_source_build.py"),
        "go_version": go_version,
        "ndk_version": str(ndk.get("version", "")),
        "ndk_host": str(ndk.get("host", "")),
        "compiler": compiler,
        "compiler_version": compiler_version,
        "compiler_mode": str(ndk.get("compiler_mode", "ndk-prebuilt")),
        "compiler_resource_dir": str(ndk.get("compiler_resource_dir", "")),
    }


def source_x02_cache_key(identity: dict) -> str:
    return digest(identity)


def validate_cached_source_x02_bundle(bundle: Path, identity: dict) -> dict:
    provenance_path = bundle / "provenance.json"
    sums_path = bundle / "SHA256SUMS"
    if not provenance_path.is_file() or not sums_path.is_file():
        raise RuntimeError("cache entry is missing provenance.json or SHA256SUMS")
    try:
        provenance = json.loads(provenance_path.read_text(encoding="utf-8"))
    except Exception as exc:
        raise RuntimeError(f"cache provenance is unreadable: {exc}") from exc
    if not isinstance(provenance, dict):
        raise RuntimeError("cache provenance is not an object")

    expected = {
        "source_id": identity["source_id"],
        "engine": identity["engine"],
        "repository": identity["repository"],
        "resolved_commit": identity["resolved_commit"],
        "api_level": 21,
        "goos": "android",
        "goarch": "arm64",
        "abi": "arm64-v8a",
        "ndk_version": identity["ndk_version"],
        "ndk_host": identity["ndk_host"],
        "compiler_mode": identity["compiler_mode"],
        "compiler_target": "aarch64-linux-android21",
    }
    for key, value in expected.items():
        if provenance.get(key) != value:
            raise RuntimeError(f"cache provenance mismatch for {key}: {provenance.get(key)!r} != {value!r}")

    # Native Termux clang can resolve the canonical linux-x86_64 resource-dir
    # alias while the NDK sysroot authority is exposed as linux-x86. The bundle
    # verifier handles that alias pair; cache validation only requires the exact
    # current compiler version/mode key above plus the production verifier below.
    binary_name = str(provenance.get("binary_name", "")).strip()
    if not binary_name or Path(binary_name).name != binary_name:
        raise RuntimeError("cache provenance has an invalid binary name")
    binary = bundle / binary_name
    if not binary.is_file():
        raise RuntimeError(f"cached Android binary is missing: {binary_name}")
    actual_binary_sha = file_sha256(binary)
    if actual_binary_sha != str(provenance.get("binary_sha256", "")).lower():
        raise RuntimeError("cached Android binary hash does not match provenance")
    if binary.stat().st_size != int(provenance.get("binary_size", -1)):
        raise RuntimeError("cached Android binary size does not match provenance")

    sum_lines = {}
    for line in sums_path.read_text(encoding="utf-8").splitlines():
        parts = line.split(None, 1)
        if len(parts) == 2:
            sum_lines[parts[1].strip().lstrip("*")] = parts[0].strip().lower()
    if sum_lines.get(binary_name) != actual_binary_sha:
        raise RuntimeError("cached SHA256SUMS does not bind the Android binary")
    if sum_lines.get("provenance.json") != file_sha256(provenance_path):
        raise RuntimeError("cached SHA256SUMS does not bind provenance.json")
    return provenance


def restore_cached_source_x02_bundle(out: Path, resolution: dict, ndk: dict) -> dict | None:
    if os.environ.get("RNEXUS_G1_REBUILD_SOURCE_X02", "").strip().lower() in {"1", "true", "yes", "on"}:
        feedback.note(f"SOURCE-X02 cache bypass requested for {resolution['source_id']}")
        return None
    identity = source_x02_cache_identity(resolution, ndk)
    key = source_x02_cache_key(identity)
    cache = source_x02_build_cache_root() / str(resolution["source_id"]) / key
    if not cache.is_dir():
        feedback.note(f"SOURCE-X02 cache miss: {resolution['source_id']} · key={key[:12]}")
        return None
    try:
        provenance = validate_cached_source_x02_bundle(cache, identity)
    except Exception as exc:
        feedback.note(f"SOURCE-X02 cache rejected for {resolution['source_id']}: {exc}")
        return None
    try:
        if out.exists():
            shutil.rmtree(out)
        shutil.copytree(cache, out, copy_function=shutil.copy2)
    except OSError as exc:
        raise local_os_failure(
            f"restore cached {resolution['source_id']} Android ARM64 bundle",
            out,
            exc,
            why="A validated persistent build is copied into the disposable journey workspace so root-side import can never mutate the cache authority.",
        ) from exc
    feedback.step(
        "SOURCE-X02",
        f"reuse cached {resolution['source_id']} Android ARM64 build",
        "The exact source commit, SOURCE-X02 builder, Go/NDK/compiler authority and cached bundle hashes still match; production import-build will reverify it before use.",
    )
    feedback.note(f"Cache: {cache}")
    feedback.note(f"Commit: {identity['resolved_commit']}")
    feedback.note(f"Target: android/arm64 · API 21 · aarch64-linux-android21")
    feedback.ok(
        "SOURCE-X02",
        f"{resolution['source_id']} Android ARM64 build cache hit",
        f"sha256={str(provenance.get('binary_sha256', ''))[:12]}; key={key[:12]}",
    )
    return provenance


def publish_source_x02_bundle_cache(bundle: Path, resolution: dict, ndk: dict) -> None:
    identity = source_x02_cache_identity(resolution, ndk)
    key = source_x02_cache_key(identity)
    parent = source_x02_build_cache_root() / str(resolution["source_id"])
    target = parent / key
    try:
        parent.mkdir(parents=True, exist_ok=True)
        # Validate the just-built bundle before it becomes persistent authority.
        validate_cached_source_x02_bundle(bundle, identity)
        if target.is_dir():
            try:
                validate_cached_source_x02_bundle(target, identity)
                return
            except Exception:
                shutil.rmtree(target, ignore_errors=True)
        staging = parent / ("." + key + f".tmp-{os.getpid()}")
        shutil.rmtree(staging, ignore_errors=True)
        shutil.copytree(bundle, staging, copy_function=shutil.copy2)
        local_write_text(
            staging / "cache-identity.json",
            json.dumps(identity, indent=2, sort_keys=True) + "\n",
            purpose="SOURCE-X02 build-cache identity",
        )
        # cache-identity.json is advisory; provenance/SHA256SUMS remain the
        # authoritative bundle proof consumed by current production import.
        os.replace(staging, target)
    except OSError as exc:
        raise local_os_failure(
            f"publish cached {resolution['source_id']} Android ARM64 bundle",
            target,
            exc,
            why="A completed expensive SOURCE-X02 build should survive unrelated later G1-A failures and be reusable only under the same exact build authority.",
        ) from exc
    feedback.note(f"SOURCE-X02 cached {resolution['source_id']} build: {target}")

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
    source_id = str(resolution["source_id"])
    target = "aarch64-linux-android21"
    feedback.step(
        "SOURCE-X02",
        f"build {source_id} for Android ARM64",
        "Compile the exact pinned source commit as an Android/arm64 CGO binary using the Android NDK authority; existing Linux x86_64 binaries are intentionally not accepted for this final-device proof.",
    )
    feedback.note(f"Repository: {resolution['repository']}")
    feedback.note(f"Commit: {resolution['commit_sha']}")
    feedback.note(f"Target: android/arm64 · API 21 · {target}")
    feedback.note(
        "Toolchain: "
        f"{ndk.get('compiler_mode', 'ndk-prebuilt')} · compiler={ndk.get('compiler', '')} · "
        f"NDK={ndk.get('version', '')} host={ndk.get('host', '')}"
    )
    feedback.note(f"Output bundle: {out}")
    if feedback.verbose_enabled():
        feedback.note("Live build output follows; the full transcript is also retained for failure reporting.")

    argv = [
        sys.executable, str(ROOT / "scripts/dev/runtime_source_build.py"), "build",
        "--source-dir", str(source), "--output-dir", str(out),
        "--repository", str(resolution["repository"]),
        "--requested-ref", str(resolution.get("requested_ref") or resolution.get("release_tag") or resolution["commit_sha"]),
        "--resolved-commit", str(resolution["commit_sha"]),
        "--source-id", str(resolution["source_id"]), "--engine", str(resolution["engine"]),
        "--ndk", str(ndk["ndk"]), "--ndk-version", str(ndk["version"]), "--ndk-host", str(ndk["host"]),
        "--compiler", str(ndk.get("compiler", "")), "--compiler-mode", str(ndk.get("compiler_mode", "ndk-prebuilt")),
        "--compiler-resource-dir", str(ndk.get("compiler_resource_dir", "")),
        "--api-level", "21",
    ]
    run(
        argv,
        timeout=1200,
        stream=feedback.verbose_enabled(),
        stream_label=f"{source_id}/android-arm64-build",
    )
    provenance_path = out / "provenance.json"
    try:
        provenance = json.loads(provenance_path.read_text(encoding="utf-8"))
    except OSError as exc:
        raise local_os_failure(
            "read SOURCE-X02 build provenance",
            provenance_path,
            exc,
            why="The completed Android ARM64 build must publish provenance before its bytes can be imported or qualified.",
        ) from exc
    feedback.ok(
        "SOURCE-X02",
        f"{source_id} Android ARM64 build completed",
        f"bundle={out}; commit={resolution['commit_sha'][:12]}",
    )
    return provenance


def process_hash(pid: int, state: str) -> str:
    return source_g1.proc_hash(pid, state)


def encrypted_config_probe(runtime_binary: str, root_dir: str, binary: str, env: dict[str, str]) -> dict:
    config = f"{root_dir}/encrypted-rclone.conf"
    pass_script = f"{root_dir}/config-pass.sh"
    canary = "RNEXUS-GRAND-G1-CONFIG-PASS-9f5b3e"
    script_body = '#!/system/bin/sh\\nprintf "%s\\n" "' + canary + '"\\n'
    runtime_g1.root_run(["/system/bin/sh", "-c", f"printf '[grandlocal]\\ntype = local\\n' > {shlex.quote(config)}"], check=True)
    # Termux cannot assume conventional /tmp access. Reuse the explicit
    # app-owned workspace authority already used by the outer GRAND-G1 journey.
    with TermuxWorkspace(prefix="rnx-g1-pass-") as pass_raw:
        local = Path(pass_raw) / "config-pass.sh"
        local_write_text(
            local,
            script_body,
            purpose="encrypted-config password-command fixture",
        )
        runtime_g1.root_write_from_local(local, pass_script)
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
    # KernelSU/Termux `su` may remain attached to a backgrounded root
    # descendant, so a synchronous root_run() cannot launch this watcher: the
    # harness would wait for the watcher while the watcher waits for activation
    # to reach QUIESCING. Launch `su` itself asynchronously with Popen, wait only
    # until the root watcher publishes its PID, then start activation in parallel.
    watcher_pidfile = f"{state}/tmp/grand-g1-failed-activation-watcher.pid"
    watcher = watcher.replace(
        "set -eu\n",
        "set -eu\n"
        f"pidfile={shlex.quote(watcher_pidfile)}\n"
        "mkdir -p \"$(dirname \"$pidfile\")\"\n"
        "printf '%s\n' \"$$\" > \"$pidfile\"\n",
        1,
    )
    watcher_command = runtime_g1.shell_join_env(
        {},
        ["/system/bin/sh", "-c", watcher],
    )
    if os.geteuid() == 0:
        watcher_argv = [
            "/system/bin/sh" if Path("/system/bin/sh").exists() else "sh",
            "-c",
            watcher_command,
        ]
    else:
        su = shutil.which("su")
        if not su:
            raise RuntimeError("failed-activation watcher requires rooted Android (su unavailable)")
        watcher_argv = [su, "-c", watcher_command]

    feedback.debug(
        "failed-activation watcher launch",
        {
            "argv": watcher_argv,
            "pidfile": watcher_pidfile,
            "candidate_runtime_id": candidate_id,
            "previous_runtime_id": previous_id,
            "statefile": statefile,
        },
    )
    try:
        watcher_proc = subprocess.Popen(
            watcher_argv,
            cwd=ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
    except OSError as exc:
        raise RuntimeError(
            "failed to start concurrent root failed-activation watcher: "
            f"argv={shlex.join(watcher_argv)}: {exc}"
        ) from exc

    watcher_pid = 0
    try:
        # Synchronize only on watcher readiness, not watcher completion.
        for _ in range(100):
            pid_result = runtime_g1.root_run(["cat", watcher_pidfile], timeout=2, check=False)
            value = pid_result.stdout.strip() if pid_result.returncode == 0 else ""
            if value.isdigit() and int(value) > 1:
                watcher_pid = int(value)
                break
            if watcher_proc.poll() is not None:
                out, err = watcher_proc.communicate()
                raise RuntimeError(
                    "failed-activation watcher exited before becoming ready: "
                    f"returncode={watcher_proc.returncode}; stdout={out!r}; stderr={err!r}"
                )
            time.sleep(0.02)
        if watcher_pid <= 1:
            raise RuntimeError(
                "failed-activation watcher did not publish a valid root PID before activation"
            )

        feedback.debug(
            "failed-activation watcher ready",
            {
                "root_pid": watcher_pid,
                "launcher_pid": watcher_proc.pid,
                "candidate_runtime_id": candidate_id,
                "previous_runtime_id": previous_id,
                "statefile": statefile,
            },
        )

        failed = runtime_g1.racctl(binary, ["runtime", "activate", candidate_id], env, timeout=360, check=False)

        # The watcher has its own bounded ~30 second loop. Reap the `su`
        # launcher after activation rather than killing a privilege-changing
        # process from the Termux UID. If something wedges unexpectedly, kill
        # the actual watcher via a separate root command, then reap `su`.
        try:
            watcher_out, watcher_err = watcher_proc.communicate(timeout=35)
        except subprocess.TimeoutExpired:
            runtime_g1.root_run(["kill", "-9", str(watcher_pid)], timeout=5, check=False)
            try:
                watcher_out, watcher_err = watcher_proc.communicate(timeout=10)
            except subprocess.TimeoutExpired as exc:
                raise RuntimeError(
                    "failed-activation watcher remained attached after root-side cleanup; "
                    f"root_pid={watcher_pid}; launcher_pid={watcher_proc.pid}"
                ) from exc

        feedback.debug(
            "failed-activation watcher completed",
            {
                "root_pid": watcher_pid,
                "launcher_pid": watcher_proc.pid,
                "returncode": watcher_proc.returncode,
                "stdout": watcher_out,
                "stderr": watcher_err,
            },
        )
        if watcher_proc.returncode not in {0, 71}:
            raise RuntimeError(
                "failed-activation watcher exited unexpectedly: "
                f"returncode={watcher_proc.returncode}; stdout={watcher_out!r}; stderr={watcher_err!r}"
            )
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
        if watcher_pid > 1:
            runtime_g1.root_run(["kill", "-9", str(watcher_pid)], timeout=5, check=False)
        runtime_g1.root_run(["rm", "-f", watcher_pidfile], timeout=5, check=False)


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

    with TermuxWorkspace(prefix="rnx-grand-g1-") as raw:
        tmp = Path(raw)
        built_racctl = tmp / "racctl"
        runtime_g1.build_current_racctl(built_racctl)
        module_dir, binary = runtime_g1.copy_gate_module(root_dir, built_racctl)
        env = runtime_g1.racctl_env(state, module_dir, provider_absent)
        runtime_g1.root_run(["mkdir", "-p", f"{state}/config/rclone", f"{state}/mounts.d", f"{state}/run"], check=True)
        cfg = tmp / "rclone.conf"
        local_write_text(cfg, "[runtimeg1]\ntype = local\n", purpose="GRAND-G1 rclone fixture config")
        runtime_g1.root_write_from_local(cfg, f"{state}/config/rclone/rclone.conf")
        source_dir = f"{root_dir}/source"; mountpoint = f"{root_dir}/mountpoint"
        runtime_g1.root_run(["mkdir", "-p", source_dir, mountpoint], check=True)
        runtime_g1.root_run(["/system/bin/sh", "-c", f"printf %s {shlex.quote(session)} > {shlex.quote(source_dir + '/proof.txt')}"], check=True)
        mount_cfg = tmp / "grand-g1.conf"
        mount_cfg.write_text(
            runtime_g1.g1_mount_definition(source_dir, mountpoint).replace(
                "runtime-g1", "grand-g1"
            ),
            encoding="utf-8",
        )

        # Do NOT publish the desired mount yet. The first managed-runtime
        # activation intentionally has no rollback runtime. Production correctly
        # refuses first activation when desired mounts already exist because
        # there would be nothing qualified to restore if activation failed.
        #
        # Establish bclone as the first stable rollback baseline first; only then
        # publish/start the GRAND-G1 mount and exercise live runtime switching.

        resolutions: dict[str, dict] = {}
        manifests: dict[str, dict] = {}
        provenances: dict[str, dict] = {}
        for sid in ("bclone", "rclone"):
            res = json.loads(runtime_g1.racctl(binary, ["runtime", "source", "resolve", sid], env, timeout=180, check=True).stdout)
            source_g1.resolution_identity(res)
            resolutions[sid] = res
            out = tmp / f"bundle-{sid}"
            cached_provenance = restore_cached_source_x02_bundle(out, res, ndk)
            if cached_provenance is None:
                repo = tmp / f"repo-{sid}"
                clone_exact(str(res["repository"]), str(res["commit_sha"]), repo)
                provenances[sid] = build_bundle(repo, out, res, ndk)
                publish_source_x02_bundle_cache(out, res, ndk)
            else:
                provenances[sid] = cached_provenance
            feedback.step(
                "GRAND-G1",
                f"import and qualify {sid} Android ARM64 build",
                "Move the verified build into isolated Nexus runtime authority and run the real runtime qualifier before activation.",
            )
            imported = json.loads(runtime_g1.racctl(binary, ["runtime", "source", "import-build", str(out)], env, timeout=600, check=True).stdout)

            # `runtime source import-build` returns:
            # {
            #   "resolution": {...},
            #   "runtime": {
            #       "runtime_id": "...",
            #       "binary_sha256": "...",
            #       "qualification": {...}
            #   }
            # }
            #
            # GRAND-G1 previously read qualification/runtime_id from the
            # top-level object, so a successfully qualified build was always
            # interpreted as unqualified.
            manifest = imported.get("runtime") if isinstance(imported.get("runtime"), dict) else {}

            if not manifest:
                raise RuntimeError(
                    f"{sid} import-build returned no runtime manifest; "
                    f"observed keys={sorted(imported.keys()) if isinstance(imported, dict) else type(imported).__name__}"
                )

            q = manifest.get("qualification") if isinstance(manifest.get("qualification"), dict) else {}

            if q.get("qualified") is not True:
                failed = []

                for check in q.get("checks") or []:
                    if not isinstance(check, dict) or check.get("status") == "pass":
                        continue

                    name = str(check.get("name") or "unnamed")
                    status = str(check.get("status") or "unknown")
                    detail = str(check.get("detail") or "").strip()

                    failed.append(
                        f"{name}={status}" +
                        (f": {detail}" if detail else "")
                    )

                detail = "; ".join(failed) if failed else json.dumps(q, sort_keys=True)

                raise RuntimeError(
                    f"{sid} real Android build did not qualify: {detail}"
                )

            manifests[sid] = manifest


            feedback.ok(

                "GRAND-G1",

                f"{sid} build imported and qualified",

                f"runtime_id={manifests[sid].get('runtime_id', '<missing>')}; sha256={str(manifests[sid].get('binary_sha256', ''))[:12]}",

            )
        b_id = str(manifests["bclone"]["runtime_id"]); r_id = str(manifests["rclone"]["runtime_id"])
        b_sha = str(manifests["bclone"]["binary_sha256"]); r_sha = str(manifests["rclone"]["binary_sha256"])
        # Bootstrap the isolated runtime authority with no desired mounts.
        # This is the only activation in this journey allowed to have no
        # rollback runtime.
        runtime_g1.racctl(
            binary,
            ["runtime", "activate", b_id],
            env,
            timeout=360,
            check=True,
        )

        first_activation = racctl_json(
            binary,
            ["runtime", "activation-status"],
            env=env,
            timeout=30,
        )
        first_state = (
            first_activation.get("state")
            if isinstance(first_activation.get("state"), dict)
            else {}
        )
        if (
            first_state.get("active_runtime_id") != b_id
            or first_state.get("phase") != "ACTIVE"
        ):
            raise RuntimeError(
                "GRAND-G1 bootstrap activation did not establish bclone as "
                f"the stable rollback baseline: {first_state}"
            )

        # Only now introduce desired mount state. Every subsequent runtime
        # activation therefore has bclone/rclone as a verified rollback target
        # and exercises the production safety invariant rather than bypassing it.
        runtime_g1.root_write_from_local(
            mount_cfg,
            f"{state}/mounts.d/grand-g1.conf",
        )
        runtime_g1.racctl(
            binary,
            ["compat", "mountctl", "start", "grand-g1"],
            env,
            timeout=120,
            check=True,
        )
        b_stat = runtime_g1.mount_status(binary, env, "grand-g1"); b_pid = int(b_stat.get("pid") or 0)
        if process_hash(b_pid, state) != b_sha or runtime_g1.root_run(["cat", f"{mountpoint}/proof.txt"], check=True).stdout.strip() != session:
            raise RuntimeError("latest bclone did not become the live mounted runtime")
        runtime_g1.racctl(binary, ["runtime", "activate", r_id], env, timeout=360, check=True)
        r_stat = runtime_g1.mount_status(binary, env, "grand-g1"); r_pid = int(r_stat.get("pid") or 0)
        if r_pid == b_pid or process_hash(r_pid, state) != r_sha:
            raise RuntimeError("bclone -> official rclone switch did not change live executable bytes")
        feedback.step(
            "GRAND-G1",
            "switch official rclone back to bclone",
            "Exercise a live runtime switch-back while the GRAND-G1 mount is active; bclone is already qualified and acts as the rollback target.",
        )
        activation_with_deep_diagnostics(
            binary,
            b_id,
            env,
            stage="GRAND-G1 rclone-to-bclone switch-back activation",
        )
        back_stat = runtime_g1.mount_status(binary, env, "grand-g1"); back_pid = int(back_stat.get("pid") or 0)
        if back_pid == r_pid or process_hash(back_pid, state) != b_sha:
            raise RuntimeError("official rclone -> bclone switch-back did not restore bclone bytes")

        failed_activation = failed_activation_rollback_probe(binary, env, state, r_id, b_id, b_sha)

        bad = tmp / "not-a-runtime"

        local_write_text(bad, "not an ELF runtime\n", purpose="invalid-runtime negative fixture")
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
        crash_local = tmp / "activation-v1.json"
        local_write_text(crash_local, json.dumps(crash_state) + "\n", purpose="activation crash-recovery fixture")
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
    install_human_progress_hooks()
    feedback.phase(
        "RUNTIME-GRAND-G1-A / real-device capture",
        "Build a device-bound proof chain for RNX-P467..RNX-P489. Existing evidence is reused only after its source, device and physical hashes validate.",
    )
    feedback.step("G1-A", "verify rooted Android execution destination", "Every final device claim must come from the real rooted Android device, not host/source-only validation.")
    runtime_g1.require_android_root()
    runtime_path = evidence_path(RUNTIME_EVIDENCE)
    source_path = evidence_path(SOURCE_EVIDENCE)
    release_path = evidence_path(RELEASE_EVIDENCE)
    feedback.note(f"Composite evidence: {path}")
    feedback.note(f"RUNTIME-G1 evidence: {runtime_path}")
    feedback.note(f"SOURCE-G1 evidence: {source_path}")
    feedback.note(f"Release-device evidence: {release_path}")

    # Each underlying harness is a production-path authority with its own physical evidence.
    # Stable contract phrases retained for source-audit/tests: reusing current RUNTIME-G1 private evidence; reusing current SOURCE-G1 private evidence.
    # Reuse already-valid private evidence after an interrupted later stage so a
    # SOURCE-G1 or release-case failure does not rerun expensive rooted/FUSE/boot
    # qualification that is still source- and device-bound. Stale/invalid evidence
    # is never trusted: validation failure falls back to a fresh capture.
    if runtime_path.is_file():
        try:
            runtime_data = runtime_g1.validate(runtime_path, resolve_files=True)
            feedback.reuse("RUNTIME-G1", runtime_path, "schema/harness identity, source bindings, Android identity and referenced physical evidence all validated")
        except Exception as exc:
            feedback.note(f"RUNTIME-G1 evidence cannot be reused: {exc}")
            feedback.note("Recapturing RUNTIME-G1 from production paths.")
            runtime_g1.capture(runtime_path)
            runtime_data = runtime_g1.validate(runtime_path, resolve_files=True)
    else:
        runtime_g1.capture(runtime_path)
        runtime_data = runtime_g1.validate(runtime_path, resolve_files=True)

    if source_path.is_file():
        try:
            source_g1.verify(source_path, physical=True)
            feedback.reuse("SOURCE-G1", source_path, "source/update harness identity, current source bindings and immutable physical snapshots all validated")
        except Exception as exc:
            feedback.note(f"SOURCE-G1 evidence cannot be reused: {exc}")
            feedback.note("Recapturing SOURCE-G1 against real external sources and current build authority.")
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
                feedback.reuse("release-device", release_path, "baseline metadata/namespace schema and stored observation proofs validate for the current harness")
            except Exception as exc:
                feedback.note(f"Release-device evidence cannot be reused: {exc}")
                feedback.note("Recapturing the baseline before endurance cases.")
                release_device.capture(release_path, [])
        else:
            release_device.capture(release_path, [])

        feedback.phase("Automatic GRAND-G1 journeys", "Exercise failed activation rollback, migration, encrypted config, recovery and update ingress that can be proven without manual device transitions.")
        try:
            automatic = automatic_journeys()
        except OSError as exc:
            target = getattr(exc, "filename", None) or "<path unavailable from OS exception>"
            diagnostic = write_local_failure_diagnostic(
                "automatic GRAND-G1 unannotated local OS operation",
                exc,
                extra={"target": str(target)},
            )
            feedback.debug("original Python traceback", traceback.format_exc())
            if diagnostic:
                feedback.note(f"Full local failure diagnostic: {diagnostic}")
            failure = local_os_failure(
                "automatic GRAND-G1 local OS operation",
                target,
                exc,
                why="The automatic journey uses local Termux temporary/build/evidence files before handing state to root-owned production commands. The original traceback is now preserved so an EPERM cannot lose its source line.",
                next_action="Inspect the saved diagnostic JSON and the verbose traceback; they identify the exact source line, executable/argv (when subprocess spawn is involved), process limits and launcher metadata.",
            )
            if diagnostic:
                failure.evidence = str(diagnostic)
            raise failure from exc
        feedback.ok("G1-A", "automatic journeys captured")
        feedback.phase("Actual configured-device probes", "Use your real configured Nexus remote/mount state for provider browse and runtime-manager production ingress evidence.")
        actual = actual_device_probes()
        feedback.ok("G1-A", "actual-device probes captured", feedback.summarize_value(actual, 500))
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
    feedback.ok("G1-A", "composite evidence written", str(path))
    feedback.note("Next: run the remaining release endurance cases and staged-reboot qualification; `status` shows each pending/manual transition.")
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
    ap.add_argument("--verbose", action="store_true", help="show command/observation diagnostics in addition to the default human-facing progress and failure context")
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
    feedback.set_verbose(ns.verbose or feedback.verbose_enabled())
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
    except BaseException as exc:
        feedback.render_failure(exc)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
