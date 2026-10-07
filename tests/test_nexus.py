from __future__ import annotations

import json
import os
from pathlib import Path
import signal
import stat
import shutil
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
MODULE = ROOT / "module"
MOUNTCTL = MODULE / "system/bin/rclone-mountctl"
NEXUS = MODULE / "system/bin/rclone-nexus"


class NexusTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.build_dir = tempfile.TemporaryDirectory()
        cls.racctl = Path(cls.build_dir.name) / "racctl"
        subprocess.run(
            ["python3", "scripts/dev/build_racctl.py", "--host", "--output", str(cls.racctl)],
            cwd=ROOT,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

    @classmethod
    def tearDownClass(cls) -> None:
        cls.build_dir.cleanup()

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.base = Path(self.tmp.name)
        self.state = self.base / "state"
        self.provider = self.base / "provider"
        (self.provider / "system/bin").mkdir(parents=True)
        (self.provider / "conf").mkdir(parents=True)
        (self.provider / "conf/rclone.conf").write_text("[fake]\ntype = local\n", encoding="utf-8")
        # Static-runtime product tests must exercise the same authority as the
        # installed module.  Give each test an isolated module projection with
        # a bundled rclone-family runtime and NewFuture-owned fusermount3 helper
        # instead of redirecting execution through the legacy provider/PATH
        # environment variables.
        self.module = self.base / "module"
        shutil.copytree(MODULE, self.module, symlinks=True)
        self.mountinfo = self.base / "mountinfo"
        self.mountinfo.write_text("", encoding="utf-8")
        fake = self.module / "system/bin/rclone"
        fake.write_text(
            "#!/bin/sh\n"
            "case ${1:-}:${2:-} in\n"
            "  version:) echo 'rclone vTEST'; exit 0 ;;\n"
            "  mount:--help) printf '%s\n' 'Flags:' '  --config string' '  --vfs-cache-mode string' '  --cache-dir string' '  --log-file string' '  --log-level string' '  --allow-other' '  --read-only' '  --dir-cache-time string' '  --poll-interval string' '  --vfs-cache-max-size string' '  --vfs-cache-max-age string' '  --vfs-cache-min-free-space string' '  --rc' '  --rc-addr string' '  --rc-user string' '  --rc-pass string'; exit 0 ;;\n"
            "  help:flags) printf '%s\n' 'Global Flags:' '  --config string' '  --rc' '  --rc-addr string' '  --rc-user string' '  --rc-pass string'; exit 0 ;;\n"
            "  --help:) printf '%s\n' 'Global Flags:' '  --config string' '  --rc'; exit 0 ;;\n"
            "  mount:*) printf '36 25 0:42 / %s rw - fuse.rclone rclone rw\\n' \"$3\" > \"$RNEXUS_MOUNTINFO_PATH\"; trap 'exit 0' TERM INT; while :; do sleep 1; done ;;\n"
            "  copy:*|sync:*|check:*) echo '{\"bytes\":1024,\"speed\":256,\"eta\":4}'; exit 0 ;;\n"
            "  listremotes:*) printf 'fake:\\n'; exit 0 ;;\n"
            "  lsjson:*) printf '[{\"Name\":\"Folder\",\"Path\":\"Folder\",\"IsDir\":true}]'; exit 0 ;;\n"
            "  *) exit 0 ;;\n"
            "esac\n",
            encoding="utf-8",
        )
        fake.chmod(0o755)
        fuse = self.module / "system/vendor/bin/fusermount3"
        fuse.parent.mkdir(parents=True, exist_ok=True)
        fuse.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
        fuse.chmod(0o755)
        managed_config = self.state / "config/rclone/rclone.conf"
        managed_config.parent.mkdir(parents=True, exist_ok=True)
        managed_config.write_text("[fake]\ntype = local\n", encoding="utf-8")
        self.env = os.environ.copy()
        self.env.update(
            {
                "RNEXUS_MODULE_DIR": str(self.module),
                "RNEXUS_STATE_DIR": str(self.state),
                "RNEXUS_PROVIDER_MODULE_DIR": str(self.provider),
                "RNEXUS_RUNTIME_MODE": "managed",
                "RNEXUS_RACCTL_BIN": str(self.racctl),
                "RNEXUS_FUSE_DEVICE": "/dev/null",
                "RNEXUS_MOUNTINFO_PATH": str(self.mountinfo),
                "RNEXUS_START_GRACE_SECONDS": "0",
                "RNEXUS_STOP_TIMEOUT_SECONDS": "2",
                "RNEXUS_RACD_DISABLE": "1",
            }
        )

    def tearDown(self) -> None:
        run = self.state / "run/mounts"
        if run.exists():
            for record_file in run.glob("*.process.json"):
                try:
                    pid = int(json.loads(record_file.read_text())["pid"])
                    os.kill(pid, signal.SIGKILL)
                except (OSError, ValueError, KeyError, json.JSONDecodeError):
                    pass
        # A failed assertion or a process-record cleanup race must not leak a
        # fake provider process into later tests/gates.  The temporary base is
        # unique to this test case, so only processes whose argv references
        # that exact directory are eligible for cleanup.
        proc = Path("/proc")
        if proc.is_dir():
            marker = str(self.base).encode()
            for entry in proc.iterdir():
                if not entry.name.isdigit():
                    continue
                try:
                    cmdline = (entry / "cmdline").read_bytes()
                    if marker in cmdline:
                        os.kill(int(entry.name), signal.SIGKILL)
                except (OSError, ValueError):
                    pass
        self.tmp.cleanup()

    def run_cmd(self, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        command = ["sh", str(args[0]), *args[1:]]
        return subprocess.run(
            command,
            cwd=ROOT,
            env=self.env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=check,
        )

    def write_mount(self, name: str = "drive", enabled: bool = True) -> Path:
        mounts = self.state / "mounts.d"
        mounts.mkdir(parents=True, exist_ok=True)
        mountpoint = self.base / "mnt" / name
        (mounts / f"{name}.conf").write_text(
            f"enabled={'true' if enabled else 'false'}\n"
            "remote=fake:\n"
            f"mountpoint={mountpoint}\n"
            "vfs_cache_mode=full\n"
            "allow_other=false\n",
            encoding="utf-8",
        )
        return mountpoint

    def protocol_request(self, operation: str, op_class: str, args: dict | None = None) -> subprocess.CompletedProcess[str]:
        payload = {
            "schema_version": 1,
            "request_id": f"python-test-{time.time_ns()}",
            "client": {"name": "python-test", "version": "1", "protocol": {"min": 1, "max": 1}},
            "operation": {"name": operation, "class": op_class, "args": args or {}},
        }
        return subprocess.run(
            [str(self.racctl), "rpc"],
            cwd=ROOT,
            env=self.env,
            input=json.dumps(payload),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=True,
        )

    def test_paths_use_persistent_state_outside_module(self) -> None:
        result = self.run_cmd(str(NEXUS), "paths")
        self.assertIn(f"state={self.state}", result.stdout)
        self.assertNotEqual(str(self.state), str(MODULE))

    def test_mount_start_status_stop(self) -> None:
        self.write_mount()
        start = self.run_cmd(str(MOUNTCTL), "start", "drive")
        self.assertIn("started", start.stdout)
        pid_file = self.state / "run/mounts/drive.process.json"
        self.assertTrue(pid_file.is_file())
        record = json.loads(pid_file.read_text())
        self.assertGreater(record["start_ticks"], 0)

        status = self.run_cmd(str(MOUNTCTL), "status", "drive")
        self.assertIn("running", status.stdout)

        stop = self.run_cmd(str(MOUNTCTL), "stop", "drive")
        self.assertIn("stopped", stop.stdout)
        self.assertFalse(pid_file.exists())

    def test_reconcile_starts_only_enabled_mounts(self) -> None:
        self.write_mount("enabled", enabled=True)
        self.write_mount("disabled", enabled=False)
        self.run_cmd(str(MOUNTCTL), "reconcile")
        self.assertTrue((self.state / "run/mounts/enabled.process.json").is_file())
        self.assertFalse((self.state / "run/mounts/disabled.process.json").exists())
        self.run_cmd(str(MOUNTCTL), "stop", "enabled")

    def test_rejects_invalid_mount_name(self) -> None:
        result = self.run_cmd(str(MOUNTCTL), "start", "../escape", check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid mount name", result.stderr)

    def test_capabilities_include_all_operation_classes(self) -> None:
        result = subprocess.run(
            [str(self.racctl), "capabilities"],
            cwd=ROOT,
            env=self.env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=True,
        )
        payload = json.loads(result.stdout)
        self.assertEqual(payload["protocol"], {"min": 1, "max": 1})
        self.assertEqual(
            set(payload["operation_classes"]),
            {"query", "preview", "run", "cancel", "reconcile"},
        )
        operation_names = {item["name"] for item in payload["operations"]}
        self.assertIn("provider.status", operation_names)
        self.assertIn("namespace.inspect", operation_names)
        self.assertIn("namespace.apply", operation_names)
        self.assertIn("policy.status", operation_names)
        self.assertIn("vfs.profiles", operation_names)
        self.assertIn("cache.prune", operation_names)
        self.assertIn("jobs.snapshot", operation_names)
        self.assertIn("job.run", operation_names)
        self.assertIn("rc.metrics", operation_names)
        self.assertIn("diagnostics.logs", operation_names)
        self.assertIn("provider.remotes", operation_names)
        self.assertIn("provider.browse", operation_names)
        self.assertIn("doctor.bundle.read", operation_names)
        self.assertIn("ui.settings.apply", operation_names)
        self.assertNotIn("system.exec", operation_names)
        self.assertNotIn("rc.call", operation_names)
        self.assertNotIn("rclone.exec", operation_names)

    def test_provider_protocol_does_not_expose_private_paths(self) -> None:
        result = self.protocol_request("provider.status", "query")
        self.assertNotIn(str(self.provider), result.stdout)
        self.assertNotIn(str(self.state), result.stdout)
        lines = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        self.assertTrue(lines[-1]["ok"])
        self.assertEqual(lines[-1]["result"]["module_id"], "rclone")

    def test_runtime_directories_are_private(self) -> None:
        self.run_cmd(str(NEXUS), "paths")
        # paths is read-only; a lifecycle operation initializes state.
        self.write_mount()
        self.run_cmd(str(MOUNTCTL), "start", "drive")
        for path in (
            self.state, self.state / "mounts.d", self.state / "run", self.state / "logs", self.state / "cache",
            self.state / "config", self.state / "desired", self.state / "run/mounts", self.state / "run/locks",
            self.state / "health", self.state / "operations", self.state / "namespace", self.state / "policy",
            self.state / "jobs", self.state / "jobs/state", self.state / "run/jobs", self.state / "run/rc",
            self.state / "diagnostics", self.state / "diagnostics/support", self.state / "platform",
        ):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700, path)
        self.run_cmd(str(MOUNTCTL), "stop", "drive")


    def test_config_protocol_migrates_legacy_without_exposing_args_file(self) -> None:
        mountpoint = self.write_mount()
        config = self.state / "mounts.d/drive.conf"
        config.write_text(config.read_text() + "args_file=private/secret.args\n", encoding="utf-8")
        snapshot = self.protocol_request("config.snapshot", "query")
        self.assertNotIn("private/secret.args", snapshot.stdout)
        lines = [json.loads(line) for line in snapshot.stdout.splitlines() if line.strip()]
        result = lines[-1]["result"]
        self.assertEqual(result["source"], "legacy-v0.1")
        self.assertTrue(result["mounts"][0]["has_args_file"])

        candidate = [{
            "name": "drive", "enabled": True, "remote": "fake:", "mountpoint": str(mountpoint),
            "vfs_cache_mode": "full", "allow_other": False, "log_level": "INFO",
        }]
        preview = self.protocol_request("config.preview", "preview", {"mounts": candidate})
        preview_result = [json.loads(line) for line in preview.stdout.splitlines() if line.strip()][-1]["result"]
        apply = self.protocol_request("config.apply", "run", {
            "expected_revision": preview_result["current_revision"],
            "candidate_digest": preview_result["candidate_digest"],
            "preview_proof": preview_result["preview_proof"],
            "mounts": candidate,
        })
        apply_result = [json.loads(line) for line in apply.stdout.splitlines() if line.strip()][-1]
        self.assertTrue(apply_result["ok"])
        self.assertEqual(apply_result["result"]["registry"]["revision"], 1)
        self.assertTrue((self.state / "config/registry-v2.json").is_file())



    def test_policy_vfs_cache_cli_surfaces(self) -> None:
        self.write_mount()
        self.env.update({
            "RNEXUS_NETWORK_STATE": "wifi",
            "RNEXUS_NETWORK_METERED": "false",
            "RNEXUS_CHARGING": "true",
            "RNEXUS_BATTERY_PERCENT": "80",
        })
        policy = json.loads(self.run_cmd(str(NEXUS), "policy", "drive").stdout)
        self.assertTrue(policy["decision"]["allowed"])
        self.assertEqual(policy["network_mode"], "offline-allowed")

        vfs = json.loads(self.run_cmd(str(NEXUS), "vfs", "drive").stdout)
        self.assertEqual(vfs["mount"]["profile"], "custom")
        self.assertEqual(vfs["mount"]["effective"]["vfs_cache_mode"], "full")
        self.assertIn(vfs["recommendation"]["profile"], {"minimal", "balanced", "streaming", "offline"})

        cache = json.loads(self.run_cmd(str(NEXUS), "cache", "status", "drive").stdout)
        self.assertEqual(cache["name"], "drive")
        self.assertNotIn(str(self.state / "cache"), json.dumps(cache))


    def test_jobs_registry_run_and_rc_credentials_stay_backend_only(self) -> None:
        candidate = [{
            "name": "copy-nightly", "enabled": True, "type": "copy",
            "source": "fake:source", "destination": "fake:destination",
            "every": "1h", "network_mode": "offline-allowed",
        }]
        preview = self.protocol_request("jobs.preview", "preview", {"jobs": candidate})
        preview_result = [json.loads(line) for line in preview.stdout.splitlines() if line.strip()][-1]["result"]
        applied = self.protocol_request("jobs.apply", "run", {
            "expected_revision": preview_result["current_revision"],
            "candidate_digest": preview_result["candidate_digest"],
            "preview_proof": preview_result["preview_proof"],
            "jobs": candidate,
        })
        self.assertTrue([json.loads(line) for line in applied.stdout.splitlines() if line.strip()][-1]["ok"])
        run = self.protocol_request("job.run", "run", {"name": "copy-nightly", "trigger": "manual"})
        run_lines = [json.loads(line) for line in run.stdout.splitlines() if line.strip()]
        self.assertTrue(run_lines[-1]["ok"])
        progress = [line for line in run_lines if line.get("kind") == "event" and line.get("event") == "progress"]
        # A very fast fake rclone may exit before the asynchronous scanner emits
        # a progress event. Progress is best-effort telemetry; the durable job
        # state and operation result are authoritative terminal truth.
        if progress:
            self.assertEqual(progress[-1]["data"]["bytes"], 1024)
        state = json.loads(self.run_cmd(str(NEXUS), "jobs", "status").stdout)["jobs"][0]
        self.assertEqual(state["last_state"], "SUCCEEDED")
        self.assertGreaterEqual(state.get("run_count", 0), 1)

        self.write_mount()
        self.run_cmd(str(MOUNTCTL), "start", "drive")
        rc_file = self.state / "run/rc/drive.json"
        secret = json.loads(rc_file.read_text())
        rc = self.run_cmd(str(NEXUS), "rc", "drive")
        self.assertNotIn(secret["username"], rc.stdout)
        self.assertNotIn(secret["password"], rc.stdout)
        self.assertIn("loopback", rc.stdout)
        self.run_cmd(str(MOUNTCTL), "stop", "drive")
        self.assertFalse(rc_file.exists())

    def test_operation_journal_survives_cli_reopen(self) -> None:
        self.write_mount()
        self.run_cmd(str(MOUNTCTL), "start", "drive")
        listing = self.run_cmd(str(NEXUS), "operations", "20")
        payload = json.loads(listing.stdout)
        operations = payload["operations"]
        starts = [item for item in operations if item["operation"] == "mount.start"]
        self.assertTrue(starts)
        self.assertEqual(starts[0]["state"], "SUCCEEDED")
        detail = self.run_cmd(str(NEXUS), "operation", starts[0]["request_id"])
        record = json.loads(detail.stdout)
        self.assertEqual(record["state"], "SUCCEEDED")
        self.assertTrue(record["events"])
        self.run_cmd(str(MOUNTCTL), "stop", "drive")

    def test_capabilities_mark_only_safe_operations_cancellable(self) -> None:
        result = subprocess.run(
            [str(self.racctl), "capabilities"], cwd=ROOT, env=self.env, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
        operations = {item["name"]: item.get("cancellable", False) for item in json.loads(result.stdout)["operations"]}
        self.assertTrue(operations["mount.start"])
        self.assertTrue(operations["mount.reconcile"])
        self.assertFalse(operations["config.apply"])
        self.assertFalse(operations["config.rollback"])
        self.assertFalse(operations["namespace.apply"])
        self.assertFalse(operations["namespace.rollback"])
        self.assertTrue(operations["job.run"])
        self.assertFalse(operations["jobs.apply"])



    def test_web_x03_logs_remotes_bundle_and_settings_are_typed_and_bounded(self) -> None:
        remotes = self.protocol_request("provider.remotes", "query")
        remotes_value = [json.loads(line) for line in remotes.stdout.splitlines() if line.strip()][-1]
        self.assertEqual(remotes_value["result"]["remotes"], ["fake"])
        browse = self.protocol_request("provider.browse", "query", {"remote": "fake", "path": "", "limit": 10})
        browse_value = [json.loads(line) for line in browse.stdout.splitlines() if line.strip()][-1]
        self.assertEqual(browse_value["result"]["entries"][0]["name"], "Folder")
        self.assertNotIn(str(self.provider), browse.stdout)

        preview = self.protocol_request("ui.settings.preview", "preview", {"settings": {"refresh_seconds": 7, "log_follow": True, "log_limit": 80, "dense_mode": True, "default_view": "runtime"}})
        pv = [json.loads(line) for line in preview.stdout.splitlines() if line.strip()][-1]["result"]
        applied = self.protocol_request("ui.settings.apply", "run", {"expected_revision": pv["current_revision"], "candidate_digest": pv["candidate_digest"], "preview_proof": pv["preview_proof"], "settings": pv["settings"]})
        self.assertTrue([json.loads(line) for line in applied.stdout.splitlines() if line.strip()][-1]["ok"])

        logs_dir = self.state / "logs"
        logs_dir.mkdir(parents=True, exist_ok=True)
        (logs_dir / "racd.log").write_text("Authorization: Bearer TOPSECRET\nINFO hello\n", encoding="utf-8")
        logs = self.protocol_request("diagnostics.logs", "query", {"limit": 20})
        self.assertNotIn("TOPSECRET", logs.stdout)
        log_value = [json.loads(line) for line in logs.stdout.splitlines() if line.strip()][-1]["result"]
        self.assertTrue(any("<redacted>" in item.get("message", "") for item in log_value["records"]))

        bundle = self.protocol_request("doctor.bundle", "run")
        bundle_meta = [json.loads(line) for line in bundle.stdout.splitlines() if line.strip()][-1]["result"]
        read = self.protocol_request("doctor.bundle.read", "query", {"bundle_id": bundle_meta["bundle_id"]})
        read_value = [json.loads(line) for line in read.stdout.splitlines() if line.strip()][-1]
        self.assertTrue(read_value["ok"])
        self.assertEqual(read_value["result"]["encoding"], "base64")
        self.assertLess(read_value["result"]["size"], 512 * 1024 + 1)

    def test_namespace_inspect_reports_observed_evidence_without_universal_claim(self) -> None:
        mountpoint = self.write_mount()
        proc = self.base / "proc"

        def write_proc(pid: int, uid: int, ns: str, command: str, mountinfo: str) -> None:
            base = proc / str(pid)
            (base / "ns").mkdir(parents=True)
            (base / "ns/mnt").symlink_to(ns)
            (base / "status").write_text(f"Name:\ttest\nUid:\t{uid}\t{uid}\t{uid}\t{uid}\n", encoding="utf-8")
            (base / "cmdline").write_bytes(command.encode() + b"\0")
            (base / "mountinfo").write_text(mountinfo, encoding="utf-8")

        base_mount = "21 1 0:1 / / rw,relatime shared:1 - rootfs rootfs rw\n"
        service_mount = base_mount + f"50 21 0:45 / {mountpoint} rw - fuse.rclone rclone rw\n"
        write_proc(100, 0, "mnt:[1]", "racd", service_mount)
        write_proc(200, 0, "mnt:[2]", "zygote64", base_mount)
        write_proc(300, 10123, "mnt:[3]", "com.termux", base_mount)
        env = self.env.copy()
        env.update({
            "RNEXUS_PROC_ROOT": str(proc),
            "RNEXUS_SERVICE_PID": "100",
            "RNEXUS_MOUNTINFO_PATH": str(proc / "100/mountinfo"),
        })
        result = subprocess.run(
            [str(self.racctl), "namespace", "inspect", "drive"],
            cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
        payload = json.loads(result.stdout)
        self.assertTrue(payload["service_visible"])
        self.assertEqual(payload["claim"], "service_only")
        self.assertEqual(payload["users"][0]["app_namespaces"], 1)
        self.assertFalse(payload["users"][0]["qualified"])


    def test_platform_doctor_bundle_and_root_manager_surfaces(self) -> None:
        self.env["RNEXUS_ROOT_MANAGER_HINT"] = "kernelsu"
        manager = subprocess.run(
            [str(self.racctl), "platform", "root-manager"], cwd=ROOT, env=self.env,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
        payload = json.loads(manager.stdout)
        self.assertEqual(payload["kind"], "kernelsu")
        self.assertTrue(payload["capabilities"]["embedded_webui"])
        self.assertNotIn(str(self.state), manager.stdout)

        doctor = subprocess.run(
            [str(self.racctl), "doctor", "--json"], cwd=ROOT, env=self.env,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
        report = json.loads(doctor.stdout)
        self.assertIn(report["overall"], {"PASS", "WARN"})
        self.assertIn("root.manager", {item["code"] for item in report["checks"]})

        bundle = subprocess.run(
            [str(self.racctl), "doctor", "--bundle"], cwd=ROOT, env=self.env,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
        path = Path(bundle.stdout.strip())
        self.assertTrue(path.is_file())
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)

    def test_purge_on_uninstall_requires_explicit_arming(self) -> None:
        subprocess.run([str(self.racctl), "platform", "migrate"], cwd=ROOT, env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        marker = self.state / "config" / "keep.txt"
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.write_text("keep", encoding="utf-8")
        status = subprocess.run([str(self.racctl), "platform", "purge-on-uninstall", "status"], cwd=ROOT, env=self.env, check=True, stdout=subprocess.PIPE, text=True)
        self.assertFalse(json.loads(status.stdout)["armed"])
        subprocess.run([str(self.racctl), "platform", "uninstall-hook"], cwd=ROOT, env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.assertTrue(marker.is_file())
        subprocess.run([str(self.racctl), "platform", "purge-on-uninstall", "enable"], cwd=ROOT, env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        subprocess.run([str(self.racctl), "platform", "uninstall-hook"], cwd=ROOT, env=self.env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.assertFalse(self.state.exists())

    def test_daemon_single_instance_and_sigterm_cleanup(self) -> None:
        env = self.env.copy()
        env.pop("RNEXUS_RACD_DISABLE", None)
        first = subprocess.Popen(
            [str(self.racctl), "racd"],
            cwd=ROOT,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        socket = self.state / "run/racd.sock"
        deadline = time.time() + 3
        while not socket.exists() and time.time() < deadline:
            time.sleep(0.02)
        self.assertTrue(socket.exists(), "racd socket did not appear")

        second = subprocess.run(
            [str(self.racctl), "racd"],
            cwd=ROOT,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
            timeout=3,
        )
        self.assertNotEqual(second.returncode, 0)
        self.assertIn("RNX_E_DAEMON_ALREADY_RUNNING", second.stderr)

        first.send_signal(signal.SIGTERM)
        first.wait(timeout=3)
        stderr_text = first.stderr.read() if first.stderr else ""
        if first.stdout:
            first.stdout.close()
        if first.stderr:
            first.stderr.close()
        self.assertEqual(first.returncode, 0, stderr_text)
        self.assertFalse(socket.exists())


    def test_webui_detached_reuse_and_embedded_typed_bridge(self) -> None:
        import base64

        request = {
            "schema_version": 1,
            "request_id": f"webui-python-{time.time_ns()}",
            "client": {"name": "webui-python", "version": "1", "protocol": {"min": 1, "max": 1}},
            "operation": {"name": "provider.status", "class": "query", "args": {}},
        }
        encoded = base64.urlsafe_b64encode(json.dumps(request, separators=(",", ":")).encode()).decode().rstrip("=")
        bridge = subprocess.run(
            [str(self.racctl), "webui", "bridge", "--request-base64", encoded],
            cwd=ROOT, env=self.env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
        envelope = json.loads(bridge.stdout)
        self.assertEqual(envelope["schema_version"], 1)
        self.assertTrue(envelope["response"]["ok"])
        self.assertNotIn(str(self.provider), bridge.stdout)

        first = subprocess.run(
            [str(self.racctl), "webui", "start", "--json"], cwd=ROOT, env=self.env,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
        second = subprocess.run(
            [str(self.racctl), "webui", "start", "--json"], cwd=ROOT, env=self.env,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
        )
        a, b = json.loads(first.stdout), json.loads(second.stdout)
        self.assertEqual(a["pid"], b["pid"])
        self.assertNotEqual(a["bootstrap_url"], b["bootstrap_url"])
        self.assertTrue(a["url"].startswith("http://127.0.0.1:"))
        self.assertNotIn("admin_secret", a)
        state = self.state / "run/webui-server.json"
        self.assertEqual(stat.S_IMODE(state.stat().st_mode), 0o600)
        os.kill(a["pid"], signal.SIGTERM)
        for _ in range(50):
            if not state.exists():
                break
            time.sleep(0.02)
        self.assertFalse(state.exists(), "owned WebUI runtime state survived server shutdown")


if __name__ == "__main__":
    unittest.main()
