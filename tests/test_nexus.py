from __future__ import annotations

import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
MODULE = ROOT / "module"
MOUNTCTL = MODULE / "system/bin/rclone-mountctl"
NEXUS = MODULE / "system/bin/rclone-nexus"


class NexusTests(unittest.TestCase):
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
                "RNEXUS_START_GRACE_SECONDS": "0",
            }
        )

    def tearDown(self) -> None:
        run = self.state / "run"
        if run.exists():
            for pid_file in run.glob("*.pid"):
                try:
                    pid = int(pid_file.read_text().strip())
                    os.kill(pid, signal.SIGKILL)
                except (OSError, ValueError):
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

    def test_paths_use_persistent_state_outside_module(self) -> None:
        result = self.run_cmd(str(NEXUS), "paths")
        self.assertIn(f"state={self.state}", result.stdout)
        self.assertNotEqual(str(self.state), str(MODULE))

    def test_mount_start_status_stop(self) -> None:
        self.write_mount()
        start = self.run_cmd(str(MOUNTCTL), "start", "drive")
        self.assertIn("started", start.stdout)
        pid_file = self.state / "run/drive.pid"
        self.assertTrue(pid_file.is_file())

        status = self.run_cmd(str(MOUNTCTL), "status", "drive")
        self.assertIn("running", status.stdout)

        stop = self.run_cmd(str(MOUNTCTL), "stop", "drive")
        self.assertIn("stopped", stop.stdout)
        self.assertFalse(pid_file.exists())

    def test_reconcile_starts_only_enabled_mounts(self) -> None:
        self.write_mount("enabled", enabled=True)
        self.write_mount("disabled", enabled=False)
        self.run_cmd(str(MOUNTCTL), "reconcile")
        self.assertTrue((self.state / "run/enabled.pid").is_file())
        self.assertFalse((self.state / "run/disabled.pid").exists())
        self.run_cmd(str(MOUNTCTL), "stop", "enabled")

    def test_rejects_invalid_mount_name(self) -> None:
        result = self.run_cmd(str(MOUNTCTL), "start", "../escape", check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid mount name", result.stderr)


if __name__ == "__main__":
    unittest.main()
