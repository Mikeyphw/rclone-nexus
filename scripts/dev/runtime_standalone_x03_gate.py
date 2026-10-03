#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
LEDGER = ROOT / "release/canonical-promise-ledger.json"
ADOPTED = "IMPLEMENTED_AND_PRODUCTION_ADOPTED"


def fail(message: str) -> None:
    print(f"ERROR: {message}", file=sys.stderr)
    raise SystemExit(1)


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def run(argv: list[str]) -> None:
    print("$ " + " ".join(argv))
    subprocess.run(argv, cwd=ROOT, check=True)


def evidence_resolves(ref: str) -> bool:
    if "::" in ref:
        path_part, node = ref.split("::", 1)
        path = ROOT / path_part
        return path.is_file() and bool(node) and node in path.read_text(encoding="utf-8", errors="replace")
    return (ROOT / ref).is_file()


def promise(pid: str, condition: bool, message: str) -> None:
    require(condition, f"{pid}: {message}")


def assert_scope() -> None:
    data = json.loads(LEDGER.read_text())
    require(data.get("max_promise_number") == 500 and data.get("promise_count") == 500, "canonical RNX-P001..RNX-P500 scope changed unexpectedly")
    by_id = {item["id"]: item for item in data["items"]}
    for number in range(349, 366):
        pid = f"RNX-P{number:03d}"
        require(pid in by_id, f"missing canonical X03 promise {pid}")
        item = by_id[pid]
        require(item.get("status") == ADOPTED, f"{pid} status={item.get('status')} want {ADOPTED}")
        evidence = item.get("evidence") or []
        require(evidence, f"{pid} has no evidence")
        for ref in evidence:
            require(evidence_resolves(str(ref)), f"{pid} evidence does not resolve: {ref}")
    active = data.get("active_position", {})
    require(active.get("position") == 3 and active.get("promise_range") == "RNX-P349..RNX-P365", "canonical active position is not X03")
    require(active.get("production_adopted_count") == 17 and active.get("blocked_by_environment_count") == 0, "X03 status counts are stale")
    # X03 must not launder the six real-device X02 obligations into success.
    for pid in ("RNX-P330", "RNX-P335", "RNX-P336", "RNX-P337", "RNX-P338", "RNX-P341"):
        require(by_id[pid].get("status") == "BLOCKED_BY_ENVIRONMENT", f"{pid} was improperly promoted by X03")


def assert_architecture() -> None:
    state = read("internal/runtimestate/state.go")
    activation = read("internal/runtimeactivation/activation.go")
    tests = read("internal/runtimeactivation/activation_test.go")
    auth = read("internal/runtimeauth/runtime.go")
    auth_tests = read("internal/runtimeauth/runtime_test.go")
    mounts = read("internal/mounts/lifecycle.go")
    registry = read("internal/mounts/registry.go")
    control = read("internal/control/engine.go")
    cli = read("cmd/racctl/main.go")
    web = read("module/webroot/app.js")
    service = read("module/service.sh")
    docs = read("docs/implementation/RUNTIME-STANDALONE-X03.md")
    devtool = read(".devtool.toml")
    policy = json.loads(read("release/final-seal-policy.json"))

    for token in ("ActiveRuntimeID", "PreviousRuntimeID", "ActiveBinarySHA256", "PreviousBinarySHA256", "RuntimeActivationState"):
        promise("RNX-P349", token in state or token in activation or token in read("internal/paths/paths.go"), f"durable identity field missing: {token}")
    promise("RNX-P350", "QuiesceOwned" in mounts and "owned" in mounts.lower() and "quiesce" in activation.lower(), "activation does not use ownership-qualified quiesce")
    promise("RNX-P351", "os.Symlink" in activation and "os.Rename" in activation and "TestProjectionRenameDoesNotOverwriteBytesUnderOpenProcess" in tests, "active process bytes can be overwritten in place")
    promise("RNX-P352", "verifyCandidate" in activation and activation.count("verifyCandidate") >= 2 and "VerifyRuntimeBytes" in activation, "candidate is not reverified across activation boundary")
    promise("RNX-P353", "ReconcileRuntimeTransition" in activation and "verifyDesired" in activation, "desired mounts are not restarted and verified")
    promise("RNX-P354", "PhaseRollback" in activation and "TestActivationRollbackOnStartupContractFailure" in tests, "automatic rollback path missing")
    promise("RNX-P355", "PreviousRuntimeID" in activation and "previous runtime" in docs.lower(), "previous runtime is not retained")
    promise("RNX-P356", 'runtime recover' in service and service.index('runtime recover') < service.index('--require-operational'), "boot does not recover activation before readiness")
    promise("RNX-P357", "PhaseStaged" in activation and "PhaseQuiescing" in activation and "PhaseActivePendingVerify" in activation and "PhaseRollback" in activation and "TestRecoverEveryInFlightTransitionFailsSafeToPrevious" in tests, "transition crash recovery is incomplete")
    shared_ops = ("runtime.activation.status", "runtime.activate", "runtime.rollback", "runtime.recover")
    promise("RNX-P358", all(op in control and op in web for op in shared_ops) and all(op.split("runtime.",1)[1].replace("activation.status","activation-status") in cli or op in cli for op in shared_ops), "CLI and WebUI do not converge on control-engine activation operations")

    promise("RNX-P359", "TestRecoverEveryInFlightTransitionFailsSafeToPrevious" in tests and "PhaseQuiescing" in tests, "crash during quiesce is not exercised")
    promise("RNX-P360", "TestRecoverEveryInFlightTransitionFailsSafeToPrevious" in tests and "PhaseActivePendingVerify" in tests, "crash after switch before verify is not exercised")
    promise("RNX-P361", "TestActivationRollbackWhenCandidateDisappearsAfterStaging" in tests, "candidate disappearance after staging is not exercised")
    promise("RNX-P362", "TestRecoverWithoutPreviousFailsClosed" in tests, "missing previous runtime is not fail-closed")
    promise("RNX-P363", "TestActivationRollbackWhenQuiesceRefuses" in tests, "mount quiesce refusal is not exercised")
    promise("RNX-P364", "TestActivationRollbackOnStartupContractFailure" in tests, "startup/RC contract failure is not exercised")
    promise("RNX-P365", "TestRollbackRestartPartialFailureIsDegradedRecovered" in tests, "partial rollback restart is not exercised")

    require("TransitionInProgress" in registry and "runtime activation is in progress" in registry, "config publication can race activation")
    require("ExecutableForTransition" in auth and "runtime activation is in progress" in auth, "direct runtime execution can bypass transition state")
    require("TestActivationStateIsCanonicalOverManagedProjection" in auth_tests and "TestNormalExecutableCannotBypassActivationTransition" in auth_tests, "runtime authority/bypass tests missing")
    require("nexus-managed-activation" in auth and "VerifyRuntimeBytes" in auth, "runtimeauth does not resolve active ID/hash from immutable store")
    require("RuntimeTransactionsDir" in read("internal/paths/paths.go") and "writeReceipt" in activation, "durable transaction receipts missing")

    required = "runtime-standalone-x03-audit"
    require(required in policy.get("required_ancestor_nodes", []), "final seal policy is not bound to X03")
    require("[targets.rclone_nexus.jobs.runtime-standalone-x03-audit]" in devtool and "runtime-standalone-x03 = [" in devtool, "Devtool X03 job/workflow missing")
    for flow in ("grand-g1-source = [", "release = ["):
        start = devtool.index(flow)
        end = devtool.find("\n]", start)
        require(required in devtool[start:end], f"{flow.split()[0]} bypasses X03")


def assert_production_cli() -> None:
    # Exercise the compiled user ingress. A host cannot manufacture Android/FUSE
    # qualification, so the useful production proof here is fail-closed activation
    # plus absence of synthetic activation state.
    with tempfile.TemporaryDirectory(prefix="rnx-x03-cli-") as raw:
        temp = Path(raw)
        racctl = temp / "racctl"
        subprocess.run(["go", "build", "-o", str(racctl), "./cmd/racctl"], cwd=ROOT, check=True)
        state = temp / "state"
        env = os.environ.copy()
        env.update({
            "RNEXUS_STATE_DIR": str(state),
            "RNEXUS_RUNTIME_STORE_DIR": str(state / "runtimes"),
            "RNEXUS_RUNTIME_DIR": str(state / "runtime"),
            "RNEXUS_RUNTIME_TRANSACTIONS_DIR": str(state / "runtime" / "transactions"),
            "RNEXUS_RUNTIME_ACTIVATION_STATE": str(state / "runtime" / "activation-v1.json"),
            "RNEXUS_RUNTIME_ACTIVATION_LOCK": str(state / "runtime" / "activation.lock"),
            "RNEXUS_CONFIG_DIR": str(state / "config"),
            "RNEXUS_MANAGED_RCLONE_CONFIG": str(state / "config" / "rclone" / "rclone.conf"),
            "RNEXUS_PROVIDER_MODULE_DIR": str(temp / "provider-absent"),
            "RNEXUS_FUSE_DEVICE": str(temp / "fake-fuse"),
        })

        status = subprocess.run([str(racctl), "runtime", "activation-status"], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        require(status.returncode == 0, f"compiled racctl activation-status failed: {status.stderr}")
        require(json.loads(status.stdout).get("present") is False, "fresh runtime activation state was manufactured")

        recover = subprocess.run([str(racctl), "runtime", "recover"], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        require(recover.returncode == 0, f"compiled racctl no-op recovery failed: {recover.stderr}")

        candidate = temp / "truncated-rclone"
        candidate.write_bytes(bytes([0x7F, ord("E"), ord("L"), ord("F"), 2, 1]))
        candidate.chmod(0o700)
        imported = subprocess.run([str(racctl), "runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", str(candidate)], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        require(imported.returncode != 0, "production import unexpectedly qualified invalid host candidate")
        manifest = json.loads(imported.stdout)
        runtime_id = manifest.get("runtime_id")
        require(bool(runtime_id), "failed import did not persist resolvable candidate evidence")

        activated = subprocess.run([str(racctl), "runtime", "activate", str(runtime_id)], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        require(activated.returncode != 0, "production activation accepted an unqualified runtime")
        require(not (state / "runtime" / "activation-v1.json").exists(), "failed preflight activation mutated canonical activation state")

        capabilities = subprocess.run([str(racctl), "capabilities"], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        require(capabilities.returncode == 0, f"compiled racctl capabilities failed: {capabilities.stderr}")
        capability_text = capabilities.stdout
        for op in ("runtime.candidates", "runtime.activation.status", "runtime.activate", "runtime.rollback", "runtime.recover"):
            require(op in capability_text, f"compiled racctl capability registry missing {op}")


def assert_tests() -> None:
    run(["go", "test", "./internal/runtimestate", "./internal/runtimeactivation", "./internal/runtimeauth", "./internal/mounts", "./internal/control", "./cmd/racctl"])
    run([sys.executable, "scripts/dev/check_canonical_scope.py"])
    assert_production_cli()


def main() -> int:
    assert_scope()
    assert_architecture()
    assert_tests()
    print("RUNTIME-STANDALONE-X03 transactional activation + rollback: PASS")
    print(json.dumps({
        "promise_range": "RNX-P349..RNX-P365",
        "count": 17,
        "production_adopted": 17,
        "blocked_by_environment": 0,
        "status": "IMPLEMENTED_AND_PRODUCTION_ADOPTED",
        "next": "RUNTIME-G1",
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
