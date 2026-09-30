from __future__ import annotations

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "dev" / "release_device_qualification.py"
CASES = [
    "reboot", "root_manager_restart", "provider_update_reload", "wifi_mobile_offline",
    "doze_screenoff_charging", "remote_outage_auth_recovery", "stale_fuse_killed_rclone",
    "daemon_crash_restart", "storage_remount_low_space", "webui_reopen_idle_expiry",
    "android_user_namespace_change", "simultaneous_mounts_jobs",
]


class ReleaseQualificationTests(unittest.TestCase):
    def evidence(self, status: str = "pass") -> dict:
        return {
            "schema_version": 1,
            "campaign_position": "REL-X01",
            "captured_at": "2026-09-30T00:00:00+00:00",
            "device": {"sdk": "36", "fingerprint": "example/device/build"},
            "nexus": {"version": "v0.1.0", "protocol": {"min": 1, "max": 1}},
            "root_manager": {"schema_version": 1, "kind": "kernelsu", "compatible": True},
            "provider": {
                "module_id": "rclone", "ready": True, "module_ready": True,
                "binary_ready": True, "fuse_device_ready": True, "fuse_helper_ready": True,
                "config_ready": True, "rclone_version": "rclone v1.75.1",
            },
            "doctor": {"schema_version": 1, "overall": "PASS", "checks": []},
            "namespace_visibility": {},
            "endurance_cases": {name: {"status": status, "note": "fixture"} for name in CASES},
        }

    def run_validate(self, data: dict, complete: bool = True) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "evidence.json"
            path.write_text(json.dumps(data))
            argv = [sys.executable, str(SCRIPT), "--file", str(path), "validate"]
            if complete:
                argv.append("--require-complete")
            return subprocess.run(argv, cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def test_complete_device_evidence_passes(self):
        result = self.run_validate(self.evidence())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('"complete": true', result.stdout)

    def test_pending_failed_and_unexplained_skip_fail_closed(self):
        for status in ("pending", "fail"):
            data = self.evidence()
            data["endurance_cases"]["reboot"] = {"status": status, "note": "fixture"}
            result = self.run_validate(data)
            self.assertNotEqual(result.returncode, 0, status)
        data = self.evidence()
        data["endurance_cases"]["android_user_namespace_change"] = {"status": "skip", "note": ""}
        result = self.run_validate(data)
        self.assertNotEqual(result.returncode, 0)

    def test_identity_provider_root_and_doctor_evidence_fail_closed(self):
        cases = [
            ("nexus", None),
            ("root_manager", {"kind": "unknown", "compatible": False}),
            ("provider", {"module_id": "rclone", "ready": False}),
            ("doctor", {"overall": "FAIL", "checks": []}),
        ]
        for key, value in cases:
            with self.subTest(key=key):
                data = self.evidence()
                if value is None:
                    data.pop(key)
                else:
                    data[key] = value
                result = self.run_validate(data)
                self.assertNotEqual(result.returncode, 0)

    def test_unknown_endurance_case_is_rejected(self):
        data = self.evidence()
        data["endurance_cases"]["invented_case"] = {"status": "pass", "note": "no"}
        result = self.run_validate(data)
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
