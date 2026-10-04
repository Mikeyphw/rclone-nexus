from __future__ import annotations

import importlib.util
import json
import subprocess
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/dev/runtime_standalone_g1_device.py"
spec = importlib.util.spec_from_file_location("runtime_g1_device", SCRIPT)
assert spec and spec.loader
g = importlib.util.module_from_spec(spec); spec.loader.exec_module(g)


class RuntimeG1EvidenceTests(unittest.TestCase):
    def fixture(self) -> dict:
        a = "rclone-a"
        b = "rclone-b"
        digest = "a" * 64
        kinds = [
            "gate-racctl", "runtime-binary-a", "runtime-manifest-a", "runtime-binary-b",
            "runtime-manifest-b", "linux-arm64-probe-binary", "linux-arm64-probe-manifest",
            "activation-state", "mount-config", "desired-state", "boot-health", "transaction-receipt", "fuse-helper", "fuse-helper-manifest",
        ]
        data = {
            "schema_version": 1,
            "harness": "scripts/dev/runtime_standalone_g1_device.py",
            "harness_version": g.HARNESS_VERSION,
            "captured_at": "2026-10-03T00:00:00+00:00",
            "session_id": "fixture",
            "status": "PASS",
            "device": {"sdk": "36", "release": "16", "fingerprint_sha256": "f" * 64},
            "source": {
                "parent_head": "fixture-base",
                "bindings": g.source_bindings(),
                "source_digest": g.digest(g.source_bindings()),
                "built_racctl_sha256": "b" * 64,
                "fuse_helper_sha256": "e" * 64,
                "fuse_helper_repository": "NewFuture/rclone-fuse3-magisk",
                "fuse_helper_release_tag": "vfixture",
                "fuse_helper_asset_id": 123,
                "fuse_helper_archive_sha256": "9" * 64,
            },
            "candidate": {"source_sha256": digest, "runtime_a": a, "runtime_b": b, "binary_sha256": digest, "binary_a_sha256": digest, "binary_b_sha256": "d" * 64},
            "authority": {
                "provider_absent_during_operation": True,
                "path_provider_poison_ignored": True,
                "active_projection_poison_ignored": True,
                "cli_runtime_id": a, "boot_runtime_id": a, "daemon_runtime_id": a, "webui_runtime_id": a,
            },
            "flow": {
                "activate_a": {"runtime_id": a, "transaction_id": "t1", "mount_pid": 100, "process_sha256": digest},
                "activate_b": {"runtime_id": b, "transaction_id": "t2", "mount_pid": 101, "process_sha256": "d" * 64},
                "rollback": {"runtime_id": a, "transaction_id": "t3", "mount_pid": 102, "process_sha256": digest},
                "boot_restart": {"runtime_id": a, "mount_pid": 103, "process_sha256": digest},
                "boot_recovery_phase": "ACTIVE",
                "boot_health_state": "RUNNING",
                "boot_desired_state": "running",
                "boot_policy_network_mode": "offline-allowed",
                "boot_policy_allowed": True,
                "synthetic_qualification_rejected": True,
                "synthetic_runtime_id": "invalid",
                "linux_arm64_android_fuse_rejected": True,
                "linux_arm64_runtime_id": "linux-probe",
            },
            "evidence": [
                {
                    "kind": kind,
                    "path": f"/fixture/evidence/snapshot/{kind}" if kind in {
                        "activation-state", "mount-config", "desired-state", "boot-health",
                    } else f"/fixture/{kind}",
                    "source_path": f"/fixture/state/{kind}" if kind in {
                        "activation-state", "mount-config", "desired-state", "boot-health",
                    } else None,
                    "sha256": "c" * 64,
                }
                for kind in kinds
            ],
            "workspace": "/fixture",
        }
        for ref in data["evidence"]:
            if ref.get("source_path") is None:
                ref.pop("source_path", None)
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        return data

    def validate(self, data: dict):
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "evidence.json"
            path.write_text(json.dumps(data), encoding="utf-8")
            return g.validate(path, resolve_files=False)

    def test_structural_evidence_passes_without_physical_resolution(self):
        self.assertEqual(self.validate(self.fixture())["status"], "PASS")

    def test_document_digest_tamper_is_rejected(self):
        data = self.fixture(); data["flow"]["activate_a"]["mount_pid"] = 999
        with self.assertRaises(RuntimeError): self.validate(data)

    def test_no_real_process_is_rejected_even_with_recomputed_digest(self):
        data = self.fixture(); data["flow"]["activate_a"]["mount_pid"] = 0
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError): self.validate(data)

    def test_source_binding_change_is_rejected(self):
        data = self.fixture(); data["source"]["bindings"]["cmd/racctl/main.go"] = "0" * 64
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError): self.validate(data)

    def test_ingress_identity_divergence_is_rejected(self):
        data = self.fixture(); data["authority"]["webui_runtime_id"] = "other"
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError): self.validate(data)

    def test_candidate_b_must_bind_distinct_bytes(self):
        data = self.fixture(); data["candidate"]["binary_b_sha256"] = data["candidate"]["binary_a_sha256"]
        data["flow"]["activate_b"]["process_sha256"] = data["candidate"]["binary_a_sha256"]
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError): self.validate(data)

    def test_linux_arm64_execute_without_fuse_rejection_is_mandatory(self):
        data = self.fixture(); data["flow"]["linux_arm64_android_fuse_rejected"] = False
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError): self.validate(data)


    def test_gate_mount_fixture_is_offline_allowed(self):
        config = g.g1_mount_definition("/data/local/source", "/data/local/mount")
        self.assertIn("network_mode=offline-allowed\n", config)
        self.assertIn("require_network=false\n", config)
        self.assertNotIn("network_mode=any\n", config)

    def test_boot_policy_binding_is_mandatory(self):
        data = self.fixture(); data["flow"]["boot_policy_allowed"] = False
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError): self.validate(data)

    def test_fuse_helper_identity_is_required(self):
        data = self.fixture(); data["source"].pop("fuse_helper_sha256")
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError): self.validate(data)

    def test_fuse_helper_must_be_newfuture_bound(self):
        data = self.fixture(); data["source"]["fuse_helper_repository"] = "example/other"
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError): self.validate(data)

    def test_mutable_live_state_reference_is_rejected(self):
        data = self.fixture()
        desired = next(ref for ref in data["evidence"] if ref["kind"] == "desired-state")
        desired["path"] = "/fixture/state/desired/runtime-g1.json"
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError):
            self.validate(data)

    def test_mutable_snapshot_requires_source_path(self):
        data = self.fixture()
        health = next(ref for ref in data["evidence"] if ref["kind"] == "boot-health")
        health.pop("source_path", None)
        data["evidence_digest"] = g.digest({k: v for k, v in data.items() if k != "evidence_digest"})
        with self.assertRaises(RuntimeError):
            self.validate(data)


    def test_capture_cleanliness_accepts_exact_overlay_owned_cleanup_script(self):
        status = " M scripts/dev/cleanup_validation_outputs.py\n?? docs/implementation/RUNTIME-STANDALONE-G1.md\n"
        self.assertEqual(g.unexpected_capture_dirty_paths(status), [])

    def test_capture_cleanliness_rejects_unrelated_bound_source_edit(self):
        # cmd/racctl/main.go is evidence-bound but is not modified by the G1
        # overlay; a pre-existing edit must not be silently blessed by capture.
        status = " M cmd/racctl/main.go\n"
        self.assertEqual(g.unexpected_capture_dirty_paths(status), ["cmd/racctl/main.go"])

    def test_capture_cleanliness_rejects_arbitrary_untracked_file(self):
        self.assertEqual(g.unexpected_capture_dirty_paths("?? local-notes.txt\n"), ["local-notes.txt"])

    def test_private_runtime_g1_evidence_is_not_tracked(self):
        proc = subprocess.run(
            ["git", "ls-files", "--error-unmatch", "--", "release/evidence/runtime-g1-device-qualification.json"],
            cwd=ROOT,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
        self.assertIn(proc.returncode, (0, 1), proc.stderr)
        self.assertEqual(proc.returncode, 1, "private RUNTIME-G1 evidence must never be tracked")


if __name__ == "__main__":
    unittest.main()
