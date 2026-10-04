#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
LEDGER = ROOT / "release/canonical-promise-ledger.json"
ADOPTED = "IMPLEMENTED_AND_PRODUCTION_ADOPTED"
BLOCKED = "BLOCKED_BY_ENVIRONMENT"
BLOCKED_IDS = {"RNX-P330", "RNX-P335", "RNX-P336", "RNX-P337", "RNX-P338", "RNX-P341"}
PROMOTED_X02 = BLOCKED_IDS


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
    if "/" in ref:
        return (ROOT / ref).is_file()
    try:
        cfg = tomllib.loads(read(".devtool.toml"))
        target = cfg["targets"]["rclone_nexus"]
        jobs = set(target.get("jobs", {}))
        nodes = {str(node.get("id")) for flow in target.get("workflows", {}).values() if isinstance(flow, list) for node in flow if isinstance(node, dict) and node.get("id")}
        return ref in jobs or ref in nodes
    except Exception:
        return False


def promise(pid: str, condition: bool, message: str) -> None:
    require(condition, f"{pid}: {message}")


def assert_scope() -> None:
    data = json.loads(LEDGER.read_text())
    count=int(data.get("promise_count") or 0); maximum=int(data.get("max_promise_number") or 0); items=data.get("items", [])
    require(count>0 and maximum==count and len(items)==count and [x.get("id") for x in items]==[f"RNX-P{i:03d}" for i in range(1,count+1)], "canonical scope is not contiguous")
    by_id = {item["id"]: item for item in data["items"]}
    active = data.get("active_position", {})
    active_position = int(active.get("position", 0))
    require(active_position >= 2, "canonical campaign position regressed before X02")
    promoted = active_position >= 4
    expected = [f"RNX-P{i:03d}" for i in range(308, 349)]
    for pid in expected:
        require(pid in by_id, f"missing canonical X02 promise {pid}")
        item = by_id[pid]
        wanted = ADOPTED if (pid not in BLOCKED_IDS or promoted) else BLOCKED
        require(item.get("status") == wanted, f"{pid} status={item.get('status')} want {wanted}")
        evidence = item.get("evidence") or []
        require(evidence, f"{pid} has no evidence")
        for ref in evidence:
            require(evidence_resolves(str(ref)), f"{pid} evidence does not resolve: {ref}")
        if pid in PROMOTED_X02 and promoted:
            require("runtime-standalone-g1-audit" in evidence, f"{pid} was promoted without RUNTIME-G1 real-device evidence")
    adopted_count = sum(1 for pid in expected if by_id[pid].get("status") == ADOPTED)
    blocked_count = sum(1 for pid in expected if by_id[pid].get("status") == BLOCKED)
    expected_counts = (41, 0) if promoted else (35, 6)
    require((adopted_count, blocked_count) == expected_counts, f"X02 status counts are stale: {(adopted_count, blocked_count)} want {expected_counts}")
    if active_position == 2:
        require(active.get("promise_range") == "RNX-P308..RNX-P348", "canonical X02 active range is malformed")


def assert_architecture() -> None:
    store = read("internal/runtimestore/store.go")
    qualify = read("internal/runtimestore/qualify.go")
    tests = read("internal/runtimestore/store_test.go")
    cli = read("cmd/racctl/main.go")
    cli_tests = read("cmd/racctl/runtime_store_test.go")
    paths = read("internal/paths/paths.go")
    contract = read("internal/mounts/runtime_contract.go")
    docs = read("docs/implementation/RUNTIME-STANDALONE-X02.md")
    devtool = read(".devtool.toml")
    policy = json.loads(read("release/final-seal-policy.json"))

    source_tokens = {
        "RNX-P308": 'SourceLocalFile      SourceType = "local-file"',
        "RNX-P309": 'SourceExecutablePath SourceType = "executable-path"',
        "RNX-P310": 'SourceURL            SourceType = "url"',
        "RNX-P311": 'SourceGitHub         SourceType = "github-release"',
        "RNX-P312": 'SourceBuild          SourceType = "source-build"',
        "RNX-P313": 'SourceNewFuture      SourceType = "newfuture-derived"',
    }
    for pid, token in source_tokens.items():
        promise(pid, token in store and "runtimestore.Import" in cli, "source is not wired through canonical production import")

    manifest_tokens = {
        "RNX-P314": "SchemaVersion",
        "RNX-P315": "RuntimeID",
        "RNX-P316": "Engine",
        "RNX-P317": "VersionOutput",
        "RNX-P318": "Source         SourceProvenance",
        "RNX-P319": "Repository",
        "RNX-P320": "ResolvedRef",
        "RNX-P321": "AssetURL",
        "RNX-P322": "ArchiveSHA256",
        "RNX-P323": "BinarySHA256",
        "RNX-P324": "ELF            ELFMetadata",
        "RNX-P325": "ImportedUnixMS",
        "RNX-P326": "Qualifier",
        "RNX-P327": "Qualification  Qualification",
    }
    for pid, token in manifest_tokens.items():
        promise(pid, token in store, f"manifest field missing: {token}")

    promise("RNX-P328", "inspectELFReader(pinned.file)" in qualify and "candidate is not an executable regular file" in qualify, "valid executable/ELF is not checked against pinned bytes")
    promise("RNX-P329", "architectureCompatible" in qualify and 'hostArch == "arm64"' in qualify, "arm64 device architecture rule missing")
    promise("RNX-P330", "androidDevice()" in qualify and '"android_execution"' in qualify and '"blocked_by_environment"' in qualify, "Android compatibility does not fail closed outside real Android")
    promise("RNX-P331", "versionCheck" in qualify and "versionPattern" in qualify, "version command/output qualification missing")
    promise("RNX-P332", "configEnumerationCheck" in qualify, "config support is not executed")
    promise("RNX-P333", 'runPinned(probe, binary, 512<<10, "mount", "--help")' in qualify, "mount command is not executed/probed")
    promise("RNX-P334", "RuntimeQualificationArgv" in qualify and "RuntimeQualificationArgv" in contract and "helpContainsFlag" in qualify, "generated global/command flag contract missing")
    promise("RNX-P335", "rcReady" in qualify and '"/core/version"' in qualify and '"rc_runtime"' in qualify, "live RC qualification path missing")
    promise("RNX-P336", "fuseSmoke" in qualify and "mountPresent" in qualify and '"probe.txt"' in qualify, "real temporary FUSE smoke path missing")
    promise("RNX-P337", "syscall.SIGTERM" in qualify and '"signal_termination"' in qualify, "signal/termination qualification missing")
    promise("RNX-P338", 'processExe := fmt.Sprintf("/proc/%d/exe"' in qualify and 'hashFile(processExe)' in qualify and '"process_ownership"' in qualify, "process ownership capture does not bind the actual process executable inode")
    promise("RNX-P339", '"listremotes"' in qualify and '"nexus_qual:"' in qualify, "config remote enumeration missing")
    promise("RNX-P340", "sanitizeDetail" in qualify and "secretPattern" in qualify and "TestDiagnosticsSanitizationRedactsSecretsAndBoundsOutput" in tests, "diagnostics sanitization missing")

    promise("RNX-P341", "real Android/FUSE qualification" in qualify and "RNX-P341" in docs, "Linux-arm64-vs-Android/FUSE adversarial boundary not dispositioned")
    promise("RNX-P342", "TestTruncatedELFFailsClosed" in tests, "truncated ELF adversarial test missing")
    promise("RNX-P343", "TestWrongArchitectureRuleFailsArm64Android" in tests, "wrong architecture adversarial test missing")
    promise("RNX-P344", "TestFakeVersionAndUnsupportedFlagAreRejected" in tests and 'versionPattern.MatchString("totally legit runtime")' in tests, "fake version adversarial test missing")
    promise("RNX-P345", "TestFakeVersionAndUnsupportedFlagAreRejected" in tests and '"--rc-pass"' in tests, "unsupported global flag adversarial test missing")
    promise("RNX-P346", "TestCommandSpecificAndGlobalHelpAreMerged" in tests and '"help", "flags"' in qualify, "command/global help split regression missing")
    promise("RNX-P347", "TestProductionQualificationRejectsBinaryDisappearanceDuringRun" in tests and "verifyPostQualificationHash" in qualify, "binary disappearance is not exercised through production qualification")
    promise("RNX-P348", "TestProductionQualificationRejectsPathReplacementButPinnedLaunchUsesOriginal" in tests and '"/proc/self/fd/3"' in qualify and "ExtraFiles" in qualify, "hash-to-launch TOCTOU defense missing")

    require("RuntimeStoreDir" in paths and 'filepath.Join(p.StateDir, "runtimes")' in paths, "canonical runtime store path missing")
    require('case "import":' in cli and 'case "inspect":' in cli and 'case "test":' in cli and 'case "list":' in cli, "racctl does not expose production runtime store/qualifier")
    require("TestRuntimeImportCLIStoresEvidenceAndFailsClosed" in cli_tests, "normal CLI fail-closed import evidence missing")
    require("func Acquire(ctx context.Context, p paths.Paths, raw ImportRequest)" in store and "State:            \"pending\"" in store, "acquisition boundary does not publish explicit pending qualification state")
    require("func Import(ctx context.Context, p paths.Paths, raw ImportRequest)" in store and "return Test(ctx, p, manifest.RuntimeID)" in store, "normal import no longer forces explicit qualification after acquisition")
    require("func Test(ctx context.Context, p paths.Paths, id string)" in store and "manifest.Qualification = Qualify(ctx, p, manifest)" in store, "explicit qualification boundary is not wired to the canonical qualifier")
    require("TestAcquirePublishesPendingCandidateWithoutRunningQualifier" in tests, "acquire-only pending-candidate regression missing")
    require("cleanURL" in store and "u.User = nil" in store and 'u.RawQuery = ""' in store, "URL provenance can retain credentials/query")
    require("SourceNewFuture" in store and "NewFuture/rclone-fuse3-magisk" in store, "NewFuture-derived intake is not first class")
    require("does **not** change" in docs and "X03 owns activation" in docs, "X02 accidentally claims X03 activation authority")
    require("ManagedRcloneBin" not in store + qualify and "runtimeauth" not in store + qualify, "X02 runtime store bypasses X03 by writing/selecting active runtime authority")

    required = "runtime-standalone-x02-audit"
    require(required in policy.get("required_ancestor_nodes", []), "final seal policy is not bound to X02")
    require("[targets.rclone_nexus.jobs.runtime-standalone-x02-audit]" in devtool and "runtime-standalone-x02 = [" in devtool, "Devtool X02 job/workflow missing")
    for flow in ("grand-g1-source = [", "release = ["):
        start = devtool.index(flow)
        end = devtool.find("\n]", start)
        require(required in devtool[start:end], f"{flow.split()[0]} bypasses X02")


def assert_production_cli() -> None:
    # Exercise the same compiled racctl ingress a user invokes, rather than
    # treating direct calls to cmd/racctl.run from unit tests as final proof.
    with tempfile.TemporaryDirectory(prefix="rnx-x02-cli-") as raw:
        temp = Path(raw)
        racctl = temp / "racctl"
        subprocess.run(["go", "build", "-o", str(racctl), "./cmd/racctl"], cwd=ROOT, check=True)
        candidate = temp / "truncated-rclone"
        candidate.write_bytes(bytes([0x7F, ord("E"), ord("L"), ord("F"), 2, 1]))
        candidate.chmod(0o700)
        state = temp / "state"
        env = os.environ.copy()
        env.update({
            "RNEXUS_STATE_DIR": str(state),
            "RNEXUS_RUNTIME_STORE_DIR": str(state / "runtimes"),
            "RNEXUS_RUNTIME_DIR": str(state / "runtime"),
            "RNEXUS_CONFIG_DIR": str(state / "config"),
            "RNEXUS_PROVIDER_MODULE_DIR": str(temp / "provider-absent"),
            "RNEXUS_FUSE_DEVICE": str(temp / "fake-fuse"),
        })
        proc = subprocess.run(
            [str(racctl), "runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", str(candidate)],
            cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
        )
        require(proc.returncode != 0, "compiled racctl production import accepted a truncated ELF")
        try:
            manifest = json.loads(proc.stdout)
        except json.JSONDecodeError as exc:
            fail(f"compiled racctl production import did not emit JSON evidence: {exc}: {proc.stdout!r}")
        runtime_id = manifest.get("runtime_id", "")
        require(runtime_id, "compiled racctl production import emitted no runtime ID")
        manifest_path = state / "runtimes" / runtime_id / "manifest.json"
        binary_path = state / "runtimes" / runtime_id / "rclone"
        require(manifest_path.is_file() and binary_path.is_file(), "compiled racctl import did not persist physical candidate/evidence")
        require(manifest.get("qualification", {}).get("qualified") is False, "compiled racctl import manufactured qualified=true")

        inspected = subprocess.run([str(racctl), "runtime", "inspect", runtime_id], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        require(inspected.returncode == 0, f"compiled racctl inspect failed: {inspected.stderr}")
        inspected_manifest = json.loads(inspected.stdout)
        require(inspected_manifest.get("binary_sha256") == manifest.get("binary_sha256"), "compiled racctl inspect did not resolve the stored bytes")

        listed = subprocess.run([str(racctl), "runtime", "list"], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        require(listed.returncode == 0, f"compiled racctl list failed: {listed.stderr}")
        items = json.loads(listed.stdout)
        require(any(item.get("runtime_id") == runtime_id for item in items), "compiled racctl list does not project the imported candidate")


def assert_tests() -> None:
    run(["go", "test", "./internal/runtimestore", "./cmd/racctl", "./internal/mounts", "./internal/paths", "./internal/provider"])
    run([sys.executable, "scripts/dev/check_canonical_scope.py"])
    assert_production_cli()


def main() -> int:
    assert_scope()
    assert_architecture()
    assert_tests()
    data = json.loads(LEDGER.read_text())
    promoted = int(data.get("active_position", {}).get("position", 0)) >= 4
    adopted = 41 if promoted else 35
    blocked = [] if promoted else sorted(BLOCKED_IDS)
    print("RUNTIME-STANDALONE-X02 runtime store + qualification: PASS")
    print(json.dumps({
        "promise_range": "RNX-P308..RNX-P348",
        "count": 41,
        "production_adopted": adopted,
        "blocked_by_environment": blocked,
        "status": "IMPLEMENTED_AND_PRODUCTION_ADOPTED" if promoted else "PARTIALLY_ADOPTED",
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
