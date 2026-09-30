from __future__ import annotations

import json
import os
from pathlib import Path
import signal
import stat
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
MODULE = ROOT / "module"
MOUNTCTL = MODULE / "system/bin/rclone-mountctl"
NEXUS = MODULE / "system/bin/rclone-nexus"


class NexusTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.build_dir = tempfile.TemporaryDirectory()
        cls.racctl = Path(cls.build_dir.name) / "racctl"
        subprocess.run(
            ["python3", "scripts/dev/build_racctl.py", "--host", "--output", str(cls.racctl)],
            cwd=ROOT,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

    @classmethod
    def tearDownClass(cls) -> None:
        cls.build_dir.cleanup()

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.base = Path(self.tmp.name)
        self.state = self.base / "state"
        self.provider = self.base / "provider"
        (self.provider / "system/bin").mkdir(parents=True)
        (self.provider / "conf").mkdir(parents=True)
        (self.provider / "conf/rclone.conf").write_text("[fake]\ntype = local\n", encoding="utf-8")
        fake = self.provider / "system/bin/rclone"
        fake.write_text(
            "#!/bin/sh\n"
            "case ${1:-} in\n"
            "  version) echo 'rclone vTEST'; exit 0 ;;\n"
            "  mount) trap 'exit 0' TERM INT; while :; do sleep 1; done ;;\n"
            "  *) exit 0 ;;\n"
            "esac\n",
            encoding="utf-8",
        )
        fake.chmod(0o755)
        self.env = os.environ.copy()
        self.env.update(
            {
                "RNEXUS_MODULE_DIR": str(MODULE),
                "RNEXUS_STATE_DIR": str(self.state),
                "RNEXUS_PROVIDER_MODULE_DIR": str(self.provider),
                "RNEXUS_RCLONE_BIN": str(fake),
                "RNEXUS_RACCTL_BIN": str(self.racctl),
                "RNEXUS_FUSE_DEVICE": "/dev/null",
                "RNEXUS_START_GRACE_SECONDS": "0",
                "RNEXUS_STOP_TIMEOUT_SECONDS": "2",
                "RNEXUS_RACD_DISABLE": "1",
            }
        )

    def tearDown(self) -> None:
        run = self.state / "run/mounts"
        if run.exists():
            for record_file in run.glob("*.process.json"):
                try:
                    pid = int(json.loads(record_file.read_text())["pid"])
                    os.kill(pid, signal.SIGKILL)
                except (OSError, ValueError, KeyError, json.JSONDecodeError):
                    pass
        self.tmp.cleanup()

    def run_cmd(self, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        command = ["sh", str(args[0]), *args[1:]]
        return subprocess.run(
            command,
            cwd=ROOT,
            env=self.env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=check,
        )

    def write_mount(self, name: str = "drive", enabled: bool = True) -> Path:
        mounts = self.state / "mounts.d"
        mounts.mkdir(parents=True, exist_ok=True)
        mountpoint = self.base / "mnt" / name
        (mounts / f"{name}.conf").write_text(
            f"enabled={'true' if enabled else 'false'}\n"
            "remote=fake:\n"
            f"mountpoint={mountpoint}\n"
            "vfs_cache_mode=full\n"
            "allow_other=false\n",
            encoding="utf-8",
        )
        return mountpoint

    def protocol_request(self, operation: str, op_class: str, args: dict | None = None) -> subprocess.CompletedProcess[str]:
        payload = {
            "schema_version": 1,
            "request_id": "python-test",
            "client": {"name": "python-test", "version": "1", "protocol": {"min": 1, "max": 1}},
            "operation": {"name": operation, "class": op_class, "args": args or {}},
        }
        return subprocess.run(
            [str(self.racctl), "rpc"],
            cwd=ROOT,
            env=self.env,
            input=json.dumps(payload),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=True,
        )

    def test_paths_use_persistent_state_outside_module(self) -> None:
        result = self.run_cmd(str(NEXUS), "paths")
        self.assertIn(f"state={self.state}", result.stdout)
        self.assertNotEqual(str(self.state), str(MODULE))

    def test_mount_start_status_stop(self) -> None:
        self.write_mount()
        start = self.run_cmd(str(MOUNTCTL), "start", "drive")
        self.assertIn("started", start.stdout)
        pid_file = self.state / "run/mounts/drive.process.json"
        self.assertTrue(pid_file.is_file())
        record = json.loads(pid_file.read_text())
        self.assertGreater(record["start_ticks"], 0)

        status = self.run_cmd(str(MOUNTCTL), "status", "drive")
        self.assertIn("running", status.stdout)

        stop = self.run_cmd(str(MOUNTCTL), "stop", "drive")
        self.assertIn("stopped", stop.stdout)
        self.assertFalse(pid_file.exists())

    def test_reconcile_starts_only_enabled_mounts(self) -> None:
        self.write_mount("enabled", enabled=True)
        self.write_mount("disabled", enabled=False)
        self.run_cmd(str(MOUNTCTL), "reconcile")
        self.assertTrue((self.state / "run/mounts/enabled.process.json").is_file())
        self.assertFalse((self.state / "run/mounts/disabled.process.json").exists())
        self.run_cmd(str(MOUNTCTL), "stop", "enabled")

    def test_rejects_invalid_mount_name(self) -> None:
        result = self.run_cmd(str(MOUNTCTL), "start", "../escape", check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid mount name", result.stderr)

    def test_capabilities_include_all_operation_classes(self) -> None:
        result = subprocess.run(
            [str(self.racctl), "capabilities"],
            cwd=ROOT,
            env=self.env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=True,
        )
        payload = json.loads(result.stdout)
        self.assertEqual(payload["protocol"], {"min": 1, "max": 1})
        self.assertEqual(
            set(payload["operation_classes"]),
            {"query", "preview", "run", "cancel", "reconcile"},
        )
        operation_names = {item["name"] for item in payload["operations"]}
        self.assertIn("provider.status", operation_names)
        self.assertNotIn("system.exec", operation_names)

    def test_provider_protocol_does_not_expose_private_paths(self) -> None:
        result = self.protocol_request("provider.status", "query")
        self.assertNotIn(str(self.provider), result.stdout)
        self.assertNotIn(str(self.state), result.stdout)
        lines = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        self.assertTrue(lines[-1]["ok"])
        self.assertEqual(lines[-1]["result"]["module_id"], "rclone")

    def test_runtime_directories_are_private(self) -> None:
        self.run_cmd(str(NEXUS), "paths")
        # paths is read-only; a lifecycle operation initializes state.
        self.write_mount()
        self.run_cmd(str(MOUNTCTL), "start", "drive")
        for path in (
            self.state, self.state / "mounts.d", self.state / "run", self.state / "logs", self.state / "cache",
            self.state / "config", self.state / "desired", self.state / "run/mounts", self.state / "run/locks",
        ):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700, path)
        self.run_cmd(str(MOUNTCTL), "stop", "drive")


    def test_config_protocol_migrates_legacy_without_exposing_args_file(self) -> None:
        mountpoint = self.write_mount()
        config = self.state / "mounts.d/drive.conf"
        config.write_text(config.read_text() + "args_file=private/secret.args\n", encoding="utf-8")
        snapshot = self.protocol_request("config.snapshot", "query")
        self.assertNotIn("private/secret.args", snapshot.stdout)
        lines = [json.loads(line) for line in snapshot.stdout.splitlines() if line.strip()]
        result = lines[-1]["result"]
        self.assertEqual(result["source"], "legacy-v0.1")
        self.assertTrue(result["mounts"][0]["has_args_file"])

        candidate = [{
            "name": "drive", "enabled": True, "remote": "fake:", "mountpoint": str(mountpoint),
            "vfs_cache_mode": "full", "allow_other": False, "log_level": "INFO",
        }]
        preview = self.protocol_request("config.preview", "preview", {"mounts": candidate})
        preview_result = [json.loads(line) for line in preview.stdout.splitlines() if line.strip()][-1]["result"]
        apply = self.protocol_request("config.apply", "run", {
            "expected_revision": preview_result["current_revision"],
            "candidate_digest": preview_result["candidate_digest"],
            "mounts": candidate,
        })
        apply_result = [json.loads(line) for line in apply.stdout.splitlines() if line.strip()][-1]
        self.assertTrue(apply_result["ok"])
        self.assertEqual(apply_result["result"]["registry"]["revision"], 1)
        self.assertTrue((self.state / "config/registry-v2.json").is_file())

    def test_daemon_single_instance_and_sigterm_cleanup(self) -> None:
        env = self.env.copy()
        env.pop("RNEXUS_RACD_DISABLE", None)
        first = subprocess.Popen(
            [str(self.racctl), "racd"],
            cwd=ROOT,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        socket = self.state / "run/racd.sock"
        deadline = time.time() + 3
        while not socket.exists() and time.time() < deadline:
            time.sleep(0.02)
        self.assertTrue(socket.exists(), "racd socket did not appear")

        second = subprocess.run(
            [str(self.racctl), "racd"],
            cwd=ROOT,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
            timeout=3,
        )
        self.assertNotEqual(second.returncode, 0)
        self.assertIn("RNX_E_DAEMON_ALREADY_RUNNING", second.stderr)

        first.send_signal(signal.SIGTERM)
        first.wait(timeout=3)
        stderr_text = first.stderr.read() if first.stderr else ""
        if first.stdout:
            first.stdout.close()
        if first.stderr:
            first.stderr.close()
        self.assertEqual(first.returncode, 0, stderr_text)
        self.assertFalse(socket.exists())


if __name__ == "__main__":
    unittest.main()
