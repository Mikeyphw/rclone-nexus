import importlib.util
import inspect
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "runtime_grand_g1_device",
    ROOT / "scripts/dev/runtime_grand_g1_device.py",
)
MOD = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(MOD)


class GrandG1WatcherLaunchContractTests(unittest.TestCase):
    def test_failed_activation_watcher_backgrounds_entire_body(self):
        source = inspect.getsource(MOD.failed_activation_rollback_probe)
        self.assertIn(
            'watcher_command = "(\\n" + watcher + "\\n) >/dev/null 2>&1 & echo $!"',
            source,
        )
        self.assertNotIn(
            'watcher + " >/dev/null 2>&1 & echo $!"',
            source,
        )
        self.assertIn("watcher_pid_text.isdigit()", source)

    def test_watcher_body_keeps_timeout_sentinel_inside_background_group(self):
        source = inspect.getsource(MOD.failed_activation_rollback_probe)
        self.assertIn("exit 71", source)
        self.assertIn("watcher_command", source)


if __name__ == "__main__":
    unittest.main()
