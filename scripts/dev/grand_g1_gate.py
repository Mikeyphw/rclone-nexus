#!/usr/bin/env python3
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import tomllib
from zipfile import ZipFile

ROOT = Path(__file__).resolve().parents[2]
POLICY = ROOT / "release" / "final-seal-policy.json"
REQUIREMENTS = ROOT / "release" / "roadmap-requirements.json"
CANONICAL = ROOT / "release" / "canonical-promise-ledger.json"
ROADMAP = ROOT / "docs" / "ROADMAP.md"
MODULE_PROP = ROOT / "module" / "module.prop"
DIST = ROOT / "dist"
EXPECTED_CASES = [
    "reboot",
    "root_manager_restart",
    "provider_update_reload",
    "wifi_mobile_offline",
    "doze_screenoff_charging",
    "remote_outage_auth_recovery",
    "stale_fuse_killed_rclone",
    "daemon_crash_restart",
    "storage_remount_low_space",
    "webui_reopen_idle_expiry",
    "android_user_namespace_change",
    "simultaneous_mounts_jobs",
]
EXPECTED_IMPL_DOCS = [
    "CORE-X01", "LIFE-X01", "LIFE-X02", "CORE-G1",
    "ANDROID-X01", "POLICY-X01", "RUNTIME-X01", "ANDROID-G1",
    "PLATFORM-X01", "PLATFORM-G1",
    "WEB-X01", "WEB-X02", "WEB-X03", "WEB-G1",
    "REL-X01", "GRAND-G1",
]


def fail(message: str) -> "None":
    raise SystemExit(message)


def load_json(path: Path) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot read JSON {path}: {exc}")
    if not isinstance(value, dict):
        fail(f"JSON root must be an object: {path}")
    return value


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def props() -> dict[str, str]:
    out: dict[str, str] = {}
    for line in MODULE_PROP.read_text(encoding="utf-8").splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            out[key] = value
    return out


def roadmap_promises() -> list[str]:
    text = ROADMAP.read_text(encoding="utf-8")
    start = text.find("# Promise ledger")
    end = text.find("# Current campaign position", start)
    if start < 0 or end < 0:
        fail("ROADMAP promise ledger/current-position boundary missing")
    promises: list[str] = []
    for line in text[start:end].splitlines():
        match = re.match(r"\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|", line)
        if not match:
            continue
        promise = match.group(1).strip()
        if promise in {"Promise", "---"} or set(promise) <= {"-", ":"}:
            continue
        promises.append(promise)
    if len(promises) != 24:
        fail(f"ROADMAP promise ledger expected 24 promises, found {len(promises)}")
    return promises


def roadmap_requirement_bullets() -> list[tuple[int, str]]:
    text = ROADMAP.read_text(encoding="utf-8")
    matches = list(re.finditer(r"^## (\d+)/16 — ([^\n]+)$", text, re.MULTILINE))
    out: list[tuple[int, str]] = []
    for index, match in enumerate(matches):
        position = int(match.group(1))
        end = matches[index + 1].start() if index + 1 < len(matches) else text.find("\n---", match.end())
        section = text[match.end():end]
        out.extend((position, line[2:].strip()) for line in section.splitlines() if line.startswith("- "))
    return out

def validate_requirement_matrix(closure: set[str], final: str) -> int:
    matrix = load_json(REQUIREMENTS)
    items = matrix.get("requirements")
    if matrix.get("schema_version") != 1 or not isinstance(items, list):
        fail("roadmap requirement matrix is malformed")
    expected = roadmap_requirement_bullets()
    actual = [(int(item.get("position", 0)), str(item.get("requirement", ""))) for item in items if isinstance(item, dict) and int(item.get("position", 0)) != 16]
    if actual != expected:
        fail("roadmap requirement matrix does not exactly cover every position-level bullet")
    grand = [item for item in items if isinstance(item, dict) and item.get("id") == "P16-R01"]
    if len(grand) != 1:
        fail("roadmap requirement matrix lacks GRAND-G1 narrative requirement")
    seen: set[str] = set()
    for item in items:
        if not isinstance(item, dict): fail("roadmap requirement entry must be an object")
        rid = str(item.get("id", "")); nodes = item.get("evidence_nodes")
        if not rid or rid in seen: fail(f"duplicate/missing roadmap requirement id: {rid!r}")
        seen.add(rid)
        if not isinstance(nodes, list) or not nodes: fail(f"roadmap requirement lacks executable evidence: {rid}")
        absent = [str(n) for n in nodes if str(n) != final and str(n) not in closure]
        if absent: fail(f"roadmap requirement evidence is not an ancestor of GRAND-G1: {rid}: {absent}")
    if matrix.get("requirement_count") != len(items): fail("roadmap requirement count is stale")
    return len(items)

def resolve_canonical_evidence(ref: str) -> bool:
    ref = ref.strip()
    if not ref:
        return False
    if "::" in ref:
        path_part, node = ref.split("::", 1)
        path = ROOT / path_part
        return path.is_file() and bool(node.strip()) and node in path.read_text(encoding="utf-8", errors="replace")
    if "/" in ref:
        return (ROOT / ref).is_file()
    if ref.startswith("tests."):
        return (ROOT / (ref.replace(".", "/") + ".py")).is_file()
    graph, refs = workflow_graph()
    jobs = set()
    try:
        config = tomllib.loads((ROOT / ".devtool.toml").read_text(encoding="utf-8"))
        jobs = set(config["targets"]["rclone_nexus"].get("jobs", {}))
    except (KeyError, TypeError):
        pass
    return ref in jobs or ref in graph or ref in refs.values()


def validate_canonical_scope() -> tuple[dict, list[dict]]:
    ledger = load_json(CANONICAL)
    items = ledger.get("items")
    if ledger.get("schema_version") != 1 or ledger.get("campaign") != "RUNTIME-STANDALONE" or not isinstance(items, list):
        fail("canonical merged promise ledger identity mismatch")
    count=int(ledger.get("promise_count") or 0); maximum=int(ledger.get("max_promise_number") or 0)
    if count <= 0 or maximum != count or len(items) != count:
        fail(f"canonical merged promise ledger size malformed: max={maximum} count={count} items={len(items)}")
    expected = [f"RNX-P{i:03d}" for i in range(1, count + 1)]
    if [str(item.get("id", "")) for item in items if isinstance(item, dict)] != expected:
        fail(f"canonical merged promise IDs are not contiguous RNX-P001..RNX-P{maximum:03d}")
    terminal = {"IMPLEMENTED_AND_PRODUCTION_ADOPTED", "EXPORTED", "SUPERSEDED", "RETIRED"}
    adopted = "IMPLEMENTED_AND_PRODUCTION_ADOPTED"
    for item in items:
        source_ref = str(item.get("source_ref", ""))
        source_path = re.sub(r":L\d+$", "", source_ref.split("#", 1)[0].strip())
        if not source_path or not (ROOT / source_path).is_file():
            fail(f"canonical promise source_ref does not resolve: {item.get('id')}: {source_ref}")
        evidence = item.get("evidence", [])
        if not isinstance(evidence, list):
            fail(f"canonical promise evidence must be a list: {item.get('id')}")
        if item.get("status") == adopted and not evidence:
            fail(f"production-adopted canonical promise has no evidence: {item.get('id')}")
        if item.get("status") == adopted:
            for ref in evidence:
                if not resolve_canonical_evidence(str(ref)):
                    fail(f"canonical promise evidence does not resolve: {item.get('id')}: {ref}")
    open_items = [item for item in items if isinstance(item, dict) and str(item.get("status", "")) not in terminal]
    return ledger, open_items


def workflow_graph() -> tuple[dict[str, set[str]], dict[str, str]]:
    config = tomllib.loads((ROOT / ".devtool.toml").read_text(encoding="utf-8"))
    try:
        workflow = config["targets"]["rclone_nexus"]["workflows"]["release"]
    except (KeyError, TypeError):
        fail("canonical rclone_nexus release workflow is missing")
    graph: dict[str, set[str]] = {}
    refs: dict[str, str] = {}
    if not isinstance(workflow, list):
        fail("release workflow must be a list")
    for node in workflow:
        if not isinstance(node, dict):
            fail("release workflow nodes must be objects")
        node_id = str(node.get("id", "")).strip()
        ref = str(node.get("ref", "")).strip()
        deps = node.get("depends_on", [])
        if not node_id or node_id in graph or not ref or not isinstance(deps, list):
            fail(f"invalid release workflow node: {node!r}")
        graph[node_id] = {str(item) for item in deps}
        refs[node_id] = ref
    for node_id, deps in graph.items():
        missing = sorted(deps - graph.keys())
        if missing:
            fail(f"release workflow node {node_id} has unknown dependencies: {missing}")
    return graph, refs


def ancestors(graph: dict[str, set[str]], node: str) -> set[str]:
    if node not in graph:
        fail(f"release workflow final node missing: {node}")
    seen: set[str] = set()
    stack = list(graph[node])
    while stack:
        current = stack.pop()
        if current in seen:
            continue
        seen.add(current)
        stack.extend(graph[current])
    return seen


def validate_policy() -> tuple[dict, list[str], set[str], int, dict, list[dict]]:
    policy = load_json(POLICY)
    if policy.get("schema_version") != 1 or policy.get("campaign_position") != "GRAND-G1":
        fail("final seal policy identity mismatch")
    promises = policy.get("promises")
    if not isinstance(promises, list):
        fail("final seal policy promises must be a list")
    policy_names = [str(item.get("promise", "")) for item in promises if isinstance(item, dict)]
    roadmap_names = roadmap_promises()
    if policy_names != roadmap_names:
        fail("final seal policy does not exactly cover the ROADMAP promise ledger")

    graph, refs = workflow_graph()
    final = str(policy.get("final_node", ""))
    if refs.get(final) != "job:grand-g1-audit":
        fail("release workflow does not terminate at job:grand-g1-audit")
    closure = ancestors(graph, final)
    required = {str(item) for item in policy.get("required_ancestor_nodes", [])}
    missing = sorted(required - closure)
    if missing:
        fail("GRAND-G1 final node is missing required workflow ancestors: " + ", ".join(missing))
    for item in promises:
        if not isinstance(item, dict):
            fail("final seal promise entry must be an object")
        nodes = item.get("evidence_nodes", [])
        if not isinstance(nodes, list) or not nodes:
            fail(f"promise has no executable/evidence nodes: {item.get('promise')}")
        absent = [str(node) for node in nodes if str(node) != final and str(node) not in closure]
        if absent:
            fail(f"promise evidence is not an ancestor of GRAND-G1: {item.get('promise')}: {absent}")
    requirement_count = validate_requirement_matrix(closure, final)
    if policy.get("canonical_promise_ledger") != "release/canonical-promise-ledger.json" or policy.get("canonical_roadmap_obligation_dispositions") != "release/canonical-roadmap-obligation-dispositions.json" or policy.get("current_campaign") != "RUNTIME-STANDALONE":
        fail("legacy GRAND-G1 policy is not bound to the canonical merged campaign ledger/disposition compiler")
    ledger, open_items = validate_canonical_scope()
    return policy, policy_names, closure, requirement_count, ledger, open_items


def validate_docs() -> None:
    for name in EXPECTED_IMPL_DOCS:
        path = ROOT / "docs" / "implementation" / f"{name}.md"
        if not path.is_file():
            fail(f"missing implementation closure document: {path.relative_to(ROOT)}")
    roadmap = ROADMAP.read_text(encoding="utf-8")
    roadmap_flat = " ".join(roadmap.split())
    required = [
        "GRAND-G1 implemented (16/16)",
        "campaign becomes sealed only when",
        "one release workflow consuming package, tests and captured device evidence",
    ]
    for token in required:
        if " ".join(token.split()).lower() not in roadmap_flat.lower():
            fail(f"ROADMAP final closure missing: {token}")
    release_docs = [
        ROOT / "README.md",
        ROOT / "CHANGELOG.md",
        *sorted((ROOT / "docs" / "release").glob("*.md")),
        *[ROOT / "docs" / "implementation" / f"{name}.md" for name in EXPECTED_IMPL_DOCS],
    ]
    marker = re.compile(r"\b(?:TODO|FIXME|TBD|XXX)\b|<placeholder>", re.IGNORECASE)
    for path in release_docs:
        match = marker.search(path.read_text(encoding="utf-8"))
        if match:
            fail(f"unresolved release placeholder marker {match.group(0)!r} in {path.relative_to(ROOT)}")


def validate_device_evidence(path: Path) -> dict[str, int]:
    script = ROOT / "scripts" / "dev" / "release_device_qualification.py"
    spec = importlib.util.spec_from_file_location("rnexus_release_device_qualification", script)
    if spec is None or spec.loader is None:
        fail("cannot load executable release-device qualification authority")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    try:
        counts = module.validate(path, True)
    except SystemExit as exc:
        fail(f"device evidence rejected by executable qualification authority: {exc}")
    if not isinstance(counts, dict):
        fail("device qualification authority returned malformed counts")
    return {"pass": int(counts.get("pass", 0)), "skip": int(counts.get("skip", 0))}


def source_digest() -> str:
    path = ROOT / "scripts" / "dev" / "release_artifacts.py"
    spec = importlib.util.spec_from_file_location("rnexus_release_artifacts", path)
    if spec is None or spec.loader is None:
        fail("cannot load release_artifacts source-digest authority")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return str(module.source_digest())


def validate_release_artifact() -> dict:
    meta = props()
    if meta.get("id") != "rclone_nexus" or meta.get("version") != "v0.1.0":
        fail("module.prop final identity must be rclone_nexus v0.1.0")
    archive = DIST / "rclone-nexus-v0.1.0.zip"
    sums = DIST / "SHA256SUMS"
    manifest_path = DIST / "release-manifest.json"
    for path in (archive, sums, manifest_path):
        if not path.is_file():
            fail(f"missing release artifact: {path.relative_to(ROOT)}")
    digest = sha256_file(archive)
    if sums.read_text(encoding="utf-8").strip() != f"{digest}  {archive.name}":
        fail("SHA256SUMS does not identify the final release artifact")
    manifest = load_json(manifest_path)
    if manifest.get("schema_version") != 1 or manifest.get("module_id") != "rclone_nexus" or manifest.get("version") != "v0.1.0" or manifest.get("evidence_schema") != 3:
        fail("release manifest identity/evidence-schema mismatch")
    artifacts = manifest.get("artifacts")
    if not isinstance(artifacts, list) or len(artifacts) != 1:
        fail("release manifest must contain exactly one artifact")
    item = artifacts[0]
    if item.get("name") != archive.name or item.get("sha256") != digest or item.get("size") != archive.stat().st_size:
        fail("release manifest artifact identity mismatch")
    current_source = source_digest()
    if manifest.get("source_digest") != current_source:
        fail("release manifest source digest does not match sealed source tree")
    with ZipFile(archive) as zf:
        names = set(zf.namelist())
        if "integrity.manifest.json" not in names or "system/bin/racctl" not in names:
            fail("release package lacks integrity manifest or racctl")
        module_prop = zf.read("module.prop").decode("utf-8")
        if "id=rclone_nexus\n" not in module_prop or "version=v0.1.0\n" not in module_prop:
            fail("release package module identity mismatch")
        forbidden = sorted(n for n in names if n.rstrip("/").split("/")[-1] in {"rclone", "fusermount", "fusermount3"})
        if forbidden:
            fail("release package bundles provider runtime: " + ", ".join(forbidden))
    return {
        "name": archive.name,
        "sha256": digest,
        "size": archive.stat().st_size,
        "source_digest": current_source,
        "manifest_sha256": sha256_file(manifest_path),
    }


def run_source_only() -> None:
    policy, promises, closure, requirement_count, ledger, open_items = validate_policy()
    validate_docs()
    if open_items:
        fail(f"legacy GRAND-G1 seal invalidated by active {ledger['campaign']} scope: {len(open_items)} of {ledger['promise_count']} canonical promises remain open; range RNX-P001..RNX-P{ledger['max_promise_number']:03d}")
    print("GRAND-G1 source/readiness audit: PASS")
    print(json.dumps({
        "status": "ready-for-real-device-qualification",
        "legacy_summary_promise_count": len(promises),
        "canonical_promise_count": ledger["promise_count"],
        "roadmap_requirement_count": requirement_count,
        "workflow_ancestor_count": len(closure),
        "policy_schema": policy["schema_version"],
    }, sort_keys=True))


def run(evidence: Path, output: Path) -> None:
    policy, promises, closure, requirement_count, ledger, open_items = validate_policy()
    validate_docs()
    if open_items:
        fail(f"refusing legacy GRAND-G1 seal: active {ledger['campaign']} scope has {len(open_items)} open canonical promises")
    counts = validate_device_evidence(evidence)
    artifact = validate_release_artifact()
    verdict = {
        "schema_version": 1,
        "status": "sealed",
        "campaign_position": "GRAND-G1",
        "version": "v0.1.0",
        "module_id": "rclone_nexus",
        "sealed_at": datetime.now(timezone.utc).isoformat(),
        "release_artifact": {
            "name": artifact["name"],
            "sha256": artifact["sha256"],
            "size": artifact["size"],
        },
        "source_digest": artifact["source_digest"],
        "release_manifest_sha256": artifact["manifest_sha256"],
        "device_evidence_sha256": sha256_file(evidence),
        "endurance": {"total": len(EXPECTED_CASES), **counts},
        "legacy_summary_promise_count": len(promises),
        "canonical_promise_count": ledger["promise_count"],
        "roadmap_requirement_count": requirement_count,
        "workflow_ancestor_count": len(closure),
        "policy_schema": policy["schema_version"],
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(verdict, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print("GRAND-G1 final release seal: PASS")
    print(json.dumps(verdict, sort_keys=True))


def default_evidence() -> Path:
    primary = os.environ.get("DEVTOOL_TRANSACTION_PRIMARY_REPO_ROOT", "").strip()
    root = Path(primary).expanduser().resolve() if primary else ROOT
    return root / "release" / "evidence" / "device-qualification.json"


def main() -> None:
    parser = argparse.ArgumentParser(description="GRAND-G1 authoritative final release seal")
    parser.add_argument("--evidence", type=Path, default=None)
    parser.add_argument("--output", type=Path, default=DIST / "release-verdict.json")
    parser.add_argument(
        "--source-only",
        action="store_true",
        help="audit GRAND-G1 source/workflow readiness without consuming private real-device evidence or emitting a seal",
    )
    args = parser.parse_args()
    if args.source_only:
        run_source_only()
        return
    evidence = args.evidence.expanduser().resolve() if args.evidence else default_evidence()
    run(evidence, args.output)


if __name__ == "__main__":
    main()
