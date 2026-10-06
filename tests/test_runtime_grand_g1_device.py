import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location('runtime_grand_g1_device', ROOT/'scripts/dev/runtime_grand_g1_device.py')
MOD = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(MOD)

class RuntimeGrandG1DeviceContractTests(unittest.TestCase):
    def test_exact_device_promise_range(self):
        self.assertEqual(MOD.DEVICE_PROMISES, [f'RNX-P{i:03d}' for i in range(467, 490)])

    def test_policy_points_at_device_harness_and_current_range(self):
        policy = json.loads((ROOT/'release/runtime-grand-g1-policy.json').read_text())
        self.assertEqual(policy['canonical_range'], 'RNX-P001..RNX-P514')
        self.assertEqual(policy['device_harness'], 'scripts/dev/runtime_grand_g1_device.py')
        self.assertEqual(policy['real_device_promises'], MOD.DEVICE_PROMISES)
        self.assertFalse(policy['legacy_grand_g1_may_seal_current_campaign'])

    def test_harness_uses_production_ingress_not_assertion_only_flags(self):
        text = (ROOT/'scripts/dev/runtime_grand_g1_device.py').read_text()
        for token in (
            '["runtime", "source", "resolve", sid]',
            '["runtime", "source", "import-build", str(out)]',
            '["runtime", "activate", b_id]',
            '["runtime", "recover"]',
            '"provider.browse"',
            '"config", "encryption", "set"',
            '"next-reboot"',
            '["migration", "inspect"',
            '["migration", "finalize"',
            'doctor", "--bundle"',
        ):
            self.assertIn(token, text)
        self.assertNotIn('manual_pass', text)
        self.assertNotIn('user_asserted_pass', text)
        self.assertNotIn('.devtool.toml', MOD.BOUND_SOURCE_PATHS)
        self.assertNotIn('release/canonical-promise-ledger.json', MOD.BOUND_SOURCE_PATHS)
        self.assertNotIn('release/runtime-grand-g1-policy.json', MOD.BOUND_SOURCE_PATHS)
        self.assertNotIn('.devtool.toml', MOD.runtime_g1.BOUND_SOURCE_PATHS)
        self.assertNotIn('release/canonical-promise-ledger.json', MOD.runtime_g1.BOUND_SOURCE_PATHS)
        self.assertNotIn('.devtool.toml', MOD.source_g1.BOUND_SOURCE_PATHS)
        self.assertNotIn('release/canonical-promise-ledger.json', MOD.source_g1.BOUND_SOURCE_PATHS)

    def test_ledger_marks_device_cases_partial_not_green(self):
        ledger = json.loads((ROOT/'release/canonical-promise-ledger.json').read_text())
        by = {item['id']: item for item in ledger['items']}
        for pid in MOD.DEVICE_PROMISES:
            self.assertEqual(by[pid]['status'], 'PARTIALLY_ADOPTED')
            self.assertIn('runtime-grand-g1-device-source-audit', by[pid]['evidence'])
        for n in range(490, 501):
            self.assertEqual(by[f'RNX-P{n:03d}']['status'], 'UNIMPLEMENTED')

    def test_private_grand_evidence_is_excluded_from_release_source_digest(self):
        text = (ROOT/'scripts/dev/release_artifacts.py').read_text()
        self.assertIn('release/evidence/runtime-grand-g1-device.json', text)
        self.assertIn('release/evidence/runtime-grand-g1-seal.json', text)


    def test_source_identity_distinguishes_build_backed_from_download_backed(self):
        base = {
            'resolution_id': 'src-' + 'a' * 32, 'source_id': 'bclone',
            'spec_digest': 'b' * 64, 'engine': 'bclone', 'kind': 'github-release',
            'channel': 'latest-stable', 'repository': 'BenjiThatFoxGuy/bclone',
            'repository_id': 1, 'release_id': 2, 'release_tag': 'v1',
            'commit_sha': 'c' * 40, 'build_required': True,
            'build_repository': 'Mikeyphw/rclone-nexus',
        }
        MOD.source_g1.resolution_identity(dict(base))
        bad = dict(base); bad['build_repository'] = ''
        with self.assertRaisesRegex(RuntimeError, 'build_repository'):
            MOD.source_g1.resolution_identity(bad)
        download = dict(base, source_id='rclone', engine='rclone', build_required=False, build_repository='')
        with self.assertRaisesRegex(RuntimeError, 'concrete numeric asset'):
            MOD.source_g1.resolution_identity(download)
        download['asset'] = {'id': 9, 'api_url': 'https://api.github.com/repos/rclone/rclone/releases/assets/9', 'name': 'rclone-linux-arm64.zip'}
        MOD.source_g1.resolution_identity(download)

    def test_composite_capture_reuses_valid_underlying_evidence(self):
        text = (ROOT/'scripts/dev/runtime_grand_g1_device.py').read_text()
        self.assertIn('reusing current RUNTIME-G1 private evidence', text)
        self.assertIn('runtime_g1.validate(runtime_path, resolve_files=True)', text)
        self.assertIn('reusing current SOURCE-G1 private evidence', text)
        self.assertIn('source_g1.verify(source_path, physical=True)', text)
    def test_composite_binds_underlying_private_evidence_and_refreshes_release_mutations(self):
        text = (ROOT/'scripts/dev/runtime_grand_g1_device.py').read_text()
        self.assertIn('underlying {key} evidence changed without composite refresh', text)
        self.assertIn('refresh_underlying_evidence_hashes(path)', text)

    def test_stale_release_device_provenance_is_a_recapture_not_terminal_exit(self):
        original_validate = MOD.release_device.validate
        original_capture = MOD.release_device.capture
        calls = []
        try:
            def stale(*_args, **_kwargs):
                raise SystemExit("qualification provenance is stale or untrusted")
            MOD.release_device.validate = stale
            MOD.release_device.capture = lambda path, mounts: calls.append((path, mounts))
            with tempfile.TemporaryDirectory() as td:
                evidence = Path(td) / "device-qualification.json"
                evidence.write_text("{}")
                MOD.ensure_release_device_baseline(evidence)
                self.assertEqual(calls, [(evidence, [])])
        finally:
            MOD.release_device.validate = original_validate
            MOD.release_device.capture = original_capture

    def test_release_qualification_uses_runtime_g1_source_bound_gate_racctl(self):
        text = (ROOT/'scripts/dev/runtime_grand_g1_device.py').read_text()
        self.assertIn('gate_racctl = runtime_gate_racctl(runtime_data)', text)
        self.assertIn('os.environ["RNEXUS_RACCTL"] = gate_racctl', text)
        self.assertIn('runtime_g1.root_hash(path).lower() != expected', text)
        self.assertEqual(MOD.HARNESS_VERSION, 8)

    def test_encrypted_config_password_fixture_uses_real_lines_and_android_shell(self):
        script, env = MOD.encrypted_config_password_fixture(
            "/data/adb/rclone-nexus/qualification/g1/config-pass.sh",
            "RNEXUS-GRAND-G1-CONFIG-PASS-test",
        )
        self.assertTrue(script.startswith("#!/system/bin/sh\nprintf "))
        self.assertTrue(script.endswith("\n"))
        self.assertNotIn("#!/system/bin/sh\\n", script)
        self.assertIn("RNEXUS-GRAND-G1-CONFIG-PASS-test", script)
        self.assertEqual(
            env["RCLONE_PASSWORD_COMMAND"],
            "/system/bin/sh /data/adb/rclone-nexus/qualification/g1/config-pass.sh",
        )

    def test_encrypted_config_accepts_rclone_comment_header_but_rejects_plaintext(self):
        encrypted = "# Encrypted rclone configuration File\n\nRCLONE_ENCRYPT_V0:\nZmFrZQ==\n"
        self.assertTrue(MOD.encrypted_config_is_ciphertext(encrypted))
        self.assertFalse(MOD.encrypted_config_is_ciphertext("[grandlocal]\ntype = local\n"))
        self.assertFalse(MOD.encrypted_config_is_ciphertext(encrypted + "[grandlocal]\ntype = local\n"))

    def test_configured_remote_names_skips_invalid_and_deduplicates(self):
        mounts = [
            {"remote": "offline:path"}, {"remote": "healthy:other"},
            {"remote": "offline:again"}, {"remote": ""}, {"remote": "invalid"},
        ]
        self.assertEqual(MOD.configured_remote_names(mounts), ["offline", "healthy"])

    def test_update_policy_restore_is_exact_and_has_no_fallback_defaults(self):
        policy = {
            "source_id": "custom", "check_automatically": False,
            "acquire_automatically": True, "qualify_automatically": True,
            "stage_automatically": False, "activation_mode": "explicit",
            "restart_active_mounts_automatically": False,
            "check_interval_minutes": 777, "retain_history": 9,
        }
        argv = MOD.runtime_update_policy_set_argv("/gate/racctl", policy)
        self.assertIn("777", argv)
        self.assertIn("9", argv)
        self.assertEqual(argv[argv.index("--source") + 1], "custom")
        with self.assertRaisesRegex(RuntimeError, "missing fields"):
            MOD.runtime_update_policy_set_argv("/gate/racctl", {"source_id": "custom"})

    def test_staged_reboot_cleanup_surfaces_source_remove_failure(self):
        policy = {
            "source_id": "original", "check_automatically": True,
            "acquire_automatically": True, "qualify_automatically": True,
            "stage_automatically": True, "activation_mode": "next-reboot",
            "restart_active_mounts_automatically": False,
            "check_interval_minutes": 360, "retain_history": 2,
        }
        original = MOD.runtime_g1.root_run
        calls = []
        class CP:
            def __init__(self, rc=0, out="", err=""):
                self.returncode = rc; self.stdout = out; self.stderr = err
        try:
            def fake(argv, **kwargs):
                calls.append(argv)
                if argv[1:4] == ["runtime", "source", "remove"]:
                    return CP(7, err="refused")
                return CP(0)
            MOD.runtime_g1.root_run = fake
            errors = MOD.restore_staged_reboot_control_state("/gate/racctl", policy, "grand-g1-temp")
            self.assertEqual(len(errors), 1)
            self.assertIn("refused", errors[0])
            self.assertTrue(any(argv[1:4] == ["runtime", "source", "remove"] for argv in calls))
        finally:
            MOD.runtime_g1.root_run = original

    def test_downstream_journeys_keep_source_bound_authority_and_android_shell_fixtures(self):
        text = (ROOT / 'scripts/dev/runtime_grand_g1_device.py').read_text()
        self.assertIn('binary = composite_runtime_gate_racctl(data)', text)
        self.assertIn('["cp", managed_runtime, provider_rclone]', text)
        self.assertIn('shlex.join(["/system/bin/sh", *argv])', text)
        self.assertIn('"source_id": source_id', text)
        self.assertIn('os.urandom(16)', text)
        self.assertIn('restore_staged_reboot_control_state(binary, old_policy, source_id)', text)


    def test_migration_isolated_state_inherits_verified_managed_fuse_helper_authority(self):
        original_text = MOD.runtime_g1.root_text
        original_hash = MOD.runtime_g1.root_hash
        original_run = MOD.runtime_g1.root_run
        calls = []
        helper_sha = "a" * 64
        manifest = {
            "schema_version": 1,
            "repository": "NewFuture/rclone-fuse3-magisk",
            "release_id": 1,
            "release_tag": "v1",
            "asset_id": 2,
            "asset_name": "magisk-rclone_arm64-v8a.zip",
            "archive_sha256": "b" * 64,
            "helper_sha256": helper_sha,
        }
        class CP:
            returncode = 0
            stdout = ""
            stderr = ""
        try:
            MOD.runtime_g1.root_text = lambda path: json.dumps(manifest)
            MOD.runtime_g1.root_hash = lambda path: helper_sha
            MOD.runtime_g1.root_run = lambda argv, **kwargs: (calls.append(argv) or CP())
            result = MOD.mirror_qualified_fuse_helper("/outer", "/isolated")
            self.assertEqual(result["helper_sha256"], helper_sha)
            self.assertIn(
                ["cp", "/outer/runtime/helpers/fusermount3/current/fusermount3",
                 "/isolated/runtime/helpers/fusermount3/current/fusermount3"],
                calls,
            )
            self.assertIn(
                ["cp", "/outer/runtime/helpers/fusermount3/current-v1.json",
                 "/isolated/runtime/helpers/fusermount3/current-v1.json"],
                calls,
            )
        finally:
            MOD.runtime_g1.root_text = original_text
            MOD.runtime_g1.root_hash = original_hash
            MOD.runtime_g1.root_run = original_run

    def test_migration_helper_copy_rejects_manifest_byte_mismatch(self):
        original_text = MOD.runtime_g1.root_text
        original_hash = MOD.runtime_g1.root_hash
        try:
            MOD.runtime_g1.root_text = lambda path: json.dumps({
                "helper_sha256": "a" * 64,
                "repository": "NewFuture/rclone-fuse3-magisk",
            })
            MOD.runtime_g1.root_hash = lambda path: "b" * 64
            with self.assertRaisesRegex(RuntimeError, "do not match their manifest"):
                MOD.mirror_qualified_fuse_helper("/outer", "/isolated")
        finally:
            MOD.runtime_g1.root_text = original_text
            MOD.runtime_g1.root_hash = original_hash

    def test_grand_automatic_journeys_use_providerless_gate_module(self):
        text = (ROOT / 'scripts/dev/runtime_grand_g1_device.py').read_text()
        self.assertNotIn('discover_fuse_helper', text)
        self.assertIn('module_dir, binary = runtime_g1.copy_gate_module(root_dir, built_racctl)', text)

    def test_policy_binds_provider_independent_newfuture_fuse_helper(self):
        policy = json.loads((ROOT / "release/runtime-grand-g1-policy.json").read_text())
        helper = policy.get("fuse_helper_authority", {})
        self.assertEqual(helper.get("repository"), "NewFuture/rclone-fuse3-magisk")
        self.assertEqual(helper.get("asset_name"), "magisk-rclone_arm64-v8a.zip")
        self.assertIs(helper.get("provider_independent"), True)

    def test_runtime_gate_racctl_rejects_missing_or_stale_reference(self):
        original = MOD.runtime_g1.root_hash
        try:
            MOD.runtime_g1.root_hash = lambda path: 'a' * 64
            data = {'evidence': [{'kind': 'gate-racctl', 'path': '/data/adb/test/racctl', 'sha256': 'a' * 64}]}
            self.assertEqual(MOD.runtime_gate_racctl(data), '/data/adb/test/racctl')
            MOD.runtime_g1.root_hash = lambda path: 'b' * 64
            with self.assertRaisesRegex(RuntimeError, 'stale'):
                MOD.runtime_gate_racctl(data)
            with self.assertRaisesRegex(RuntimeError, 'does not contain'):
                MOD.runtime_gate_racctl({'evidence': []})
        finally:
            MOD.runtime_g1.root_hash = original

if __name__ == '__main__':
    unittest.main()

