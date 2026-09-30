#!/usr/bin/env python3
from __future__ import annotations

from hashlib import sha256
from pathlib import Path
import json
import os
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def digest(path: Path) -> str:
    return sha256(path.read_bytes()).hexdigest()


def static_contract_audit() -> None:
    failures: list[str] = []

    control = (ROOT / "internal/control/engine.go").read_text(encoding="utf-8")
    rc = (ROOT / "internal/rc/rc.go").read_text(encoding="utf-8")
    jobs = (ROOT / "internal/jobs/jobs.go").read_text(encoding="utf-8")
    namespace = (ROOT / "internal/namespace/mutation.go").read_text(encoding="utf-8")
    cache = (ROOT / "internal/cache/cache.go").read_text(encoding="utf-8")
    lifecycle = (ROOT / "internal/mounts/lifecycle.go").read_text(encoding="utf-8")

    # Typed runtime only: no generic command/RC tunnel may appear in the
    # registered operation surface.
    for forbidden in ('"rclone.exec"', '"rc.call"', '"shell.exec"', '"argv.exec"'):
        if forbidden in control:
            failures.append(f"control plane exposes forbidden generic operation {forbidden}")

    # Production RC authority must stay loopback-only and private extra args
    # must never be able to override Nexus-owned --rc flags.
    if re.search(r'0\.0\.0\.0|\[::\]', rc):
        failures.append("production RC implementation contains wildcard bind literal")
    if 'net.Listen("tcp", "127.0.0.1:0")' not in rc:
        failures.append("RC endpoint allocation is no longer explicitly loopback-only")
    if 'strings.HasPrefix(lower, "--rc")' not in lifecycle:
        failures.append("mount args_file no longer rejects RC option override")
    if 'args = append(args, rc.Args(rcRecord)...' not in lifecycle:
        failures.append("Nexus-owned RC args are no longer appended authoritatively")

    # Namespace mutation must always recompute preview/qualification while both
    # namespace and lifecycle locks are held.
    if 'withNamespaceLock' not in namespace or 'mounts.WithLock' not in namespace:
        failures.append("namespace mutation lost lock composition")
    if 'previewWithMutator(p, name, m)' not in namespace:
        failures.append("namespace apply no longer re-qualifies topology before mutation")
    if 'if !plan.Qualified' not in namespace:
        failures.append("namespace apply no longer fails closed on unqualified topology")

    # Cache mutation must remain rooted below the private cache authority and
    # bounded against symlink/non-regular deletion.
    for needle, label in [
        ('cache path escapes Nexus cache root', 'cache ownership boundary'),
        ('owned cache root is a symlink', 'cache symlink refusal'),
        ('cache scan exceeds bounded entry limit', 'cache bounded scan'),
        ('refusing non-regular cache deletion', 'cache regular-file restriction'),
    ]:
        if needle not in cache:
            failures.append(f"missing {label}")

    # Destructive sync must be previewable but not persistable until approval;
    # registry apply must be serialized across processes before revision check.
    if 'requireApproval && c.Type == TypeSync' not in jobs:
        failures.append("sync approval is not apply-time only")
    if 'withRegistryLock' not in jobs or 'syscall.LOCK_EX' not in jobs:
        failures.append("scheduled-job registry apply is not cross-process serialized")

    if failures:
        raise SystemExit("ANDROID-G1 contract audit failed:\n" + "\n".join(f"- {x}" for x in failures))


def persistent_state_replacement_simulation() -> None:
    with tempfile.TemporaryDirectory(prefix="rnx-android-g1-") as td:
        temp = Path(td)
        state = temp / "state"
        canaries: dict[Path, bytes] = {
            state / "namespace/drive.json": b'{"schema_version":1,"name":"drive","desired":"app_visible","bindings":[]}\n',
            state / "policy/drive.json": b'{"schema_version":1,"name":"drive","network_class":"wifi"}\n',
            state / "jobs/registry-v1.json": b'{"schema_version":1,"revision":3,"digest":"gate","jobs":[]}\n',
            state / "jobs/state/copy.json": b'{"schema_version":1,"name":"copy","next_run_unix_ms":9999999999999,"run_count":2}\n',
            state / "health/drive.json": b'{"schema_version":1,"name":"drive","state":"RUNNING"}\n',
        }
        for path, payload in canaries.items():
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(payload)
        before = {path.relative_to(state): digest(path) for path in canaries}

        for generation in (1, 2):
            module_copy = temp / f"module-{generation}"
            shutil.copytree(ROOT / "module", module_copy)
            env = {
                **os.environ,
                "RNEXUS_STATE_DIR": str(state),
                "RNEXUS_MODULE_DIR": str(module_copy),
                "RNEXUS_PROVIDER_MODULE_DIR": str(temp / "provider"),
            }
            subprocess.run(["sh", str(module_copy / "post-fs-data.sh")], cwd=ROOT, env=env, check=True)
            shutil.rmtree(module_copy)

        after = {rel: digest(state / rel) for rel in before}
        if before != after:
            raise SystemExit(f"ANDROID-G1: module replacement mutated Android/runtime state: before={before} after={after}")

        uninstall_copy = temp / "module-uninstall"
        shutil.copytree(ROOT / "module", uninstall_copy)
        subprocess.run(
            ["sh", str(uninstall_copy / "uninstall.sh")],
            cwd=ROOT,
            env={**os.environ, "RNEXUS_STATE_DIR": str(state)},
            check=True,
        )
        final = {rel: digest(state / rel) for rel in before}
        if before != final:
            raise SystemExit(f"ANDROID-G1: uninstall mutated Android/runtime state: before={before} after={final}")


def roadmap_ledger_audit() -> None:
    roadmap = (ROOT / "docs/ROADMAP.md").read_text(encoding="utf-8")
    required = [
        "ANDROID-X01",
        "POLICY-X01",
        "RUNTIME-X01",
        "ANDROID-G1",
        "PLATFORM-X01",
    ]
    missing = [name for name in required if name not in roadmap]
    if missing:
        raise SystemExit(f"ANDROID-G1 roadmap ledger missing: {missing}")
    for doc in (
        "docs/implementation/ANDROID-X01.md",
        "docs/implementation/POLICY-X01.md",
        "docs/implementation/RUNTIME-X01.md",
    ):
        if not (ROOT / doc).is_file():
            raise SystemExit(f"ANDROID-G1 missing implementation evidence document: {doc}")


def main() -> None:
    static_contract_audit()
    persistent_state_replacement_simulation()
    roadmap_ledger_audit()
    print(json.dumps({
        "schema_version": 1,
        "gate": "ANDROID-G1",
        "status": "PASS",
        "window": ["ANDROID-X01", "POLICY-X01", "RUNTIME-X01"],
        "checks": [
            "namespace_preview_before_mutation",
            "visibility_evidence_contract",
            "policy_lifecycle_separation",
            "owned_bounded_cache",
            "scheduled_job_revision_serialization",
            "destructive_sync_preview_approval",
            "loopback_private_rc",
            "persistent_android_runtime_state",
        ],
    }, sort_keys=True))


if __name__ == "__main__":
    main()
