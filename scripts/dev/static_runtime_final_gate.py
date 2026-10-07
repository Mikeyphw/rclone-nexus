#!/usr/bin/env python3
"""Final source/package seal for the singular bundled-runtime product contract."""

from __future__ import annotations

import argparse
import json
import sys
import tomllib
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DIST_ZIP = ROOT / "dist" / "rclone-nexus-v0.1.0.zip"

RETIRED_PACKAGES = (
    "runtimeactivation",
    "runtimeupdate",
    "runtimeacquire",
    "runtimestore",
    "runtimesource",
    "runtimemanager",
    "runtimebuild",
    "runtimestate",
)

DYNAMIC_OPERATIONS = (
    "runtime.manager",
    "runtime.activate",
    "runtime.rollback",
    "runtime.recover",
    "runtime.activation.status",
    "runtime.candidates",
    "runtime.source.",
    "runtime.update.",
)


def fail(message: str) -> None:
    raise SystemExit(f"static-runtime final seal: FAIL: {message}")


def read(rel: str) -> str:
    return (ROOT / rel).read_text(encoding="utf-8")


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def source_contract() -> None:
    for package in RETIRED_PACKAGES:
        require(not any((ROOT / "internal" / package).glob("*.go")), f"retired package still has compiled Go source: internal/{package}")

    runtimeauth = read("internal/runtimeauth/runtime.go")
    require('"system", "bin", "rclone"' in runtimeauth, "runtimeauth does not resolve module/system/bin/rclone")
    require("ModeExternal" not in runtimeauth and "MigrationRequired" not in runtimeauth, "runtimeauth still carries alternate runtime modes")

    paths = read("internal/paths/paths.go")
    for token in (
        "RuntimeActivationState",
        "RuntimeActivationLock",
        "RuntimeStoreDir",
        "RuntimeSourcesDir",
        "RuntimeUpdateDir",
        "ManagedRcloneBin",
    ):
        require(token not in paths, f"retired mutable-runtime path survives: {token}")

    service = read("module/service.sh")
    require("runtime status --json --require-operational" in service, "boot does not gate on canonical runtime status")
    require("runtime recover" not in service and "boot-activate" not in service, "boot still owns mutable runtime lifecycle")

    racctl = read("cmd/racctl/main.go")
    control = read("internal/control/engine.go")
    app = read("module/webroot/app.js")
    model = read("module/webroot/model.js")
    production_surface = "\n".join((racctl, control, app, model))
    for operation in DYNAMIC_OPERATIONS:
        require(operation not in production_surface, f"dynamic runtime operation leaked into product surface: {operation}")
    require("byId('runtimeEngineManager')" in app, "static runtime UI does not target the shipped runtime status container")
    require("runtime-status-hero" in app and "runtime-manager" not in app, "static runtime UI still carries Runtime Manager presentation state")
    require("innerHTML" not in app, "static runtime UI reintroduced unsafe DOM mutation")
    for package in RETIRED_PACKAGES:
        require(f"internal/{package}" not in racctl + control, f"retired package imported by production ingress: {package}")

    mounts = read("internal/mounts/lifecycle.go") + read("internal/mounts/registry.go")
    migration = read("internal/migration/migration.go")
    require("runtimestate" not in mounts, "mount lifecycle still reads runtime activation state")
    require("runtimestate" not in migration, "migration still reads runtime activation state")
    require("runtimeauth.Executable" in migration, "migration evidence is not bound to the bundled runtime")

    package_py = read("scripts/dev/package_module.py")
    customize = read("module/customize.sh")
    require("RNEXUS_RCLONE_PREBUILT" in package_py, "packager has no explicit prebuilt runtime input")
    require('entries["system/bin/rclone"]' in package_py, "packager does not write system/bin/rclone")
    require('system/bin/rclone' in customize, "installer does not require bundled runtime")


def devtool_contract() -> None:
    cfg = tomllib.loads(read(".devtool.toml"))
    wrapper = cfg.get("wrapper", {}).get("commands", {})
    current = wrapper.get("static-runtime-finalize")
    require(isinstance(current, dict), "static-runtime-finalize wrapper is missing")
    require(current.get("target") == "rclone_static_runtime", "static-runtime-finalize wrapper targets the wrong EXO target")
    require(current.get("workflow") == "final-seal", "static-runtime-finalize wrapper targets the wrong workflow")

    retired_workflows = {
        "runtime-standalone-x01",
        "runtime-standalone-x02",
        "runtime-standalone-x03",
        "runtime-standalone-g1-source",
        "runtime-standalone-g1-device",
        "runtime-standalone-g1",
        "source-x01",
        "source-x02",
        "update-x01",
        "source-g1",
        "migrate-x01",
        "ux-x01",
        "runtime-preseal",
        "runtime-grand-g1-a",
    }
    retired_jobs = {
        "runtime-standalone-x01-audit",
        "runtime-standalone-x02-audit",
        "runtime-standalone-x03-audit",
        "runtime-standalone-g1-source-audit",
        "runtime-standalone-g1-device-evidence",
        "runtime-standalone-g1-audit",
        "runtime-standalone-g1-validation-cleanup",
        "source-x01-audit",
        "source-x02-audit",
        "update-x01-audit",
        "source-g1-source-audit",
        "source-g1-device-evidence",
        "source-g1-audit",
        "source-g1-validation-cleanup",
        "migrate-x01-audit",
        "ux-x01-audit",
        "runtime-preseal-adoption-audit",
        "runtime-grand-g1-device-source-audit",
        "runtime-grand-g1-device-capture",
        "runtime-grand-g1-device-validate",
    }
    for retired in retired_workflows:
        require(retired not in wrapper, f"retired mutable-runtime wrapper remains public: {retired}")

    target = cfg.get("targets", {}).get("rclone_static_runtime")
    require(isinstance(target, dict), "rclone_static_runtime target is missing")
    require(target.get("execution_environment") == "auto", "static runtime target must resolve execution environment through DevTool EXO")
    jobs = target.get("jobs", {})
    required_jobs = {
        "surface-contract",
        "go-authority",
        "webui-static",
        "shell-syntax",
        "module-contract",
        "go-repository",
        "package-contract",
        "artifact-audit",
        "final-audit",
    }
    require(required_jobs.issubset(jobs), f"static runtime EXO jobs incomplete: {sorted(required_jobs - set(jobs))}")
    workflows = target.get("workflows", {})
    artifact = workflows.get("artifact-seal")
    require(isinstance(artifact, list) and len(artifact) == 7, "artifact-seal workflow DAG must contain exactly seven repository-read-only stages")
    artifact_by_id = {node.get("id"): node for node in artifact if isinstance(node, dict)}
    require(set(artifact_by_id) == {"surface", "authority", "webui-static", "shell-syntax", "module-contract", "repository", "seal"}, "artifact-seal DAG stage set drifted")
    require(artifact_by_id["repository"].get("depends_on") == ["authority"], "artifact-seal: repository gate must depend on focused authority")
    require(set(artifact_by_id["seal"].get("depends_on", ())) == {"surface", "webui-static", "repository", "shell-syntax", "module-contract"}, "artifact-seal is not dependency-gated on all source/repository checks")
    require(artifact_by_id["seal"].get("ref") == "job:artifact-audit", "artifact-seal must use the read-only artifact audit")
    for node in artifact:
        ref = str(node.get("ref") or "")
        require(ref.startswith("job:"), "artifact-seal may contain only target-local jobs")
        require(bool(jobs[ref.removeprefix("job:")].get("metadata", {}).get("read_only")), f"artifact-seal node is not read-only: {ref}")

    final = workflows.get("final-seal")
    require(isinstance(final, list) and len(final) == 8, "final-seal workflow DAG must contain exactly eight native stages")
    final_by_id = {node.get("id"): node for node in final if isinstance(node, dict)}
    require(set(final_by_id) == {"surface", "authority", "webui-static", "shell-syntax", "module-contract", "repository", "package", "seal"}, "final-seal DAG stage set drifted")
    require(final_by_id["repository"].get("depends_on") == ["authority"], "final-seal: repository gate must depend on focused authority")
    require(set(final_by_id["package"].get("depends_on", ())) == {"repository", "shell-syntax", "module-contract"}, "final-seal package gate dependencies drifted")
    require(set(final_by_id["seal"].get("depends_on", ())) == {"surface", "webui-static", "package"}, "final-seal is not dependency-gated on product surface, WebUI and package")
    require(final_by_id["package"].get("ref") == "job:package-contract", "final-seal must execute the package contract")
    require(final_by_id["seal"].get("ref") == "job:final-audit", "final-seal must finish with the source/product audit")
    package_job = jobs.get("package-contract", {})
    require("--ephemeral" in package_job.get("command", []), "static-runtime package contract must be ephemeral")
    require(bool(package_job.get("metadata", {}).get("read_only")), "static-runtime package contract must be declared read-only")
    require(not package_job.get("outputs"), "static-runtime package contract must not claim dist outputs")

    main = cfg.get("targets", {}).get("rclone_nexus", {})
    main_jobs = main.get("jobs", {})
    main_workflows = main.get("workflows", {})
    main_package_contract = main_jobs.get("package-contract", {})
    require("--ephemeral" in main_package_contract.get("command", []), "main package-contract must be ephemeral and must not overwrite release artifacts")
    require(bool(main_package_contract.get("metadata", {}).get("read_only")), "main package-contract must be declared read-only")
    require(not main_package_contract.get("outputs"), "main package-contract must not claim dist outputs")
    leaked_jobs = sorted(retired_jobs.intersection(main_jobs))
    leaked_workflows = sorted(retired_workflows.intersection(main_workflows))
    require(not leaked_jobs, f"retired mutable-runtime jobs remain in live DevTool graph: {leaked_jobs}")
    require(not leaked_workflows, f"retired mutable-runtime workflows remain in live DevTool graph: {leaked_workflows}")

    for workflow_name in ("grand-g1-source", "release"):
        nodes = main_workflows.get(workflow_name)
        require(isinstance(nodes, list), f"{workflow_name} workflow is missing")
        node = next((item for item in nodes if isinstance(item, dict) and item.get("id") == "static-runtime-final-seal"), None)
        require(node is not None, f"{workflow_name} does not integrate the static runtime final seal")
        require(node.get("ref") == "target:rclone_static_runtime#final-seal", f"{workflow_name} static seal is not a cross-target EXO workflow ref")

    declared_tests = {item.get("id") for item in cfg.get("test", []) if isinstance(item, dict)}
    required_artifact_tests = {
        "static-runtime-artifact-surface",
        "static-runtime-artifact-authority",
        "static-runtime-artifact-webui",
        "static-runtime-artifact-module",
        "static-runtime-artifact-audit",
    }
    require(required_artifact_tests.issubset(declared_tests), f"DevTool artifact TestSpec coverage is incomplete: {sorted(required_artifact_tests - declared_tests)}")
    require("static-runtime-artifact-repository" not in declared_tests, "repository-wide Go suite must remain outside atomic apply validation")
    require(jobs.get("go-repository", {}).get("command") == ["go", "test", "-count=1", "./..."], "post-apply EXO repository gate is missing or weakened")

    final_web = read("scripts/dev/check_webui_final.mjs")
    require("check_static_runtime_ux.mjs" in final_web, "final WebUI contract does not execute static runtime UX gate")
    require("check_runtime_manager_ux.mjs" not in final_web, "final WebUI contract still executes retired Runtime Manager gate")
    require(not (ROOT / "scripts/dev/check_runtime_manager_ux.mjs").exists(), "retired Runtime Manager UX checker still exists")


def package_contract(require_package: bool) -> None:
    # Source/artifact validation must be independent of any stale pre-existing
    # dist ZIP. Only an explicit --require-package invocation is allowed to
    # inspect the repository release artifact.
    if not require_package:
        return
    if not DIST_ZIP.exists():
        fail(f"required package missing: {DIST_ZIP.relative_to(ROOT)}")

    with zipfile.ZipFile(DIST_ZIP) as zf:
        names = zf.namelist()
        runtime_names = [name for name in names if Path(name).name == "rclone"]
        require(runtime_names == ["system/bin/rclone"], f"package must contain exactly one rclone runtime, got {runtime_names}")
        forbidden = [name for name in names if Path(name).name in {"fusermount", "fusermount3", "libfuse.so", "libfuse3.so"}]
        require(not forbidden, f"package unexpectedly owns FUSE/provider payloads: {forbidden}")
        require("integrity.manifest.json" in names, "package has no integrity manifest")
        manifest = json.loads(zf.read("integrity.manifest.json"))
        entries = manifest.get("entries", manifest)
        if isinstance(entries, dict):
            covered = set(entries)
        elif isinstance(entries, list):
            covered = {str(item.get("path")) for item in entries if isinstance(item, dict)}
        else:
            covered = set()
        require("system/bin/rclone" in covered, "integrity manifest does not cover bundled runtime")
        info = zf.getinfo("system/bin/rclone")
        mode = (info.external_attr >> 16) & 0o777
        require(mode & 0o111, f"bundled runtime is not executable in package (mode={mode:o})")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--require-package", action="store_true")
    args = parser.parse_args()

    source_contract()
    devtool_contract()
    package_contract(args.require_package)
    print("static-runtime final seal: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
