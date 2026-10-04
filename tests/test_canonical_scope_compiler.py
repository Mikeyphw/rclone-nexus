from __future__ import annotations
import copy, importlib.util, json, tempfile, unittest
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
SPEC=importlib.util.spec_from_file_location('check_canonical_scope',ROOT/'scripts/dev/check_canonical_scope.py')
assert SPEC and SPEC.loader
scope=importlib.util.module_from_spec(SPEC); SPEC.loader.exec_module(scope)

class CanonicalScopeCompilerTests(unittest.TestCase):
    def test_current_scope_is_contiguous_and_tracks_nonbullet_obligations(self):
        data=json.loads((ROOT/'release/canonical-promise-ledger.json').read_text())
        disp=json.loads((ROOT/'release/canonical-roadmap-obligation-dispositions.json').read_text())
        result=scope.validate_scope(data,disp)
        self.assertEqual(result['maximum'],514)
        self.assertEqual(result['direct_bullets'],222)
        self.assertEqual(result['supplemental_promises'],14)
        self.assertGreaterEqual(result['supplemental_clauses'],40)

    def test_numbered_migration_and_postseal_steps_are_compiled(self):
        _, clauses=scope.extract_runtime_roadmap()
        texts={x['text'] for x in clauses if x['kind']=='numbered'}
        self.assertIn('2. validate/import `rclone.conf` into Nexus durable config;',texts)
        self.assertIn('10. report old module as no longer required, but do not silently uninstall it.',texts)
        self.assertIn('3. run authoritative `./devtoolw release` seal;',texts)

    def test_new_unclassified_prose_changes_inventory(self):
        original=(ROOT/'docs/campaign/RUNTIME_STANDALONE_ROADMAP.md').read_text()
        marker='### CLI/API parity\n'
        mutated=original.replace(marker,marker+'This newly normative sentence must not escape canonical disposition.\n',1)
        with tempfile.TemporaryDirectory() as raw:
            p=Path(raw)/'roadmap.md'; p.write_text(mutated)
            _,clauses=scope.extract_runtime_roadmap(p)
        self.assertTrue(any(x['text']=='This newly normative sentence must not escape canonical disposition.' for x in clauses))


    def test_missing_disposition_fails_closed(self):
        data=json.loads((ROOT/'release/canonical-promise-ledger.json').read_text())
        disp=json.loads((ROOT/'release/canonical-roadmap-obligation-dispositions.json').read_text())
        broken=copy.deepcopy(disp); broken['supplemental_clauses'].pop()
        with self.assertRaises(SystemExit):
            scope.validate_scope(data,broken)

    def test_numbered_step_cannot_be_downgraded_to_guidance(self):
        data=json.loads((ROOT/'release/canonical-promise-ledger.json').read_text())
        disp=json.loads((ROOT/'release/canonical-roadmap-obligation-dispositions.json').read_text())
        broken=copy.deepcopy(disp)
        clause=next(x for x in broken['supplemental_clauses'] if x['kind']=='numbered')
        clause['classification']='guidance'; clause['promise_ids']=[]
        with self.assertRaises(SystemExit):
            scope.validate_scope(data,broken)

    def test_new_promise_ids_are_exactly_the_supplemental_closure(self):
        disp=json.loads((ROOT/'release/canonical-roadmap-obligation-dispositions.json').read_text())
        ids=[pid for c in disp['supplemental_clauses'] if c['classification']=='new-promise' for pid in c['promise_ids']]
        self.assertEqual(ids,[f'RNX-P{i:03d}' for i in range(501,515)])

if __name__=='__main__': unittest.main()
