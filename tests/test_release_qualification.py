from __future__ import annotations

import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "dev" / "release_device_qualification.py"
spec = importlib.util.spec_from_file_location("qualification", SCRIPT)
assert spec and spec.loader
q = importlib.util.module_from_spec(spec); spec.loader.exec_module(q)


def base_obs(*, boot="boot-a", daemon_pid=100, mount_pids=(200, 201), online=True, network="wifi", charging=True,
             screen="on", idle="active", remote="online", storage=True, pressure=False, user="0", users=None, job_count=2):
    users = ["0", "10"] if users is None else users
    mounts = {}
    policies = {}
    namespaces = {}
    for idx, name in enumerate(("drive", "media")):
        pid = mount_pids[idx]
        mounts[name] = {
            "name": name, "state": "RUNNING", "process_alive": True, "mount_alive": True, "owned_mount": True, "pid": pid,
            "readiness": {
                "ready": storage and remote == "online",
                "remote_state": remote,
                "conditions": [{"name": "target_storage", "required": True, "ready": storage, "reason": "" if storage else "target_storage_unavailable"}],
            },
            "policy": {"state": "allowed", "reasons": [], "observation": {"online": online, "network_class": network, "charging_known": True, "charging": charging}},
            "cache": {"below_min_free": pressure, "above_high": False, "prune_pending": pressure},
        }
        policies[name] = {"name": name, "decision": {"state": "allowed", "reasons": ["cache_free_space_below_minimum"] if pressure else [], "observation": {"online": online, "network_class": network, "charging_known": True, "charging": charging}}}
        namespaces[name] = {"claim": "observed", "achieved_classes": ["service", "shell"], "visibility": []}
    return {
        "boot_id": boot, "uptime_seconds": 1000.0, "current_user": user, "users": users,
        "screen_state": screen, "device_idle_state": idle,
        "nexus": {"version": "v0.1.0"},
        "root_manager": {"compatible": True, "kind": "kernelsu", "version": "KernelSU 1.0"},
        "provider": {"module_id": "rclone", "ready": True, "module_version": "1.0", "rclone_version": "rclone v1.75", "fuse_helper_ready": True},
        "runtime_authority": {"mode": "managed", "canonical": True, "operational": True, "ambiguous_authority": False, "binary": "/data/adb/rclone-nexus/runtimes/r1/rclone", "config": "/data/adb/rclone-nexus/config/rclone/rclone.conf", "source": "nexus-managed-activation", "active_runtime_id": "r1"},
        "daemon": {"pid": daemon_pid, "alive": True, "start_ticks": daemon_pid * 10},
        "mounts": mounts, "policies": policies, "namespaces": namespaces,
        "jobs": {
            "config": {"jobs": [{"name": "verify", "enabled": True, "type": "check", "every": "1m"}]},
            "status": {"jobs": [{"name": "verify", "run_count": job_count}]},
        },
    }


def entry_for(case: str, observations: list[dict]) -> dict:
    entry = {"status": "verifying", "phase": "test", "observations": []}
    for idx, obs in enumerate(observations):
        q.append_observation(entry, obs, f"o{idx}")
    q.pass_case(entry, ["fixture machine assertion"])
    ok, reason = q.verify_case_proof(case, entry)
    if not ok:
        raise AssertionError(f"bad test fixture for {case}: {reason}")
    return entry


def valid_entries() -> dict[str, dict]:
    pre = base_obs()
    values: dict[str, dict] = {}
    values["reboot"] = entry_for("reboot", [pre, base_obs(boot="boot-b", daemon_pid=101, mount_pids=(210, 211))])
    values["root_manager_restart"] = entry_for("root_manager_restart", [pre, base_obs(daemon_pid=102)])
    provider_post = base_obs(mount_pids=(212, 213)); provider_post["provider"]["module_version"] = "1.1"
    values["provider_update_reload"] = entry_for("provider_update_reload", [pre, provider_post])
    offline = base_obs(online=False, network="offline")
    cellular = base_obs(network="cellular")
    values["wifi_mobile_offline"] = entry_for("wifi_mobile_offline", [pre, offline, cellular, base_obs()])
    doze = base_obs(charging=False, screen="off", idle="idle")
    values["doze_screenoff_charging"] = entry_for("doze_screenoff_charging", [pre, doze, base_obs()])
    values["remote_outage_auth_recovery"] = entry_for("remote_outage_auth_recovery", [pre, base_obs(remote="offline"), base_obs(), base_obs(remote="auth_error"), base_obs()])
    stale = base_obs(); stale["mounts"]["drive"].update({"state": "MOUNT_STALE", "process_alive": False, "mount_alive": True})
    values["stale_fuse_killed_rclone"] = entry_for("stale_fuse_killed_rclone", [pre, stale, base_obs(mount_pids=(220, 201))])
    daemon_down = base_obs(); daemon_down["daemon"] = {"pid": 100, "alive": False, "start_ticks": 1000}
    values["daemon_crash_restart"] = entry_for("daemon_crash_restart", [pre, daemon_down, base_obs(daemon_pid=103)])
    storage_down = base_obs(storage=False)
    pressured = base_obs(pressure=True)
    values["storage_remount_low_space"] = entry_for("storage_remount_low_space", [pre, storage_down, pressured, base_obs()])
    w1 = base_obs(); w1["webui"] = {"pid": 300, "reachable": True, "url": "http://127.0.0.1:1"}
    w2 = base_obs(); w2["webui"] = {"pid": 300, "expired": True}
    w3 = base_obs(); w3["webui"] = {"pid": 301, "reachable": True, "url": "http://127.0.0.1:2"}
    values["webui_reopen_idle_expiry"] = entry_for("webui_reopen_idle_expiry", [w1, w2, w3])
    switched = base_obs(user="10")
    values["android_user_namespace_change"] = entry_for("android_user_namespace_change", [pre, switched, base_obs()])
    values["simultaneous_mounts_jobs"] = entry_for("simultaneous_mounts_jobs", [pre, base_obs(job_count=3)])
    return values


def complete_evidence() -> dict:
    return {
        "schema_version": 3, "campaign_position": "REL-X01", "captured_at": "2026-09-30T00:00:00+00:00",
        "device": {"sdk": "36", "fingerprint": "example/device/build"},
        "nexus": {"version": "v0.1.0"},
        "root_manager": {"kind": "kernelsu", "version": "KernelSU 1.0", "compatible": True},
        "provider": {"module_id": "rclone", "module_version": "1.0", "ready": True, "fuse_helper_ready": True, "rclone_version": "rclone v1.75"},
        "runtime_authority": {"mode": "managed", "canonical": True, "operational": True, "ambiguous_authority": False, "binary": "/data/adb/rclone-nexus/runtimes/r1/rclone", "config": "/data/adb/rclone-nexus/config/rclone/rclone.conf", "source": "nexus-managed-activation", "active_runtime_id": "r1"},
        "doctor": {"overall": "PASS", "checks": []},
        "namespace_visibility": {"drive": {"claim": "observed", "achieved_classes": ["service"], "visibility": []}},
        "qualification": {"harness": "scripts/dev/release_device_qualification.py", "harness_version": q.HARNESS_VERSION, "session_id": "fixture", "mounts": ["drive", "media"], "config_digest": "x", "baseline_digest": "y"},
        "endurance_cases": valid_entries(),
    }


class ReleaseQualificationTests(unittest.TestCase):
    def write_and_validate(self, data: dict, complete=True):
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "device.json"
            path.write_text(json.dumps(data), encoding="utf-8"); path.chmod(0o600)
            return q.validate(path, complete)

    def test_explicit_source_bound_racctl_is_first_candidate(self):
        old = q.os.environ.get("RNEXUS_RACCTL")
        try:
            q.os.environ["RNEXUS_RACCTL"] = "/data/adb/qualification/gate-racctl"
            self.assertEqual(q.racctl_candidates()[0], "/data/adb/qualification/gate-racctl")
        finally:
            if old is None:
                q.os.environ.pop("RNEXUS_RACCTL", None)
            else:
                q.os.environ["RNEXUS_RACCTL"] = old

    def test_webui_startup_read_honors_deadline(self):
        proc = subprocess.Popen(
            [sys.executable, "-c", "import time; time.sleep(2)"],
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        try:
            started = q.time.monotonic()
            self.assertEqual(q.process_line_before_deadline(proc, 0.05), "")
            self.assertLess(q.time.monotonic() - started, 0.75)
        finally:
            proc.kill(); proc.wait(timeout=2)
            if proc.stdout is not None: proc.stdout.close()
            if proc.stderr is not None: proc.stderr.close()

    def test_release_automatic_cases_use_shell_and_measured_webui_reopen(self):
        text = SCRIPT.read_text(encoding="utf-8")
        self.assertIn('fixed_root_command(["/system/bin/sh", service], 20)', text)
        self.assertIn('reopened_reachable = loopback_bootstrap_reachable(reopened_url)', text)
        self.assertIn('process_line_before_deadline(proc, 6)', text)

    def test_absent_daemon_identity_is_recorded_as_not_alive(self):
        from unittest import mock

        def fake_fixed_root_command(argv, timeout=10):
            return (1, "", "not found")

        with mock.patch.object(q, "fixed_root_command", side_effect=fake_fixed_root_command):
            self.assertEqual(q.daemon_identity(), {"alive": False})

    def test_all_twelve_case_proofs_validate(self):
        counts = self.write_and_validate(complete_evidence())
        self.assertEqual(counts["pass"], 12)
        self.assertEqual(counts["failed"], 0)



    def test_namespace_achieved_classes_may_be_omitted_when_empty(self):
        data = complete_evidence()
        data["namespace_visibility"]["drive"] = {"claim": "service_only", "visibility": []}
        counts = self.write_and_validate(data)
        self.assertEqual(counts["failed"], 0)

    def test_namespace_malformed_error_is_actionable(self):
        data = complete_evidence()
        data["namespace_visibility"]["drive"] = {"claim": "service_only"}
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "device.json"
            path.write_text(json.dumps(data), encoding="utf-8")
            path.chmod(0o600)
            with self.assertRaises(q.feedback.QualificationFailure) as ctx:
                q.validate(path, True)
        exc = ctx.exception
        self.assertEqual(exc.promise, "RNX-P482")
        self.assertIn("racctl namespace inspect drive", exc.command_text)
        self.assertIn("visibility", exc.expected)

    def test_providerless_managed_runtime_is_release_ready(self):
        data = complete_evidence()
        data["provider"] = {}
        counts = self.write_and_validate(data)
        self.assertEqual(counts["failed"], 0)

    def test_managed_runtime_update_satisfies_historical_provider_reload_case(self):
        pre = base_obs()
        pre["provider"] = {}
        post = base_obs(mount_pids=(212, 213))
        post["provider"] = {}
        post["runtime_authority"]["active_runtime_id"] = "r2"
        post["runtime_authority"]["binary"] = "/data/adb/rclone-nexus/runtimes/r2/rclone"
        entry = entry_for("provider_update_reload", [pre, post])
        ok, reason = q.verify_case_proof("provider_update_reload", entry)
        self.assertTrue(ok, reason)

    def test_android_user_machine_skip_is_the_only_skip(self):
        data = complete_evidence()
        data["endurance_cases"]["android_user_namespace_change"] = {
            "status": "skip", "phase": "complete",
            "proof": {"schema_version": 1, "kind": "machine-unavailable", "reason": "one Android user", "observed_users": ["0"]},
        }
        counts = self.write_and_validate(data)
        self.assertEqual((counts["pass"], counts["skip"]), (11, 1))
        data["endurance_cases"]["reboot"] = copy.deepcopy(data["endurance_cases"]["android_user_namespace_change"])
        with self.assertRaises(SystemExit): self.write_and_validate(data)

    def test_assertion_only_pass_is_rejected(self):
        data = complete_evidence()
        data["endurance_cases"]["reboot"] = {"status": "pass", "note": "trust me"}
        with self.assertRaises(SystemExit): self.write_and_validate(data)

    def test_observation_chain_tamper_is_rejected(self):
        data = complete_evidence()
        data["endurance_cases"]["reboot"]["observations"][1]["payload"]["boot_id"] = "tampered"
        with self.assertRaises(SystemExit): self.write_and_validate(data)

    def test_missing_real_transition_is_rejected_even_with_rehashed_chain(self):
        data = complete_evidence()
        entry = entry_for("reboot", [base_obs(), base_obs(boot="boot-b", mount_pids=(210,211))])
        # Rebuild a syntactically valid chain that does not change boot id.
        entry = {"status": "verifying", "observations": []}
        q.append_observation(entry, base_obs(), "a"); q.append_observation(entry, base_obs(mount_pids=(210,211)), "b")
        q.pass_case(entry, ["fake"])
        data["endurance_cases"]["reboot"] = entry
        with self.assertRaises(SystemExit): self.write_and_validate(data)

    def test_legacy_v1_v2_evidence_is_rejected(self):
        for schema in (1, 2):
            data = complete_evidence(); data["schema_version"] = schema
            with self.subTest(schema=schema), self.assertRaises(SystemExit): self.write_and_validate(data)

    def test_free_form_record_command_is_removed(self):
        result = subprocess.run([sys.executable, str(SCRIPT), "--help"], cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(result.returncode, 0)
        self.assertNotIn("record", result.stdout)
        self.assertIn("run", result.stdout); self.assertIn("resume", result.stdout)

    def test_every_case_has_a_machine_predicate(self):
        self.assertEqual(set(valid_entries()), set(q.CASES))
        for case, entry in valid_entries().items():
            ok, reason = q.verify_case_proof(case, entry)
            self.assertTrue(ok, f"{case}: {reason}")

    def test_real_policy_status_shape_is_parsed(self):
        obs = base_obs(network="cellular", charging=False)
        self.assertEqual(q.network_class(obs), (True, "cellular"))
        self.assertIs(q.charging(obs), False)

    def test_scheduled_job_can_advance_from_no_prior_state_file(self):
        pre = base_obs(job_count=0)
        pre["jobs"]["status"] = {"jobs": []}
        post = base_obs(job_count=1)
        entry = entry_for("simultaneous_mounts_jobs", [pre, post])
        ok, reason = q.verify_case_proof("simultaneous_mounts_jobs", entry)
        self.assertTrue(ok, reason)

    def test_root_first_command_prefers_privileged_observation_even_when_user_command_would_succeed(self):
        from unittest import mock
        calls = []

        def fake_command(argv, timeout=10):
            calls.append(list(argv))
            if argv and argv[0] == "/fixture/su":
                return 0, "root-view", ""
            return 0, "user-view", ""

        with mock.patch.object(q.os, "geteuid", return_value=2000), \
             mock.patch.object(q.shutil, "which", return_value="/fixture/su"), \
             mock.patch.object(q, "command", side_effect=fake_command):
            rc, out, err = q.root_first_command(["racctl", "platform", "root-manager"])
        self.assertEqual((rc, out, err), (0, "root-view", ""))
        self.assertEqual(calls[0][0], "/fixture/su")
        self.assertNotIn(["racctl", "platform", "root-manager"], calls[:1])

    def test_metadata_readiness_reports_exact_blockers(self):
        obs = base_obs()
        obs["root_manager"] = {"compatible": False, "kind": "unknown", "version": ""}
        obs["runtime_authority"] = {"mode": "managed", "canonical": False, "operational": False, "binary": ""}
        errors = q.metadata_readiness_errors(obs)
        joined = " | ".join(errors)
        self.assertIn("root-manager incompatible/undetected", joined)
        self.assertIn("root-manager version missing", joined)
        self.assertIn("runtime authority not release-ready", joined)

    def test_capture_auto_discovers_mounts_for_canonical_wrapper(self):
        from unittest import mock
        baseline = base_obs()
        baseline["doctor"] = {"overall": "PASS", "checks": []}
        with tempfile.TemporaryDirectory() as td, \
             mock.patch.object(q, "is_android", return_value=True), \
             mock.patch.object(q, "configured_mounts", return_value=(["drive", "media"], {"mounts": [{"name": "drive"}, {"name": "media"}]})), \
             mock.patch.object(q, "collect_observation", return_value=baseline), \
             mock.patch.object(q, "prop", return_value="fixture"):
            path = Path(td) / "device.json"
            q.capture(path, [])
            data = json.loads(path.read_text())
            self.assertEqual(data["qualification"]["mounts"], ["drive", "media"])
            self.assertEqual(set(data["namespace_visibility"]), {"drive", "media"})

    def test_devtool_release_evidence_wrapper_uses_auto_discovery_capture(self):
        text = (ROOT / ".devtool.toml").read_text(encoding="utf-8")
        self.assertIn('command = ["python3", "scripts/dev/release_device_qualification.py", "capture"]', text)
        self.assertNotIn('"capture", "--mount"', text)


if __name__ == "__main__":
    unittest.main()
