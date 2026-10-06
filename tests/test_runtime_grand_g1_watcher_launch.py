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
    def test_failed_activation_watcher_is_concurrent_popen_not_sync_root_run(self):
        source = inspect.getsource(MOD.failed_activation_rollback_probe)
        self.assertIn("subprocess.Popen(", source)
        self.assertIn("watcher_pidfile", source)
        self.assertIn("failed-activation watcher ready", source)
        self.assertNotIn(
            'watcher_command = "(\\n" + watcher + "\\n) >/dev/null 2>&1 & echo $!"',
            source,
        )

    def test_activation_begins_after_watcher_readiness_not_completion(self):
        source = inspect.getsource(MOD.failed_activation_rollback_probe)
        ready = source.index('"failed-activation watcher ready"')
        activate = source.index('["runtime", "activate", candidate_id]')
        communicate = source.index("watcher_proc.communicate(timeout=35)")
        self.assertLess(ready, activate)
        self.assertLess(activate, communicate)

    def test_cleanup_never_kills_privileged_su_via_popen(self):
        source = inspect.getsource(MOD.failed_activation_rollback_probe)
        self.assertNotIn("watcher_proc.kill()", source)
        self.assertNotIn("watcher_proc.terminate()", source)
        self.assertIn('["kill", "-9", str(watcher_pid)]', source)
        self.assertIn('["rm", "-f", watcher_pidfile]', source)

    def test_watcher_retains_bounded_timeout_sentinel(self):
        source = inspect.getsource(MOD.failed_activation_rollback_probe)
        self.assertIn("exit 71", source)
        self.assertIn("range(100)", source)


if __name__ == "__main__":
    unittest.main()
