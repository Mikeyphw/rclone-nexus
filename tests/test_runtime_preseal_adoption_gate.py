from __future__ import annotations
import importlib.util, json
from pathlib import Path
import unittest
ROOT=Path(__file__).resolve().parents[1]
def load():
    p=ROOT/'scripts/dev/runtime_preseal_adoption_gate.py'; spec=importlib.util.spec_from_file_location('preseal',p); assert spec and spec.loader
    m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m); return m
preseal=load()
class RuntimePresealAdoptionTests(unittest.TestCase):
    def test_legacy_adoption_is_honestly_split(self):
        counts=preseal.assert_adoption()
        self.assertEqual(counts,{'source_requalified':146,'legacy_device_pending':51,'runtime_grand_g1_device_partial':23,'legacy_final_pending':1,'runtime_grand_g1_unimplemented':11})
    def test_current_final_policy_displaces_legacy_gate(self):
        preseal.assert_current_final_policy()
    def test_docs_and_source_build_dependencies_are_current(self):
        preseal.assert_docs(); preseal.assert_ci_immutability()
if __name__=='__main__': unittest.main()
