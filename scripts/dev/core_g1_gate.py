#!/usr/bin/env python3
from __future__ import annotations

from hashlib import sha256
from pathlib import Path
import os
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def run(*argv: str, env: dict[str, str] | None = None) -> None:
    merged = os.environ.copy()
    if env:
        merged.update(env)
    print("$", " ".join(argv), flush=True)
    subprocess.run(argv, cwd=ROOT, env=merged, check=True)


def digest(path: Path) -> str:
    return sha256(path.read_bytes()).hexdigest()


def static_security_audit() -> None:
    roots = [ROOT / "cmd", ROOT / "internal", ROOT / "module"]
    product_files: list[Path] = []
    for base in roots:
        product_files.extend(p for p in base.rglob("*") if p.is_file() and not p.name.endswith("_test.go"))

    forbidden = [
        (re.compile(r"\beval\b"), "eval"),
        (re.compile(r"\b(?:killall|pkill)\b"), "global process kill"),
        (re.compile(r"\bsh\s+-c\b"), "shell command-string execution"),
        (re.compile(r'exec\.Command(?:Context)?\([^\n]*["\']sh["\'][^\n]*["\']-c["\']'), "Go shell command-string execution"),
    ]
    failures: list[str] = []
    for path in product_files:
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        for pattern, label in forbidden:
            if pattern.search(text):
                failures.append(f"{path.relative_to(ROOT)}: forbidden {label}")
    if failures:
        raise SystemExit("CORE-G1 security audit failed:\n" + "\n".join(failures))

    common = (ROOT / "module/lib/common.sh").read_text(encoding="utf-8")
    if "/data/adb/rclone-nexus" not in common:
        raise SystemExit("CORE-G1: persistent state root is no longer canonical")
    uninstall = (ROOT / "module/uninstall.sh").read_text(encoding="utf-8")
    if re.search(r"\brm\s+-[^\n]*r[^\n]*f", uninstall):
        raise SystemExit("CORE-G1: uninstall script contains recursive forced deletion")


def module_replacement_simulation() -> None:
    with tempfile.TemporaryDirectory(prefix="rnx-core-g1-") as temp_text:
        temp = Path(temp_text)
        state = temp / "persistent-state"
        (state / "config").mkdir(parents=True)
        (state / "desired").mkdir(parents=True)
        (state / "health").mkdir(parents=True)
        (state / "operations").mkdir(parents=True)
        canaries = {
            state / "config/registry-v2.json": b'{"schema_version":2,"revision":77,"digest":"gate-canary","mounts":[]}\n',
            state / "desired/drive.json": b'{"schema_version":1,"state":"stopped"}\n',
            state / "health/drive.json": b'{"schema_version":1,"name":"drive","state":"STOPPED"}\n',
            state / "operations/gate-op.json": b'{"schema_version":1,"request_id":"gate-op","state":"SUCCEEDED"}\n',
        }
        for path, payload in canaries.items():
            path.write_bytes(payload)
        before = {path.relative_to(state): digest(path) for path in canaries}

        for generation in (1, 2):
            module_copy = temp / f"module-{generation}"
            shutil.copytree(ROOT / "module", module_copy)
            env = {
                "RNEXUS_STATE_DIR": str(state),
                "RNEXUS_MODULE_DIR": str(module_copy),
                "RNEXUS_PROVIDER_MODULE_DIR": str(temp / "provider"),
            }
            subprocess.run(["sh", str(module_copy / "post-fs-data.sh")], cwd=ROOT, env={**os.environ, **env}, check=True)
            shutil.rmtree(module_copy)

        after = {rel: digest(state / rel) for rel in before}
        if before != after:
            raise SystemExit(f"CORE-G1: module replacement mutated persistent canaries: before={before} after={after}")

        # Uninstall is also non-destructive; it may leave an informational
        # marker, but campaign state/configuration must survive untouched.
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
            raise SystemExit(f"CORE-G1: uninstall mutated persistent canaries: before={before} after={final}")


def main() -> None:
    static_security_audit()
    module_replacement_simulation()
    print("CORE-G1 control-plane/lifecycle gate audit: PASS")


if __name__ == "__main__":
    main()
