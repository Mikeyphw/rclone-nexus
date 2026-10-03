from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from zipfile import ZipFile

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("install_stack", ROOT / "scripts" / "dev" / "install_stack.py")
mod = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
SPEC.loader.exec_module(mod)


class FakeBroker:
    def __init__(self, root: Path, commands: dict[str, str] | None = None):
        self.adb_dir = root / "adb"
        self.privilege = "fake-root"
        self.commands = commands or {}
        self.runs = []
        self.adb_dir.mkdir(parents=True)

    def is_dir(self, p: Path): return p.is_dir()
    def is_file(self, p: Path): return p.is_file()
    def exists(self, p: Path): return p.exists()
    def read_text(self, p: Path): return p.read_text()
    def root_which(self, name: str): return self.commands.get(name)
    def shell(self, script: str, check: bool = True, timeout: int = 180):
        class CP: returncode=0; stdout=""; stderr=""
        return CP()
    def run(self, argv, check=True, capture=True, timeout=180):
        self.runs.append(list(argv))
        class CP: returncode=0; stdout=""; stderr=""
        return CP()


def module_dir(b: FakeBroker, root_name: str, module_id: str, version="v1"):
    d = b.adb_dir / root_name / module_id
    d.mkdir(parents=True)
    (d / "module.prop").write_text(f"id={module_id}\nname={module_id}\nversion={version}\n")
    return d


def make_zip(path: Path, module_id: str):
    with ZipFile(path, "w") as z:
        z.writestr("module.prop", f"id={module_id}\nname={module_id}\nversion=v1\n")


class InstallStackTests(unittest.TestCase):
    def test_manager_install_commands(self):
        self.assertEqual(mod.manager_install_argv({"kind":"magisk","command":"magisk"}, "/tmp/m.zip"), ["magisk","--install-module","/tmp/m.zip"])
        self.assertEqual(mod.manager_install_argv({"kind":"kernelsu","command":"ksud"}, "/tmp/m.zip"), ["ksud","module","install","/tmp/m.zip"])
        self.assertEqual(mod.manager_install_argv({"kind":"kernelsu-next","command":"ksud"}, "/tmp/m.zip"), ["ksud","module","install","/tmp/m.zip"])
        self.assertEqual(mod.manager_install_argv({"kind":"apatch","command":"apd"}, "/tmp/m.zip"), ["apd","module","install","/tmp/m.zip"])

    def test_detection_prefers_kernelsu_next(self):
        with tempfile.TemporaryDirectory() as td:
            b = FakeBroker(Path(td), {"ksud":"/system/bin/ksud"})
            (b.adb_dir / "ksu").mkdir()
            (b.adb_dir / "ksu" / ".ksu_next").touch()
            self.assertEqual(mod.detect_manager(b)["kind"], "kernelsu-next")

    def test_existing_provider_is_preserved_by_default_contract(self):
        with tempfile.TemporaryDirectory() as td:
            b = FakeBroker(Path(td))
            module_dir(b, "modules", "rclone", "v1.75.1")
            st = mod.module_status(b, "rclone")
            self.assertTrue(st["present"])
            self.assertFalse(st["instances"][0]["staged"])

    def test_stack_verify_accepts_provider_and_staged_nexus_and_requires_reboot(self):
        with tempfile.TemporaryDirectory() as td:
            b = FakeBroker(Path(td))
            provider = module_dir(b, "modules", "rclone", "v1.75.1")
            (provider / "system/vendor/bin").mkdir(parents=True)
            (provider / "system/vendor/bin/rclone").touch()
            nexus = module_dir(b, "modules_update", "rclone_nexus", "v0.1.0")
            (nexus / "system/bin").mkdir(parents=True)
            (nexus / "system/bin/racctl").touch()
            result = mod.verify_stack(b, require_runtime=False)
            self.assertTrue(result["ok"])
            self.assertTrue(result["reboot_required"])
            self.assertTrue(result["provider_binary"].endswith("system/vendor/bin/rclone"))

    def test_stack_verify_accepts_provider_missing_for_managed_nexus(self):
        with tempfile.TemporaryDirectory() as td:
            b = FakeBroker(Path(td))
            module_dir(b, "modules_update", "rclone_nexus")
            result = mod.verify_stack(b, require_runtime=False)
            self.assertTrue(result["ok"])
            self.assertFalse(result["provider"]["present"])
            self.assertEqual(result["provider_binary"], "")

    def test_module_zip_id_is_verified(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "x.zip"
            make_zip(p, "wrong")
            with self.assertRaises(mod.InstallError):
                mod.require_module_zip(p, "rclone")

    def test_provider_asset_selection_prefers_device_arch(self):
        release = {"assets":[
            {"name":"magisk-rclone-x86_64.zip","browser_download_url":"x"},
            {"name":"magisk-rclone-arm64-v8a.zip","browser_download_url":"a"},
        ]}
        selected = mod.select_provider_assets(release, "aarch64")
        self.assertEqual(selected[0]["name"], "magisk-rclone-arm64-v8a.zip")

    def test_plain_install_parser_defaults_replace_provider_false(self):
        args = mod.build_parser().parse_args(["install", "--nexus-zip", "/tmp/nexus.zip"])
        self.assertEqual(args.command, "install")
        self.assertFalse(getattr(args, "replace_provider", False))

    def test_install_stack_parser_exposes_replace_provider(self):
        args = mod.build_parser().parse_args(["install-stack", "--replace-provider", "--nexus-zip", "/tmp/nexus.zip"])
        self.assertEqual(args.command, "install-stack")
        self.assertTrue(args.replace_provider)

    def test_main_dispatch_plain_install_reaches_do_install_without_attribute_error(self):
        calls = []
        original = mod.do_install
        try:
            def fake(args, *, stack):
                calls.append((args.command, stack, getattr(args, "replace_provider", False)))
                return 0
            mod.do_install = fake
            rc = mod.main(["install", "--nexus-zip", "/tmp/nexus.zip"])
        finally:
            mod.do_install = original
        self.assertEqual(rc, 0)
        self.assertEqual(calls, [("install", False, False)])

    def test_main_dispatch_install_stack_preserves_replace_provider(self):
        calls = []
        original = mod.do_install
        try:
            def fake(args, *, stack):
                calls.append((args.command, stack, args.replace_provider))
                return 0
            mod.do_install = fake
            rc = mod.main(["install-stack", "--replace-provider", "--nexus-zip", "/tmp/nexus.zip"])
        finally:
            mod.do_install = original
        self.assertEqual(rc, 0)
        self.assertEqual(calls, [("install-stack", True, True)])


    def test_customize_is_staging_safe_and_defers_runtime_checks(self):
        text = (ROOT / "module" / "customize.sh").read_text()
        self.assertIn('Package staging checks passed', text)
        self.assertIn('Runtime/state/integrity checks will run via install-verify after reboot', text)
        self.assertNotIn('platform validate-upgrade', text)
        self.assertNotIn('platform verify-integrity', text)
        self.assertIn('[ -x "$MODPATH/system/bin/racctl" ] || abort', text)
        self.assertIn('[ -f "$MODPATH/integrity.manifest.json" ] || abort', text)

    def test_install_verify_owns_deferred_runtime_and_integrity_checks(self):
        with tempfile.TemporaryDirectory() as td:
            b = FakeBroker(Path(td))
            provider = module_dir(b, "modules", "rclone", "v1.75.1")
            (provider / "system/vendor/bin").mkdir(parents=True)
            (provider / "system/vendor/bin/rclone").touch()
            nexus = module_dir(b, "modules", "rclone_nexus", "v0.1.0")
            (nexus / "system/bin").mkdir(parents=True)
            (nexus / "system/bin/racctl").touch()
            (nexus / "system/bin/rclone-nexus").touch()
            result = mod.verify_stack(b, require_runtime=True)
            self.assertTrue(result["ok"])
            expected = [
                [str(nexus / "system/bin/racctl"), "platform", "validate-upgrade"],
                [str(nexus / "system/bin/racctl"), "platform", "verify-integrity"],
                [str(nexus / "system/bin/racctl"), "version"],
                [str(nexus / "system/bin/racctl"), "runtime", "status", "--json", "--require-operational"],
                [str(nexus / "system/bin/rclone-nexus"), "provider"],
                [str(nexus / "system/bin/rclone-nexus"), "health"],
            ]
            self.assertEqual(b.runs[:6], expected)
            self.assertIn("validate_upgrade", result["runtime"])
            self.assertIn("verify_integrity", result["runtime"])
            self.assertIn("runtime_authority", result["runtime"])
            self.assertIn("provider", result["runtime"])
            self.assertIn("health", result["runtime"])

    def test_install_verify_fails_when_canonical_runtime_is_not_operational(self):
        with tempfile.TemporaryDirectory() as td:
            b = FakeBroker(Path(td))
            nexus = module_dir(b, "modules", "rclone_nexus", "v0.1.0")
            (nexus / "system/bin").mkdir(parents=True)
            (nexus / "system/bin/racctl").touch()
            (nexus / "system/bin/rclone-nexus").touch()
            original_run = b.run

            def run(argv, check=True, capture=True, timeout=180):
                cp = original_run(argv, check=check, capture=capture, timeout=timeout)
                if list(argv)[1:] == ["runtime", "status", "--json", "--require-operational"]:
                    cp.returncode = 1
                    cp.stderr = "runtime authority is not operational"
                return cp

            b.run = run
            result = mod.verify_stack(b, require_runtime=True)
            self.assertFalse(result["ok"])
            self.assertIn("racctl runtime_authority failed with exit 1", result["errors"])

    def test_staged_nexus_defers_only_stale_active_runtime_error(self):
        import argparse
        from unittest import mock

        args = argparse.Namespace(
            adb_dir="/data/adb",
            nexus_zip="dist/rclone-nexus-v0.1.0.zip",
            replace_provider=False,
        )

        provider = {
            "present": True,
            "instances": [{"id": "rclone", "staged": False}],
        }
        staged_nexus = {
            "module": "rclone_nexus",
            "manager": "kernelsu",
            "version": "v0.1.0",
            "status": {
                "present": True,
                "instances": [{"id": "rclone_nexus", "staged": True}],
            },
        }
        verification = {
            "ok": False,
            "reboot_required": True,
            "errors": ["active Nexus module is missing system/bin/racctl"],
            "nexus": {
                "instances": [{"id": "rclone_nexus", "staged": True}],
            },
        }

        with (
            mock.patch.object(mod, "RootBroker"),
            mock.patch.object(
                mod,
                "detect_manager",
                return_value={"kind": "kernelsu", "command": "ksud"},
            ),
            mock.patch.object(mod, "module_status", return_value=provider),
            mock.patch.object(mod, "install_module", return_value=staged_nexus),
            mock.patch.object(mod, "verify_stack", return_value=verification),
            mock.patch.object(mod, "print_json") as print_json,
        ):
            rc = mod.do_install(args, stack=False)

        self.assertEqual(rc, 0)
        self.assertEqual(
            print_json.call_args.args[0]["status"],
            "installed-reboot-required",
        )

    def test_staged_nexus_does_not_hide_unrelated_verification_error(self):
        import argparse
        from unittest import mock

        args = argparse.Namespace(
            adb_dir="/data/adb",
            nexus_zip="dist/rclone-nexus-v0.1.0.zip",
            replace_provider=False,
        )

        provider = {
            "present": True,
            "instances": [{"id": "rclone", "staged": False}],
        }
        staged_nexus = {
            "module": "rclone_nexus",
            "manager": "kernelsu",
            "version": "v0.1.0",
            "status": {
                "present": True,
                "instances": [{"id": "rclone_nexus", "staged": True}],
            },
        }
        verification = {
            "ok": False,
            "reboot_required": True,
            "errors": ["active provider is missing its rclone binary"],
            "nexus": {
                "instances": [{"id": "rclone_nexus", "staged": True}],
            },
        }

        with (
            mock.patch.object(mod, "RootBroker"),
            mock.patch.object(
                mod,
                "detect_manager",
                return_value={"kind": "kernelsu", "command": "ksud"},
            ),
            mock.patch.object(mod, "module_status", return_value=provider),
            mock.patch.object(mod, "install_module", return_value=staged_nexus),
            mock.patch.object(mod, "verify_stack", return_value=verification),
            mock.patch.object(mod, "print_json") as print_json,
        ):
            rc = mod.do_install(args, stack=False)

        self.assertEqual(rc, 2)
        self.assertEqual(
            print_json.call_args.args[0]["status"],
            "installed-with-verification-errors",
        )

    def test_install_verify_requires_env_loading_wrapper(self):
        with tempfile.TemporaryDirectory() as td:
            b = FakeBroker(Path(td))
            provider = module_dir(b, "modules", "rclone", "v1.75.1")
            (provider / "system/vendor/bin").mkdir(parents=True)
            (provider / "system/vendor/bin/rclone").touch()
            nexus = module_dir(b, "modules", "rclone_nexus", "v0.1.0")
            (nexus / "system/bin").mkdir(parents=True)
            (nexus / "system/bin/racctl").touch()
            result = mod.verify_stack(b, require_runtime=True)
            self.assertFalse(result["ok"])
            self.assertTrue(any("rclone-nexus" in item for item in result["errors"]))
            self.assertEqual(b.runs, [])

    def test_devtool_exposes_install_workflows(self):
        text = (ROOT / ".devtool.toml").read_text()
        for needle in ["[wrapper.commands.install]", "[wrapper.commands.install-stack]", "[wrapper.commands.install-verify]", "install-stack = [", "install-verify = ["]:
            self.assertIn(needle, text)


if __name__ == "__main__":
    unittest.main()
