#!/usr/bin/env python3
from __future__ import annotations

import argparse
import importlib.util
import json
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
LEDGER = ROOT / "release/canonical-promise-ledger.json"
POLICY = ROOT / "release/final-seal-policy.json"
DEFAULT_EVIDENCE = ROOT / "release/evidence/runtime-g1-device-qualification.json"
ADOPTED = "IMPLEMENTED_AND_PRODUCTION_ADOPTED"
PROMOTED_X02 = {"RNX-P330", "RNX-P335", "RNX-P336", "RNX-P337", "RNX-P338", "RNX-P341"}


def fail(message: str) -> None:
    print(f"ERROR: {message}", file=sys.stderr)
    raise SystemExit(1)


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def read(rel: str) -> str:
    return (ROOT / rel).read_text(encoding="utf-8")


def run(argv: list[str]) -> None:
    print("$ " + " ".join(argv))
    subprocess.run(argv, cwd=ROOT, check=True)


def git_tracked(rel: str) -> bool:
    proc = subprocess.run(
        ["git", "ls-files", "--error-unmatch", "--", rel],
        cwd=ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if proc.returncode not in (0, 1):
        fail(f"cannot determine Git tracking state for {rel}: {proc.stderr.strip()}")
    return proc.returncode == 0


def evidence_resolves(ref: str) -> bool:
    ref = ref.strip()
    if not ref:
        return False
    if "::" in ref:
        path_part, node = ref.split("::", 1)
        path = ROOT / path_part
        return path.is_file() and bool(node.strip()) and node in path.read_text(encoding="utf-8", errors="replace")
    if "/" in ref:
        return (ROOT / ref).is_file()
    try:
        import tomllib
        cfg = tomllib.loads(read(".devtool.toml"))
        target = cfg["targets"]["rclone_nexus"]
        jobs = set(target.get("jobs", {}))
        nodes: set[str] = set()
        for workflow in target.get("workflows", {}).values():
            if isinstance(workflow, list):
                nodes.update(str(item.get("id")) for item in workflow if isinstance(item, dict) and item.get("id"))
        return ref in jobs or ref in nodes
    except Exception:
        return False


def assert_scope() -> None:
    data = json.loads(LEDGER.read_text(encoding="utf-8"))
    count=int(data.get("promise_count") or 0); maximum=int(data.get("max_promise_number") or 0); items=data.get("items", [])
    require(count>0 and maximum==count and len(items)==count and [x.get("id") for x in items]==[f"RNX-P{i:03d}" for i in range(1,count+1)], "canonical scope is not contiguous")
    by_id = {item["id"]: item for item in data["items"]}
    for number in range(366, 374):
        pid = f"RNX-P{number:03d}"
        item = by_id.get(pid)
        require(isinstance(item, dict), f"missing RUNTIME-G1 promise {pid}")
        require(item.get("status") == ADOPTED, f"{pid} status={item.get('status')} want {ADOPTED}")
        refs = item.get("evidence") or []
        require(refs, f"{pid} has no executable evidence")
        require("runtime-standalone-g1-audit" in refs, f"{pid} is not bound to the real RUNTIME-G1 workflow gate")
        for ref in refs:
            require(evidence_resolves(str(ref)), f"{pid} evidence does not resolve: {ref}")
    for pid in PROMOTED_X02:
        item = by_id[pid]
        require(item.get("status") == ADOPTED, f"{pid} real-device X02 obligation was not promoted by RUNTIME-G1")
        require("runtime-standalone-g1-audit" in (item.get("evidence") or []), f"{pid} promotion lacks RUNTIME-G1 evidence")
    active = data.get("active_position", {})
    position = int(active.get("position", 0))
    require(position >= 4, "canonical campaign position regressed before RUNTIME-G1")
    if position == 4:
        require(active.get("promise_range") == "RNX-P366..RNX-P373", "canonical RUNTIME-G1 active range is malformed")
        require(active.get("production_adopted_count") == 8 and active.get("blocked_by_environment_count") == 0, "RUNTIME-G1 status counts are stale")
        require(active.get("promoted_inherited_count") == 6, "RUNTIME-G1 does not report the six inherited X02 environment closures")


def assert_architecture() -> None:
    devtool = read(".devtool.toml")
    policy = json.loads(POLICY.read_text(encoding="utf-8"))
    device = read("scripts/dev/runtime_standalone_g1_device.py")
    release_q = read("scripts/dev/release_device_qualification.py")
    release_tests = read("tests/test_release_qualification.py")
    x02_gate = read("scripts/dev/runtime_standalone_x02_gate.py")
    x03_gate = read("scripts/dev/runtime_standalone_x03_gate.py")
    qualify = read("internal/runtimestore/qualify.go")
    helper = read("internal/runtimestore/helper.go")
    provider = read("internal/provider/provider.go")
    mounts_lifecycle = read("internal/mounts/lifecycle.go")
    control = read("internal/control/engine.go")
    customize = read("module/customize.sh")
    artifacts = read("scripts/dev/release_artifacts.py")
    gitignore = read(".gitignore")
    install_doc = read("docs/release/INSTALLATION.md")
    compatibility = read("docs/release/COMPATIBILITY.md")

    required_device_tokens = [
        "provider_absent_during_operation", "path_provider_poison_ignored", "active_projection_poison_ignored",
        "candidate A did not become the canonical operational managed runtime", "activate candidate B", "rollback candidate B",
        "synthetic qualified=true manifest bypassed", "boot reconcile did not restart", "source_bindings()",
        "runtime-manifest-a", "transaction-receipt", "process_ownership", "fuse_smoke_mount", "android_execution",
        "linux_arm64_android_fuse_rejected", "binary_b_sha256", "source_digest",
    ]
    for token in required_device_tokens:
        require(token in device, f"real-device RUNTIME-G1 harness missing proof surface: {token}")
    for token in ("NewFuture/rclone-fuse3-magisk", "fuse-helper-manifest", "managed_fuse_helper", "fuse_helper_asset_id"):
        require(token in device, f"RUNTIME-G1 does not prove provider-independent NewFuture fusermount3 authority: {token}")
    require("EnsureFuseHelper" in qualify and "fuse_helper_authority" in qualify, "runtime qualification does not acquire/prove canonical helper authority")
    require('newFutureRepository' in helper and '"NewFuture/rclone-fuse3-magisk"' in helper and 'newFutureHelperAsset' in helper and '"magisk-rclone_arm64-v8a.zip"' in helper, "canonical fusermount3 authority is not fixed to NewFuture")
    require("ManagedFuseHelperBin" in provider and "RuntimeEnv" in provider, "managed helper is not projected into production runtime environment")
    require("cmd.Env = provider.RuntimeEnv(p)" in mounts_lifecycle, "production mount process does not receive managed NewFuture helper PATH")

    require("runtime_authority_ready" in release_q, "release qualification still lacks canonical runtime authority readiness")
    require('runtime_authority = require_dict(data.get("runtime_authority")' in release_q, "release evidence validation does not require runtime authority")
    require('provider = data.get("provider")' in release_q and "provider compatibility evidence is malformed" in release_q, "provider remains mandatory release authority")
    require("test_providerless_managed_runtime_is_release_ready" in release_tests, "providerless release regression test missing")
    require('"fusermount3 is unavailable"' not in qualify, "runtime qualification still hard-requires a legacy/provider fusermount3 helper before attempting rooted FUSE")
    require('"/system/bin/umount"' in qualify, "providerless qualifier lacks rooted unmount fallback")
    require("external provider helper is optional in managed mode" in control, "managed-mode FUSE diagnostics still claim provider helper authority")
    require("test_managed_runtime_update_satisfies_historical_provider_reload_case" in release_tests, "historical provider reload case is not lowered to runtime compatibility")

    require("install it before using managed mounts" not in customize.lower(), "module install UX still declares provider mandatory for managed mounts")
    require("managed runtime" in customize.lower() and "optional" in customize.lower(), "module install UX does not explain optional provider compatibility")
    require("standalone" in install_doc.lower() and "provider" in install_doc.lower(), "release installation docs do not describe standalone managed mode")
    require("managed" in compatibility.lower() and "external" in compatibility.lower(), "compatibility docs do not distinguish managed from external provider mode")

    private_evidence = "release/evidence/runtime-g1-device-qualification.json"
    require("runtime-g1-device-qualification.json" in gitignore, "private RUNTIME-G1 device evidence is not gitignored")
    require("runtime-g1-device-qualification.json" in artifacts, "release source digest does not explicitly exclude private RUNTIME-G1 evidence")
    require(not git_tracked(private_evidence), "private RUNTIME-G1 device evidence is tracked by Git")

    for token in (
        "[wrapper.commands.runtime-standalone-g1]",
        "[wrapper.commands.runtime-standalone-g1-source]",
        "[wrapper.commands.runtime-standalone-g1-device]",
        "[targets.rclone_nexus.jobs.runtime-standalone-g1-source-audit]",
        "[targets.rclone_nexus.jobs.runtime-standalone-g1-device-evidence]",
        "[targets.rclone_nexus.jobs.runtime-standalone-g1-audit]",
        "[targets.rclone_nexus.jobs.runtime-standalone-g1-validation-cleanup]",
        "runtime-standalone-g1-source = [",
        "runtime-standalone-g1-device = [",
        "runtime-standalone-g1 = [",
    ):
        require(token in devtool, f"Devtool RUNTIME-G1 surface missing: {token}")

    release_start = devtool.index("release = [")
    release_end = devtool.find("\n]", release_start)
    release_flow = devtool[release_start:release_end]
    require("runtime-standalone-g1-device-evidence" in release_flow, "release workflow does not capture fresh private RUNTIME-G1 evidence")
    require(release_flow.index("runtime-standalone-g1-source-audit") < release_flow.index("runtime-standalone-g1-device-evidence") < release_flow.index("runtime-standalone-g1-audit") < release_flow.index("runtime-standalone-g1-validation-cleanup"), "release workflow G1 evidence lifecycle is not source -> capture -> audit -> cleanup")

    full_start = devtool.index("runtime-standalone-g1 = [")
    full_end = devtool.find("\n]", full_start)
    full_flow = devtool[full_start:full_end]
    require("runtime-standalone-g1-device-evidence" in full_flow, "full RUNTIME-G1 workflow does not capture private device evidence")
    require(full_flow.index("runtime-standalone-g1-device-evidence") < full_flow.index("runtime-standalone-g1-audit") < full_flow.index("runtime-standalone-g1-validation-cleanup"), "full RUNTIME-G1 workflow does not clean private evidence after audit")

    required = set(policy.get("required_ancestor_nodes", []))
    require("runtime-standalone-g1-source-audit" in required and "runtime-standalone-g1-audit" in required, "final seal policy is not bound to RUNTIME-G1 source + real-device gate")
    for flow, node in (("grand-g1-source = [", "runtime-standalone-g1-source-audit"), ("release = [", "runtime-standalone-g1-audit")):
        start = devtool.index(flow)
        end = devtool.find("\n]", start)
        require(node in devtool[start:end], f"{flow.split()[0]} bypasses {node}")
    release_start = devtool.index("release = [")
    release_end = devtool.find("\n]", release_start)
    release_flow = devtool[release_start:release_end]
    require("runtime-standalone-g1-audit" in release_flow, "release workflow does not validate RUNTIME-G1 physical evidence")
    full_start = devtool.index("runtime-standalone-g1 = [")
    full_end = devtool.find("\n]", full_start)
    require("runtime-standalone-g1-device-evidence" in devtool[full_start:full_end], "explicit RUNTIME-G1 workflow does not capture real device evidence before audit")

    require("PROMOTED_X02" in x02_gate and "active.get(\"position\"" in x02_gate, "X02 gate cannot recognize later real-device qualification")
    require("PROMOTED_X02" in x03_gate, "X03 gate still requires X02 real-device obligations to remain blocked forever")


def load_device_module():
    path = ROOT / "scripts/dev/runtime_standalone_g1_device.py"
    spec = importlib.util.spec_from_file_location("rnexus_runtime_g1_device", path)
    require(spec is not None and spec.loader is not None, "cannot load RUNTIME-G1 device validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def assert_device_evidence(path: Path) -> dict:
    device = load_device_module()
    try:
        data = device.validate(path, resolve_files=True)
    except Exception as exc:
        fail(f"RUNTIME-G1 device evidence rejected: {exc}")

    candidate = data["candidate"]
    authority = data["authority"]
    flow = data["flow"]
    a_id = str(candidate["runtime_a"])
    a_hash = str(candidate.get("binary_a_sha256", candidate.get("binary_sha256", "")))
    b_hash = str(candidate.get("binary_b_sha256", ""))
    require(len(a_hash) == 64 and len(b_hash) == 64 and a_hash != b_hash, "RNX-P370 activation flow did not use distinct executable bytes")
    require(authority.get("provider_absent_during_operation") is True, "RNX-P367 providerless operation was not proven")
    require(all(str(authority.get(k, "")) == a_id for k in ("cli_runtime_id", "boot_runtime_id", "daemon_runtime_id", "webui_runtime_id")), "RNX-P368 ingress runtime identities diverge")
    require(authority.get("path_provider_poison_ignored") is True and authority.get("active_projection_poison_ignored") is True, "RNX-P366/P369 competing runtime authorities remain")
    require(all(isinstance(flow.get(k), dict) and int(flow[k].get("mount_pid") or 0) > 1 for k in ("activate_a", "activate_b", "rollback", "boot_restart")), "RNX-P370/RNX-P371 real process flow is incomplete")
    require(flow.get("synthetic_qualification_rejected") is True, "RNX-P373 synthetic qualification bypass was not rejected")
    require(flow.get("linux_arm64_android_fuse_rejected") is True, "RNX-P341 execute-but-fail-FUSE Android adversarial candidate was not proven")
    require(str(flow.get("activate_b", {}).get("process_sha256", "")) == b_hash, "candidate B process did not execute the distinct B bytes")

    # The gate must demonstrably reject nonexistent evidence even if the document
    # hash is recomputed, and must reject a document claiming no real operation.
    original = json.loads(path.read_text(encoding="utf-8"))
    with tempfile.TemporaryDirectory(prefix="rnx-g1-negative-") as raw:
        bad_path = Path(raw) / "bad-evidence.json"
        broken = json.loads(json.dumps(original))
        broken["evidence"][0]["path"] = "/definitely/nonexistent/runtime-g1-evidence"
        broken["evidence_digest"] = device.digest({k: v for k, v in broken.items() if k != "evidence_digest"})
        bad_path.write_text(json.dumps(broken), encoding="utf-8")
        try:
            device.validate(bad_path, resolve_files=True)
        except Exception:
            pass
        else:
            fail("RUNTIME-G1 validator accepts nonexistent evidence references")

        no_run = json.loads(json.dumps(original))
        no_run["flow"]["activate_a"]["mount_pid"] = 0
        no_run["evidence_digest"] = device.digest({k: v for k, v in no_run.items() if k != "evidence_digest"})
        bad_path.write_text(json.dumps(no_run), encoding="utf-8")
        try:
            device.validate(bad_path, resolve_files=False)
        except Exception:
            pass
        else:
            fail("RUNTIME-G1 validator passes when the underlying operation did not run")
    return data


def assert_tests() -> None:
    # Re-run every implementation overlay in the gate window against current
    # source, then the cross-scope release-qualification regression affected by
    # the providerless authority remediation.
    run([sys.executable, "scripts/dev/check_canonical_scope.py"])
    run([sys.executable, "scripts/dev/runtime_standalone_x01_gate.py"])
    run([sys.executable, "scripts/dev/runtime_standalone_x02_gate.py"])
    run([sys.executable, "scripts/dev/runtime_standalone_x03_gate.py"])
    run([sys.executable, "-m", "unittest", "tests.test_release_qualification", "tests.test_runtime_standalone_g1_device", "-v"])


def main() -> int:
    parser = argparse.ArgumentParser(description="RUNTIME-G1 adversarial runtime authority gate")
    parser.add_argument("--source-only", action="store_true", help="audit gate/source wiring without accepting missing real-device evidence")
    parser.add_argument("--evidence", type=Path, default=DEFAULT_EVIDENCE)
    args = parser.parse_args()

    assert_scope()
    assert_architecture()
    assert_tests()
    if args.source_only:
        print("RUNTIME-STANDALONE-G1 source/gate implementation: PASS (real-device evidence is still mandatory for qualification)")
        print(json.dumps({"promise_range": "RNX-P366..RNX-P373", "count": 8, "status": "SOURCE_READY_DEVICE_GATE_REQUIRED", "promoted_x02": sorted(PROMOTED_X02)}, sort_keys=True))
        return 0

    data = assert_device_evidence(args.evidence.expanduser().resolve())
    print("RUNTIME-STANDALONE-G1 runtime authority gate: PASS")
    print(json.dumps({
        "promise_range": "RNX-P366..RNX-P373",
        "count": 8,
        "production_adopted": 8,
        "promoted_x02": sorted(PROMOTED_X02),
        "device_evidence_digest": data.get("evidence_digest", ""),
        "status": ADOPTED,
        "next": "SOURCE-X01",
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
