from __future__ import annotations

import contextlib
import importlib.util
import io
from pathlib import Path
import sys
import unittest

ROOT = Path(__file__).resolve().parents[1]
DEV = ROOT / "scripts" / "dev"
sys.path.insert(0, str(DEV))
import device_qualification_feedback as feedback


class DeviceQualificationFeedbackTests(unittest.TestCase):
    def test_structured_failure_explains_why_expected_observed_and_next(self):
        exc = feedback.QualificationFailure(
            "namespace visibility evidence is malformed for mount 'safegdrive'",
            why="RNX-P482 requires namespace visibility",
            expected="claim + visibility[]",
            observed="keys=['claim']",
            command_text="racctl namespace inspect safegdrive",
            evidence="release/evidence/device-qualification.json",
            promise="RNX-P482",
            next_action="rerun namespace inspect",
        )
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            feedback.render_failure(exc)
        text = buf.getvalue()
        for token in ("Problem:", "Blocks: RNX-P482", "Why this matters:", "Expected:", "Observed:", "Command:", "Evidence:", "Next:"):
            self.assertIn(token, text)

    def test_default_step_includes_why_without_verbose_mode(self):
        feedback.set_verbose(False)
        buf = io.StringIO()
        with contextlib.redirect_stderr(buf):
            feedback.step("SOURCE-G1", "resolve sources", "Prove immutable upstream identity")
        text = buf.getvalue()
        self.assertIn("SOURCE-G1", text)
        self.assertIn("Why: Prove immutable upstream identity", text)


if __name__ == "__main__":
    unittest.main()
