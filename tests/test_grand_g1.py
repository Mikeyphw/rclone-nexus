from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import shutil
import sys
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

def load(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module

grand = load("grand_g1_gate", ROOT / "scripts/dev/grand_g1_gate.py")
fixtures = load("qualification_fixtures", ROOT / "tests/test_release_qualification.py")
device = load("release_device_qualification", ROOT / "scripts/dev/release_device_qualification.py")


class GrandG1Tests(unittest.TestCase):
    def test_policy_exactly_covers_roadmap_and_final_workflow(self):
        policy, promises, closure, requirement_count = grand.validate_policy()
        self.assertEqual(policy["final_node"], "grand-g1-audit")
        self.assertEqual(len(promises), 24)
        self.assertEqual(requirement_count, 229)
        self.assertTrue(set(policy["required_ancestor_nodes"]).issubset(closure))
        self.assertIn("device-evidence", closure)
        self.assertIn("release-contract", closure)
        self.assertIn("webui-final-contract", closure)

    def test_grand_gate_uses_executable_v3_device_authority(self):
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "device-qualification.json"
            path.write_text(json.dumps(fixtures.complete_evidence()), encoding="utf-8")
            path.chmod(0o600)
            self.assertEqual(grand.validate_device_evidence(path), {"pass": 12, "skip": 0})

    def test_grand_gate_rejects_assertion_only_or_legacy_evidence(self):
        mutations = []
        legacy = fixtures.complete_evidence(); legacy["schema_version"] = 1; mutations.append(legacy)
        assertion = fixtures.complete_evidence(); assertion["endurance_cases"]["reboot"] = {"status": "pass", "note": "trust me"}; mutations.append(assertion)
        for data in mutations:
            with self.subTest(schema=data.get("schema_version")), tempfile.TemporaryDirectory() as td:
                path = Path(td) / "device-qualification.json"
                path.write_text(json.dumps(data), encoding="utf-8"); path.chmod(0o600)
                with self.assertRaises(SystemExit): grand.validate_device_evidence(path)

    def test_release_evidence_default_follows_devtool_primary_checkout(self):
        old = os.environ.get("DEVTOOL_TRANSACTION_PRIMARY_REPO_ROOT")
        try:
            with tempfile.TemporaryDirectory() as td:
                os.environ["DEVTOOL_TRANSACTION_PRIMARY_REPO_ROOT"] = td
                self.assertEqual(device.default_evidence_path(), Path(td).resolve() / "release/evidence/device-qualification.json")
        finally:
            if old is None: os.environ.pop("DEVTOOL_TRANSACTION_PRIMARY_REPO_ROOT", None)
            else: os.environ["DEVTOOL_TRANSACTION_PRIMARY_REPO_ROOT"] = old

    def test_source_readiness_path_does_not_require_private_device_evidence(self):
        policy, promises, closure, requirement_count = grand.validate_policy()
        grand.validate_docs()
        self.assertEqual(len(promises), 24)
        self.assertEqual(requirement_count, 229)
        text = (ROOT / ".devtool.toml").read_text(encoding="utf-8")
        self.assertIn('grand-g1-source = [', text)
        self.assertIn('command = ["python3", "scripts/dev/grand_g1_gate.py", "--source-only"]', text)
        # Source/readiness qualification must install before private real-device evidence exists.
        source_block = text.split('grand-g1-source = [', 1)[1].split(']\n\nrelease = [', 1)[0]
        self.assertNotIn('release-device-validate', source_block)
        self.assertNotIn('device-evidence', source_block)
        self.assertIn('grand-g1-source-audit', source_block)
        self.assertIn('validation-output-cleanup', source_block)
        self.assertGreater(source_block.index('validation-output-cleanup'), source_block.index('grand-g1-source-audit'))
        # The authoritative seal remains fail-closed on the real device evidence.
        release_block = text.split('release = [', 1)[1].split(']\n', 1)[0]
        self.assertIn('device-evidence', release_block)
        self.assertIn('grand-g1-audit', release_block)

    def test_validation_cleanup_restores_tracked_and_removes_untracked_generated_outputs(self):
        script = ROOT / "scripts/dev/cleanup_validation_outputs.py"
        with tempfile.TemporaryDirectory() as td:
            repo = Path(td)
            subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
            subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=repo, check=True)
            subprocess.run(["git", "config", "user.name", "test"], cwd=repo, check=True)
            tracked = repo / "build/android/arm64-v8a/racctl"
            tracked.parent.mkdir(parents=True)
            tracked.write_bytes(b"baseline")
            (repo / "dist").mkdir()
            subprocess.run(["git", "add", "build/android/arm64-v8a/racctl"], cwd=repo, check=True)
            subprocess.run(["git", "commit", "-qm", "baseline"], cwd=repo, check=True)
            tracked.write_bytes(b"validation-output")
            for rel in ("dist/rclone-nexus-v0.1.0.zip", "dist/SHA256SUMS", "dist/release-manifest.json"):
                p = repo / rel; p.write_text("generated", encoding="utf-8")
            target = repo / "scripts/dev/cleanup_validation_outputs.py"
            target.parent.mkdir(parents=True)
            shutil.copy2(script, target)
            subprocess.run([sys.executable, str(target)], cwd=repo, check=True)
            self.assertEqual(tracked.read_bytes(), b"baseline")
            for rel in ("dist/rclone-nexus-v0.1.0.zip", "dist/SHA256SUMS", "dist/release-manifest.json"):
                self.assertFalse((repo / rel).exists())
            status = subprocess.check_output(
                ["git", "status", "--porcelain=v1", "--", "build/android/arm64-v8a/racctl", "dist/rclone-nexus-v0.1.0.zip", "dist/SHA256SUMS", "dist/release-manifest.json"],
                cwd=repo,
                text=True,
            ).strip()
            self.assertEqual(status, "")

    def test_policy_and_requirement_matrix_fail_closed_when_incomplete(self):
        original_policy, original_req, original_roadmap = grand.POLICY, grand.REQUIREMENTS, grand.ROADMAP
        try:
            with tempfile.TemporaryDirectory() as td:
                td = Path(td)
                policy = json.loads(original_policy.read_text())
                policy["promises"] = policy["promises"][:-1]
                policy_path = td / "policy.json"; policy_path.write_text(json.dumps(policy))
                grand.POLICY = policy_path
                with self.assertRaises(SystemExit): grand.validate_policy()
                grand.POLICY = original_policy

                req = json.loads(original_req.read_text())
                req["requirements"] = req["requirements"][:-1]
                req_path = td / "req.json"; req_path.write_text(json.dumps(req))
                grand.REQUIREMENTS = req_path
                with self.assertRaises(SystemExit): grand.validate_policy()
                grand.REQUIREMENTS = original_req

                roadmap = original_roadmap.read_text().replace("- reboot and root-manager restart;", "- reboot only;")
                roadmap_path = td / "ROADMAP.md"; roadmap_path.write_text(roadmap)
                grand.ROADMAP = roadmap_path
                with self.assertRaises(SystemExit): grand.validate_policy()
        finally:
            grand.POLICY, grand.REQUIREMENTS, grand.ROADMAP = original_policy, original_req, original_roadmap


if __name__ == "__main__":
    unittest.main()
