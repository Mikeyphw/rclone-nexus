#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
LEDGER = ROOT / "release/canonical-promise-ledger.json"
POSITION_IDS = [f"RNX-P{i:03d}" for i in range(279, 308)]
TERMINAL = {"IMPLEMENTED_AND_PRODUCTION_ADOPTED"}


def fail(message: str) -> None:
    print(f"RUNTIME-STANDALONE-X01: FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def promise(pid: str, condition: bool, message: str) -> None:
    if not condition:
        fail(f"{pid}: {message}")


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def run(argv: list[str]) -> None:
    cp = subprocess.run(argv, cwd=ROOT, text=True)
    if cp.returncode != 0:
        fail(f"command failed ({cp.returncode}): {' '.join(argv)}")


def resolve_evidence(ref: str) -> bool:
    path, sep, node = ref.partition("::")
    target = ROOT / path
    if not target.is_file():
        return False
    if not sep:
        return True
    return node in target.read_text(encoding="utf-8", errors="replace")


def assert_scope() -> None:
    data = json.loads(LEDGER.read_text(encoding="utf-8"))
    count=int(data.get("promise_count") or 0); maximum=int(data.get("max_promise_number") or 0); items=data.get("items", [])
    require(count>0 and maximum==count and len(items)==count and [x.get("id") for x in items]==[f"RNX-P{i:03d}" for i in range(1,count+1)], "canonical scope is not a contiguous RNX-P001..max universe")
    by_id = {item.get("id"): item for item in data.get("items", [])}
    require(all(pid in by_id for pid in POSITION_IDS), "Position 1 promise IDs are incomplete")
    for pid in POSITION_IDS:
        item = by_id[pid]
        require(item.get("status") in TERMINAL, f"{pid} is not production-adopted: {item.get('status')}")
        evidence = item.get("evidence") or []
        require(evidence, f"{pid} has no executable/resolvable evidence")
        for ref in evidence:
            require(resolve_evidence(ref), f"{pid} evidence does not resolve: {ref}")


def assert_architecture() -> None:
    runtime = read("internal/runtimeauth/runtime.go")
    paths = read("internal/paths/paths.go")
    provider = read("internal/provider/provider.go")
    service = read("module/service.sh")
    common = read("module/lib/common.sh")
    racctl = read("cmd/racctl/main.go")
    control = read("internal/control/engine.go")
    lifecycle = read("internal/mounts/lifecycle.go")
    supervisor = read("internal/supervisor/supervisor.go")
    jobs = read("internal/jobs/jobs.go")
    readiness = read("internal/readiness/readiness.go")
    doctor = read("internal/doctor/doctor.go")
    diagnostics = read("internal/diagnostics/read.go")
    app = read("module/webroot/app.js")
    install = read("scripts/dev/install_stack.py")
    policy = json.loads((ROOT / "release/final-seal-policy.json").read_text())
    devtool = read(".devtool.toml")
    runtime_tests = read("internal/runtimeauth/runtime_test.go")
    cli_tests = read("cmd/racctl/runtime_test.go")

    managed = runtime[runtime.index("case ModeManaged:"):runtime.index("case ModeExternal:")]
    external_case = runtime[runtime.index("case ModeExternal:"):runtime.index("case ModeMigrationRequired:")]
    external_fn = runtime[runtime.index("func externalExecutable"):runtime.index("func explicitMode")]

    # Exact promise -> executable assertion mapping for Position 1.
    promise("RNX-P279", all(token in runtime for token in ('ModeManaged           Mode = "managed"', 'ModeExternal          Mode = "external"', 'ModeMigrationRequired Mode = "migration-required"')), "explicit three-state runtime mode model missing")
    promise("RNX-P280", all(token in paths for token in ["RuntimeDir", "ManagedRuntimeDir", "ManagedRcloneBin", "ManagedConfigDir", "ManagedRcloneConfig"]) and 'filepath.Join(p.StateDir, "runtime")' in paths and 'filepath.Join(p.ConfigDir, "rclone")' in paths, "canonical Nexus-owned runtime/config roots missing")
    promise("RNX-P281", "return runtimeauth.Executable(p)" in provider and "return runtimeauth.ConfigPath(p)" in provider and "runtimeauth.Resolve(engine.Paths)" in control, "production resolver/facade convergence missing")
    promise("RNX-P282", "result.Config = p.ManagedRcloneConfig" in managed and 'os.Getenv("RCLONE_CONFIG")' not in managed, "managed rclone.conf is not single-authority")
    promise("RNX-P283", 'result.Canonical = false' in external_case and 'result.Source = "external-compatibility"' in external_case and 'exec.LookPath("rclone")' in external_fn, "external-provider compatibility is not explicit/noncanonical")
    promise("RNX-P284", "legacyProviderState" in runtime and 'mode = ModeMigrationRequired' in runtime and 'result.Source = "legacy-provider-detected"' in runtime, "legacy-provider migration detection missing")
    promise("RNX-P285", "legacy_provider_lifecycle_enabled" in runtime and "runtime authority ambiguous" in runtime and 'runtime status --json --require-operational' in service, "boot does not refuse ambiguous dual authority")
    promise("RNX-P286", (ROOT / "docs/implementation/RUNTIME-STANDALONE-X01.md").is_file() and "canonical authority direction" in read("docs/implementation/RUNTIME-STANDALONE-X01.md").lower(), "authority direction is not documented")

    promise("RNX-P287", 'runtime status --json --require-operational' in service and "refusing boot reconcile" in service, "service.sh/boot ingress bypasses resolver")
    promise("RNX-P288", 'case "runtime":' in racctl and "runtimeauth.Resolve(p)" in racctl and "runtime status" in racctl, "normal racctl ingress bypasses resolver")
    promise("RNX-P289", 'engine.register("runtime.status"' in control and "runtimeauth.Resolve(engine.Paths)" in control, "daemon/control ingress bypasses resolver")
    promise("RNX-P290", "provider.FindRclone(p)" in lifecycle and "provider.ConfigPath(p)" in lifecycle, "mount lifecycle bypasses resolver")
    promise("RNX-P291", 'add("runtime_authority", true' in readiness and "readinessTerminalFailure" in supervisor, "supervisor is not driven by canonical runtime readiness")
    promise("RNX-P292", "provider.FindRclone(p)" in jobs and "provider.ConfigPath(p)" in jobs, "jobs bypass resolver")
    promise("RNX-P293", 'add("runtime_authority", true' in readiness and 'add("provider_module", false' in readiness, "readiness/policy still requires legacy provider authority")
    promise("RNX-P294", '"runtime.authority"' in doctor and "provider.Discover(p)" in doctor and "p.ManagedRcloneConfig" in diagnostics, "diagnostics do not consume/sanitize canonical runtime truth")
    promise("RNX-P295", "query('runtime.status')" in app and "Runtime authority:" in app and 'engine.register("runtime.status"' in control, "WebUI API/control path bypasses runtime authority")
    promise("RNX-P296", '"runtime_authority": [str(racctl), "runtime", "status", "--json", "--require-operational"]' in install and "managed runtime does not require a provider module" in install, "install/install-verify does not require the canonical runtime authority to be operational")
    required_node = "runtime-standalone-x01-audit"
    flows_bound = required_node in policy.get("required_ancestor_nodes", []) and "[targets.rclone_nexus.jobs.runtime-standalone-x01-audit]" in devtool and "runtime-standalone-x01 = [" in devtool
    for flow in ["grand-g1-source = [", "release = ["]:
        start = devtool.index(flow); end = devtool.find("\n]", start)
        flows_bound = flows_bound and required_node in devtool[start:end]
    promise("RNX-P297", flows_bound, "release qualification is not bound to Position 1 audit")

    promise("RNX-P298", "return runtimeauth.Executable(p)" in provider and 'filepath.Join(p.ProviderModuleDir, "system", "vendor", "bin", "rclone")' not in provider, "provider-dir runtime lookup remains an independent default authority")
    offenders = []
    for path in (ROOT / "internal").rglob("*.go"):
        if path.name.endswith("_test.go") or path == ROOT / "internal/runtimeauth/runtime.go":
            continue
        if 'exec.LookPath("rclone")' in path.read_text(encoding="utf-8"):
            offenders.append(str(path.relative_to(ROOT)))
    promise("RNX-P299", not offenders and 'exec.LookPath("rclone")' not in managed, f"parallel/PATH-first authorities remain: {offenders}")
    promise("RNX-P300", '"$racctl" runtime executable' in common and '"$racctl" runtime config' in common and "result.Config = p.ManagedRcloneConfig" in managed and "if rnexus_external_compat_requested" in common, "shell compatibility helpers do not project the native runtime/config authority")
    promise("RNX-P301", "LegacyProviderEnabled" in runtime and "AmbiguousAuthority" in runtime and "legacy_provider_lifecycle_enabled" in runtime, "enabled parent lifecycle is not classified as competing authority")
    promise("RNX-P302", "LegacyProviderEnabled" in runtime and "AmbiguousAuthority" in runtime and "legacy_provider_lifecycle_enabled" in runtime, "enabled parent job/autosync authority is not blocked in managed mode")

    promise("RNX-P303", "TestManagedProviderRemovedIsOperational" in runtime_tests and "TestRuntimeStatusCLIUsesManagedAuthorityAndIgnoresPoison" in cli_tests and "TestRuntimeExecutableAndConfigCommandsProjectCanonicalResolver" in cli_tests, "managed/provider-removed production CLI evidence missing")
    promise("RNX-P304", "TestExternalMissingExecutableFailsClosed" in runtime_tests, "external missing-executable negative evidence missing")
    promise("RNX-P305", "TestManagedAndEnabledLegacyProviderIsAmbiguous" in runtime_tests and "TestRuntimeStatusCLIRequiresMigrationForLegacyOnlyState" in cli_tests, "dual-authority/migration negative evidence missing")
    promise("RNX-P306", "TestManagedIgnoresPoisonedPathAndGenericConfig" in runtime_tests, "poisoned PATH negative evidence missing")
    promise("RNX-P307", "TestManagedIgnoresPoisonedPathAndGenericConfig" in runtime_tests and "TestManagedMissingConfigIsNotOperational" in runtime_tests and 'p.RcloneConfig = poisonConfig' in runtime_tests, "poisoned RCLONE_CONFIG/config truth negative evidence missing")


def assert_tests() -> None:
    run(["go", "test", "./internal/runtimeauth", "./cmd/racctl", "./internal/provider", "./internal/mounts", "./internal/jobs", "./internal/readiness", "./internal/doctor", "./internal/control", "./internal/supervisor"])
    run([sys.executable, "-m", "unittest", "tests.test_install_stack"])
    run([sys.executable, "scripts/dev/check_device_runtime_webui_remediation.py"])
    shell = os.environ.get("SHELL") or "/bin/sh"
    run([shell, "-n", "module/lib/common.sh"])
    run([shell, "-n", "module/service.sh"])


def main() -> int:
    assert_scope()
    assert_architecture()
    assert_tests()
    print("RUNTIME-STANDALONE-X01 canonical runtime ownership: PASS")
    print(json.dumps({"promise_range": "RNX-P279..RNX-P307", "count": 29, "status": "IMPLEMENTED_AND_PRODUCTION_ADOPTED"}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
