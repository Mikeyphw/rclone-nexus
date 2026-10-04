import importlib.util
import json
from pathlib import Path
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

    def test_composite_binds_underlying_private_evidence_and_refreshes_release_mutations(self):
        text = (ROOT/'scripts/dev/runtime_grand_g1_device.py').read_text()
        self.assertIn('underlying {key} evidence changed without composite refresh', text)
        self.assertIn('refresh_underlying_evidence_hashes(path)', text)

if __name__ == '__main__':
    unittest.main()
