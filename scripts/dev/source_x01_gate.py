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
POLICY = ROOT / "release/final-seal-policy.json"
ADOPTED = "IMPLEMENTED_AND_PRODUCTION_ADOPTED"


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


def evidence_resolves(ref: str) -> bool:
    ref = ref.strip()
    if "::" in ref:
        path_part, node = ref.split("::", 1)
        path = ROOT / path_part
        return path.is_file() and bool(node.strip()) and node in path.read_text(encoding="utf-8", errors="replace")
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


def assert_scope() -> None:
    data = json.loads(LEDGER.read_text(encoding="utf-8"))
    require(data.get("max_promise_number") == 500 and data.get("promise_count") == 500, "canonical RNX-P001..RNX-P500 scope changed")
    by_id = {item["id"]: item for item in data["items"]}
    for number in range(374, 391):
        pid = f"RNX-P{number:03d}"
        item = by_id.get(pid)
        require(isinstance(item, dict), f"missing SOURCE-X01 promise {pid}")
        require(item.get("status") == ADOPTED, f"{pid} status={item.get('status')} want {ADOPTED}")
        refs = item.get("evidence") or []
        require("source-x01-audit" in refs, f"{pid} is not bound to SOURCE-X01 executable gate")
        for ref in refs:
            require(evidence_resolves(str(ref)), f"{pid} evidence does not resolve: {ref}")
    active = data.get("active_position", {})
    position = int(active.get("position", 0))
    require(position >= 5, "canonical campaign position regressed before SOURCE-X01")
    if position == 5:
        require(active.get("promise_range") == "RNX-P374..RNX-P390", "canonical SOURCE-X01 active range is malformed")
        require(active.get("production_adopted_count") == 17 and active.get("blocked_by_environment_count") == 0, "SOURCE-X01 status counts are stale")


def assert_architecture() -> None:
    source = read("internal/runtimesource/source.go")
    github = read("internal/runtimesource/github.go")
    tests = read("internal/runtimesource/source_test.go")
    store = read("internal/runtimestore/store.go")
    cli = read("cmd/racctl/main.go")
    paths = read("internal/paths/paths.go")
    docs = read("docs/implementation/SOURCE-X01.md")
    g1 = read("scripts/dev/runtime_standalone_g1_gate.py")
    devtool = read(".devtool.toml")
    policy = json.loads(POLICY.read_text(encoding="utf-8"))

    require('Repository: "BenjiThatFoxGuy/bclone"' in source, "RNX-P374 canonical bclone source missing")
    require('Repository: "rclone/rclone"' in source, "RNX-P375 official rclone source missing")
    require('Repository: "NewFuture/rclone-fuse3-magisk"' in source, "RNX-P376 NewFuture source missing")
    require("v1.75.3" not in source + github + cli + docs, "bclone latest is hardcoded to the historical v1.75.3 example")
    for token in ("KindGitHub", "KindURL", "KindLocalBinary", "KindSourceBuild", "KindNewFuture"):
        require(token in source, f"first-class source kind missing: {token}")
    for token in ("ChannelLatestStable", "ChannelPinnedRelease", "ChannelPinnedCommit", "ChannelManualOnly"):
        require(token in source and token in github if token != "ChannelManualOnly" else token in source, f"source channel missing: {token}")
    for token in ("RepositoryID", "ReleaseID", "CommitSHA", "ResolutionID", "RuntimeSourceResolutionsDir"):
        require(token in source or token in paths, f"immutable provenance field missing: {token}")
    require('/releases/assets/' in github and '/releases/latest' in github, "latest resolution is not converted to numeric asset authority")
    require("pinned-commit requires full 40-hex commit SHA" in github, "pinned commit accepts mutable refs")
    require("GitHub metadata redirect escaped trusted API origin" in github, "metadata poisoned redirect defense missing")
    require("GitHub repository identity mismatch" in github, "repository owner/name identity is not verified")
    require("latest-stable resolved to draft or prerelease" in github, "stable channel does not reject prerelease/draft")
    require("release is missing expected asset" in github, "missing expected asset does not fail closed")
    require("runtime source SHA-256 mismatch" in store and "ExpectedSHA256" in store, "resolved expected digest is not enforced before publication")
    require("ImportResolution" in source and "import-resolution" in cli, "persisted resolution does not reach the production X02 import authority")
    require("runtime source register" in cli and "runtime source resolve" in cli, "production registry/resolver CLI ingress missing")
    require("RuntimeSourceRegistry" in paths and "RNEXUS_RUNTIME_SOURCE_REGISTRY" in paths, "canonical source registry paths/env authority missing")

    negatives = (
        "TestLatestStableResolvesToImmutableCommitAndAssetIDAndPersists",
        "TestPinnedReleaseRetargetDoesNotRewritePriorResolution",
        "TestNegativeGitHubCasesFailClosed",
        "TestPoisonedMetadataRedirectIsRejected",
        "TestPinnedCommitRequiresFullImmutableSHA",
        "TestImportRequestUsesImmutableAssetAPIURLNotLatestOrTagURL",
    )
    for token in negatives:
        require(token in tests, f"SOURCE-X01 adversarial regression missing: {token}")

    require('position >= 4' in g1 and 'position == 4' in g1, "sealed RUNTIME-G1 gate still prevents legitimate later campaign positions")
    require("[wrapper.commands.source-x01]" in devtool and "[targets.rclone_nexus.jobs.source-x01-audit]" in devtool and "source-x01 = [" in devtool, "Devtool SOURCE-X01 wrapper/job/workflow missing")
    required = set(policy.get("required_ancestor_nodes", []))
    require("source-x01-audit" in required, "final seal policy is not bound to SOURCE-X01")
    for flow in ("grand-g1-source = [", "release = ["):
        start = devtool.index(flow); end = devtool.find("\n]", start)
        require("source-x01-audit" in devtool[start:end], f"{flow.split()[0]} bypasses SOURCE-X01")


def assert_production_cli() -> None:
    with tempfile.TemporaryDirectory(prefix="rnx-source-x01-") as raw:
        temp = Path(raw)
        racctl = temp / "racctl"
        subprocess.run(["go", "build", "-o", str(racctl), "./cmd/racctl"], cwd=ROOT, check=True)
        state = temp / "state"
        env = os.environ.copy()
        env["RNEXUS_STATE_DIR"] = str(state)
        env["RNEXUS_RUNTIME_SOURCES_DIR"] = str(state / "runtime" / "sources")
        env["RNEXUS_RUNTIME_SOURCE_REGISTRY"] = str(state / "runtime" / "sources" / "registry-v1.json")
        env["RNEXUS_RUNTIME_SOURCE_RESOLUTIONS_DIR"] = str(state / "runtime" / "sources" / "resolutions")
        env["RNEXUS_RUNTIME_STORE_DIR"] = str(state / "runtimes")
        env["RNEXUS_FUSE_DEVICE"] = str(temp / "missing-fuse")

        listed = subprocess.run([str(racctl), "runtime", "source", "list"], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        require(listed.returncode == 0, f"compiled source list failed: {listed.stderr}")
        items = json.loads(listed.stdout)
        by_id = {item["id"]: item for item in items}
        require(by_id["bclone"]["repository"] == "BenjiThatFoxGuy/bclone", "compiled CLI bclone builtin is not canonical")

        candidate = temp / "candidate"
        candidate.write_bytes(bytes([0x7F, ord("E"), ord("L"), ord("F"), 2, 1]))
        candidate.chmod(0o700)
        registered = subprocess.run([str(racctl), "runtime", "source", "register", "--id", "fixture", "--kind", "local-binary", "--path", str(candidate)], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        require(registered.returncode == 0, f"compiled source register failed: {registered.stderr}")
        resolved = subprocess.run([str(racctl), "runtime", "source", "resolve", "fixture"], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        require(resolved.returncode == 0, f"compiled source resolve failed: {resolved.stderr}")
        resolution = json.loads(resolved.stdout)
        rid = resolution.get("resolution_id", "")
        require(rid.startswith("src-") and len(resolution.get("content_sha256", "")) == 64, "compiled local resolution is not immutable")
        inspected = subprocess.run([str(racctl), "runtime", "source", "inspect-resolution", rid], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        require(inspected.returncode == 0 and json.loads(inspected.stdout).get("resolution_id") == rid, "compiled resolution inspection failed integrity check")
        imported = subprocess.run([str(racctl), "runtime", "source", "import-resolution", rid], cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        require(imported.returncode != 0, "truncated local resolution unexpectedly qualified")
        manifest = json.loads(imported.stdout)
        require(manifest.get("source", {}).get("resolution_id") == rid, "runtime manifest lost SOURCE-X01 resolution provenance")


def main() -> int:
    assert_scope()
    assert_architecture()
    run(["go", "test", "./internal/runtimesource", "./internal/runtimestore", "./cmd/racctl"])
    run([sys.executable, "scripts/dev/check_canonical_scope.py"])
    run([sys.executable, "scripts/dev/runtime_standalone_g1_gate.py", "--source-only"])
    assert_production_cli()
    print("SOURCE-X01 source registry + latest/pinned semantics: PASS")
    print(json.dumps({"promise_range":"RNX-P374..RNX-P390","count":17,"production_adopted":17,"blocked_by_environment":0,"status":ADOPTED,"next":"SOURCE-X02"}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
