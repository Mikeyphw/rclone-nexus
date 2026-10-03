#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from zipfile import ZipFile, BadZipFile

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_NEXUS_ZIP = ROOT / "dist" / "rclone-nexus-v0.1.0.zip"
PROVIDER_REPO = "NewFuture/rclone-fuse3-magisk"
GITHUB_LATEST = f"https://api.github.com/repos/{PROVIDER_REPO}/releases/latest"


class InstallError(RuntimeError):
    pass


def parse_prop_text(text: str) -> dict[str, str]:
    out: dict[str, str] = {}
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        out[k.strip()] = v.strip()
    return out


def zip_module_prop(path: Path) -> dict[str, str]:
    try:
        with ZipFile(path) as zf:
            names = zf.namelist()
            if "module.prop" not in names:
                raise InstallError(f"{path}: module.prop must be at archive root")
            return parse_prop_text(zf.read("module.prop").decode("utf-8", "strict"))
    except BadZipFile as exc:
        raise InstallError(f"{path}: invalid module zip: {exc}") from exc


def require_module_zip(path: Path, expected_id: str) -> dict[str, str]:
    path = path.expanduser().resolve()
    if not path.is_file():
        raise InstallError(f"module zip not found: {path}")
    props = zip_module_prop(path)
    if props.get("id") != expected_id:
        raise InstallError(f"{path}: expected module id {expected_id!r}, got {props.get('id')!r}")
    return props


def arch_aliases(machine: str | None = None) -> tuple[str, ...]:
    m = (machine or platform.machine()).lower()
    if m in {"aarch64", "arm64", "arm64-v8a"}:
        return ("arm64-v8a", "arm64", "aarch64")
    if m in {"armv7l", "arm", "armeabi-v7a"}:
        return ("armeabi-v7a", "armv7", "arm")
    if m in {"x86_64", "amd64", "x64"}:
        return ("x86_64", "x64", "amd64")
    if m in {"i386", "i686", "x86"}:
        return ("x86", "i686", "i386")
    return (m,)


def select_provider_assets(release: dict, machine: str | None = None) -> list[dict]:
    assets = [a for a in release.get("assets", []) if str(a.get("name", "")).lower().endswith(".zip") and a.get("browser_download_url")]
    aliases = arch_aliases(machine)

    def score(a: dict) -> tuple[int, int, str]:
        name = str(a.get("name", "")).lower()
        arch_score = 0 if any(alias in name for alias in aliases) else 1
        module_score = 0 if ("rclone" in name or "magisk" in name) else 1
        return (arch_score, module_score, name)

    return sorted(assets, key=score)


class RootBroker:
    def __init__(self, adb_dir: str | None = None, force_root: bool | None = None):
        self.adb_dir = Path(adb_dir or os.environ.get("RNEXUS_ADB_DIR", "/data/adb"))
        self._is_root = os.geteuid() == 0 if force_root is None else force_root
        if not self._is_root and shutil.which("su") is None:
            raise InstallError("root access required: su not found")

    @property
    def privilege(self) -> str:
        return "direct-root" if self._is_root else "su-c"

    def run(self, argv: list[str], *, check: bool = True, capture: bool = True, timeout: int = 180) -> subprocess.CompletedProcess[str]:
        if self._is_root:
            cmd = argv
        else:
            cmd = ["su", "-c", shlex.join(argv)]
        return subprocess.run(cmd, text=True, capture_output=capture, check=check, timeout=timeout)

    def shell(self, script: str, *, check: bool = True, timeout: int = 180) -> subprocess.CompletedProcess[str]:
        if self._is_root:
            cmd = ["sh", "-c", script]
        else:
            cmd = ["su", "-c", script]
        return subprocess.run(cmd, text=True, capture_output=True, check=check, timeout=timeout)

    def exists(self, path: Path) -> bool:
        cp = self.run(["test", "-e", str(path)], check=False)
        return cp.returncode == 0

    def is_file(self, path: Path) -> bool:
        cp = self.run(["test", "-f", str(path)], check=False)
        return cp.returncode == 0

    def is_dir(self, path: Path) -> bool:
        cp = self.run(["test", "-d", str(path)], check=False)
        return cp.returncode == 0

    def root_which(self, name: str) -> str | None:
        cp = self.shell(f"command -v {shlex.quote(name)} 2>/dev/null || true", check=False)
        out = cp.stdout.strip().splitlines()
        return out[0] if out else None

    def read_text(self, path: Path) -> str:
        cp = self.run(["cat", str(path)])
        return cp.stdout



def detect_manager(broker: RootBroker) -> dict[str, str]:
    adb = broker.adb_dir
    # Order matters: KernelSU Next is a specialization of KernelSU.
    ksud_next = broker.root_which("ksud-next")
    ksud = broker.root_which("ksud")
    apd = broker.root_which("apd")
    magisk = broker.root_which("magisk")
    if broker.is_dir(adb / "ksu") and (broker.exists(adb / "ksu" / ".ksu_next") or ksud_next):
        return {"kind": "kernelsu-next", "command": ksud_next or ksud or "ksud"}
    if broker.is_dir(adb / "ksu") or ksud:
        return {"kind": "kernelsu", "command": ksud or "ksud"}
    if broker.is_dir(adb / "ap") or broker.is_dir(adb / "apatch") or apd:
        return {"kind": "apatch", "command": apd or "apd"}
    if broker.is_dir(adb / "magisk") or magisk:
        return {"kind": "magisk", "command": magisk or "magisk"}
    if broker.is_dir(adb / "modules"):
        return {"kind": "compatible", "command": ""}
    return {"kind": "unknown", "command": ""}


def manager_install_argv(manager: dict[str, str], staged_zip: str) -> list[str]:
    kind = manager["kind"]
    command = manager.get("command") or ""
    if kind == "magisk":
        return [command, "--install-module", staged_zip]
    if kind in {"kernelsu", "kernelsu-next"}:
        return [command, "module", "install", staged_zip]
    if kind == "apatch":
        return [command, "module", "install", staged_zip]
    raise InstallError(f"automatic module installation unsupported for root manager: {kind}")


def module_locations(broker: RootBroker, module_id: str) -> list[Path]:
    return [broker.adb_dir / "modules" / module_id, broker.adb_dir / "modules_update" / module_id]


def module_status(broker: RootBroker, module_id: str) -> dict:
    found: list[dict] = []
    for root in module_locations(broker, module_id):
        prop = root / "module.prop"
        if broker.is_file(prop):
            props = parse_prop_text(broker.read_text(prop))
            found.append({
                "path": str(root),
                "id": props.get("id", ""),
                "name": props.get("name", ""),
                "version": props.get("version", ""),
                "staged": root.parent.name == "modules_update",
                "disabled": broker.exists(root / "disable"),
                "remove": broker.exists(root / "remove"),
            })
    return {"present": bool(found), "instances": found}


def stage_zip_for_manager(broker: RootBroker, src: Path, module_id: str) -> Path:
    digest = hashlib.sha256(src.read_bytes()).hexdigest()[:16]
    dst = Path("/data/local/tmp") / f"rnexus-{module_id}-{digest}.zip"
    broker.run(["cp", str(src), str(dst)])
    broker.run(["chmod", "0644", str(dst)])
    return dst


def install_module(broker: RootBroker, manager: dict[str, str], src: Path, expected_id: str) -> dict:
    props = require_module_zip(src, expected_id)
    staged = stage_zip_for_manager(broker, src, expected_id)
    try:
        argv = manager_install_argv(manager, str(staged))
        cp = broker.run(argv, check=False, timeout=300)
        if cp.returncode != 0:
            detail = (cp.stderr or cp.stdout or "").strip()
            raise InstallError(f"{manager['kind']} module install failed ({cp.returncode}): {detail}")
    finally:
        broker.run(["rm", "-f", str(staged)], check=False)
    status = module_status(broker, expected_id)
    if not status["present"]:
        raise InstallError(f"root manager returned success but module {expected_id!r} is not staged/installed")
    return {"module": expected_id, "version": props.get("version", ""), "manager": manager["kind"], "status": status}


def github_json(url: str) -> dict:
    req = urllib.request.Request(url, headers={"Accept": "application/vnd.github+json", "User-Agent": "rclone-nexus-install-stack/1"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def download_to(url: str, dest: Path) -> None:
    req = urllib.request.Request(url, headers={"User-Agent": "rclone-nexus-install-stack/1"})
    with urllib.request.urlopen(req, timeout=90) as resp, dest.open("wb") as out:
        shutil.copyfileobj(resp, out)


def fetch_latest_provider(machine: str | None = None) -> Path:
    release = github_json(GITHUB_LATEST)
    assets = select_provider_assets(release, machine)
    if not assets:
        raise InstallError("latest provider release exposes no ZIP assets")
    temp_root = Path(tempfile.mkdtemp(prefix="rnexus-provider-"))
    failures: list[str] = []
    for index, asset in enumerate(assets):
        name = str(asset.get("name") or f"provider-{index}.zip")
        digest = str(asset.get("digest") or "")
        if not digest.startswith("sha256:"):
            failures.append(f"{name}: missing GitHub SHA-256 digest")
            continue
        dest = temp_root / name
        try:
            download_to(str(asset["browser_download_url"]), dest)
            actual = hashlib.sha256(dest.read_bytes()).hexdigest()
            if actual.lower() != digest.split(":", 1)[1].lower():
                failures.append(f"{name}: digest mismatch")
                dest.unlink(missing_ok=True)
                continue
            props = zip_module_prop(dest)
            if props.get("id") != "rclone":
                failures.append(f"{name}: module id {props.get('id')!r}")
                dest.unlink(missing_ok=True)
                continue
            return dest
        except Exception as exc:  # candidate probing; report all failures together
            failures.append(f"{name}: {exc}")
            dest.unlink(missing_ok=True)
    raise InstallError("no trustworthy provider asset matched this stack:\n  " + "\n  ".join(failures))


def provider_binary_candidates(module_dir: Path) -> list[Path]:
    return [module_dir / "system" / "vendor" / "bin" / "rclone", module_dir / "vendor" / "bin" / "rclone"]


def verify_stack(broker: RootBroker, *, require_runtime: bool = True) -> dict:
    provider = module_status(broker, "rclone")
    nexus = module_status(broker, "rclone_nexus")
    errors: list[str] = []
    # A legacy provider is optional under RUNTIME-STANDALONE. Its presence is
    # reported for migration/external compatibility, but managed mode must not
    # depend on it merely to install or verify Nexus.
    if not nexus["present"]:
        errors.append("Nexus module rclone_nexus is not installed/staged")

    provider_binary = ""
    provider_active = broker.adb_dir / "modules" / "rclone"
    if broker.is_dir(provider_active):
        for candidate in provider_binary_candidates(provider_active):
            if broker.is_file(candidate):
                provider_binary = str(candidate)
                break
        if not provider_binary:
            errors.append("active provider is missing its rclone binary")

    nexus_active = broker.adb_dir / "modules" / "rclone_nexus"
    racctl = nexus_active / "system" / "bin" / "racctl"
    nexus_wrapper = nexus_active / "system" / "bin" / "rclone-nexus"
    racctl_ok = broker.is_file(racctl)
    wrapper_ok = broker.is_file(nexus_wrapper)
    runtime_checks: dict[str, object] = {}
    if broker.is_dir(nexus_active) and not racctl_ok:
        errors.append("active Nexus module is missing system/bin/racctl")
    if broker.is_dir(nexus_active) and not wrapper_ok:
        errors.append("active Nexus module is missing system/bin/rclone-nexus")
    if require_runtime and racctl_ok and wrapper_ok:
        # Runtime authority is resolved by racctl itself. The wrapper checks
        # remain for compatibility UI/health surfaces, but they no longer own
        # executable/config selection in managed mode.
        for label, argv in {
            "validate_upgrade": [str(racctl), "platform", "validate-upgrade"],
            "verify_integrity": [str(racctl), "platform", "verify-integrity"],
            "version": [str(racctl), "version"],
            "runtime_authority": [str(racctl), "runtime", "status", "--json", "--require-operational"],
            "provider": [str(nexus_wrapper), "provider"],
            "health": [str(nexus_wrapper), "health"],
        }.items():
            cp = broker.run(argv, check=False, timeout=30)
            runtime_checks[label] = {"exit": cp.returncode, "stdout": cp.stdout.strip()[:2000], "stderr": cp.stderr.strip()[:1000]}
            if cp.returncode != 0:
                errors.append(f"racctl {label} failed with exit {cp.returncode}")

    reboot_required = any(instance.get("staged") for state in (provider, nexus) for instance in state["instances"])
    return {
        "ok": not errors,
        "privilege": broker.privilege,
        "provider": provider,
        "nexus": nexus,
        "provider_binary": provider_binary,
        "racctl": str(racctl) if racctl_ok else "",
        "runtime": runtime_checks,
        "reboot_required": reboot_required,
        "errors": errors,
    }


def print_json(data: object) -> None:
    print(json.dumps(data, indent=2, sort_keys=True))


def do_install(args: argparse.Namespace, *, stack: bool) -> int:
    broker = RootBroker(adb_dir=args.adb_dir)
    manager = detect_manager(broker)
    if manager["kind"] in {"unknown", "compatible"}:
        raise InstallError(f"cannot safely automate installation for detected manager {manager['kind']!r}")

    provider = module_status(broker, "rclone")
    provider_result: object = {"action": "kept-existing", "status": provider}
    replace_provider = bool(getattr(args, "replace_provider", False))
    if stack and (not provider["present"] or replace_provider):
        if provider["present"] and not replace_provider:
            raise AssertionError("unreachable")
        if args.provider_zip:
            provider_zip = Path(args.provider_zip)
        elif args.provider_latest:
            provider_zip = fetch_latest_provider()
        else:
            raise InstallError("provider is missing; pass --provider-latest or --provider-zip PATH")
        provider_result = {"action": "installed", "result": install_module(broker, manager, provider_zip, "rclone")}
    # Plain install is now valid without NewFuture. install-stack remains an
    # explicit legacy/external bootstrap path during migration.

    nexus_zip = Path(args.nexus_zip or DEFAULT_NEXUS_ZIP)
    nexus_result = install_module(broker, manager, nexus_zip, "rclone_nexus")
    verify = verify_stack(broker, require_runtime=False)
    staged_nexus = any(
        instance.get("staged") and instance.get("id") == "rclone_nexus"
        for instance in verify["nexus"]["instances"]
    )
    errors = list(verify.get("errors", []))
    deferred_errors = {"active Nexus module is missing system/bin/racctl"}
    deferred_only = bool(errors) and all(error in deferred_errors for error in errors)
    reboot_required = bool(verify["reboot_required"])

    if verify["ok"]:
        status = "installed-reboot-required" if reboot_required else "installed"
        exit_code = 0
    elif reboot_required and staged_nexus and deferred_only:
        # KernelSU may leave the previous active Nexus visible while the
        # complete replacement waits in modules_update/. Runtime verification
        # belongs to install-verify after reboot.
        status = "installed-reboot-required"
        exit_code = 0
    else:
        status = "installed-with-verification-errors"
        exit_code = 2

    result = {
        "status": status,
        "manager": manager,
        "provider": provider_result,
        "nexus": nexus_result,
        "verification": verify,
        "next": "reboot, then run ./devtoolw install-verify" if reboot_required else "run ./devtoolw install-verify",
    }
    print_json(result)
    return exit_code


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Build/install verification helper for the Rclone root-module + Rclone Nexus stack")
    p.add_argument("--adb-dir", default=os.environ.get("RNEXUS_ADB_DIR", "/data/adb"))
    sub = p.add_subparsers(dest="command", required=True)

    def add_install(sp: argparse.ArgumentParser, *, stack: bool) -> None:
        sp.add_argument("--nexus-zip", default=os.environ.get("RNEXUS_NEXUS_ZIP", str(DEFAULT_NEXUS_ZIP)))
        if stack:
            sp.add_argument("--provider-zip", default=os.environ.get("RNEXUS_PROVIDER_ZIP"))
            sp.add_argument("--provider-latest", action="store_true", default=os.environ.get("RNEXUS_PROVIDER_LATEST", "1") not in {"0", "false", "no"})
            sp.add_argument("--replace-provider", action="store_true", default=os.environ.get("RNEXUS_REPLACE_PROVIDER", "0") in {"1", "true", "yes"})

    add_install(sub.add_parser("install", help="install/update Nexus only; managed runtime does not require a provider module"), stack=False)
    add_install(sub.add_parser("install-stack", help="bootstrap provider if missing, then install Nexus"), stack=True)
    verify = sub.add_parser("verify", help="verify Nexus runtime authority and optional provider compatibility after reboot")
    verify.add_argument("--no-runtime", action="store_true")
    sub.add_parser("status", help="show root manager and module staging state")
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        if args.command == "install":
            return do_install(args, stack=False)
        if args.command == "install-stack":
            return do_install(args, stack=True)
        broker = RootBroker(adb_dir=args.adb_dir)
        if args.command == "verify":
            result = verify_stack(broker, require_runtime=not args.no_runtime)
            print_json(result)
            return 0 if result["ok"] else 2
        if args.command == "status":
            print_json({"manager": detect_manager(broker), "provider": module_status(broker, "rclone"), "nexus": module_status(broker, "rclone_nexus"), "privilege": broker.privilege})
            return 0
        raise AssertionError(args.command)
    except InstallError as exc:
        print(f"install-stack: ERROR: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
