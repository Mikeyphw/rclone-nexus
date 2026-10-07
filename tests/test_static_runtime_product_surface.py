from pathlib import Path
import tomllib

ROOT = Path(__file__).resolve().parents[1]
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
    "runtime.source.",
    "runtime.update.",
    "runtime.candidates",
    "runtime.activation.status",
)
RETIRED_WORKFLOWS = {
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
RETIRED_JOBS = {
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


def read(rel: str) -> str:
    return (ROOT / rel).read_text(encoding="utf-8")


def test_webui_is_read_only_static_runtime_status_surface():
    app = read("module/webroot/app.js")
    model = read("module/webroot/model.js")
    html = read("module/webroot/index.html")
    css = read("module/webroot/style.css")
    assert "query('runtime.status')" in app
    assert "byId('runtimeEngineManager')" in app
    assert "Bundled Runtime" in app
    assert "runtime-status-hero" in app
    assert "runtime-manager" not in app
    assert "Runtime Manager" not in app
    assert "innerHTML" not in app
    assert 'id="runtimeEngineManager"' in html
    assert ".runtime-status-hero" in css
    combined = app + "\n" + model
    for operation in DYNAMIC_OPERATIONS:
        assert operation not in combined


def test_dynamic_runtime_packages_are_retired_from_compiled_tree():
    for package in RETIRED_PACKAGES:
        assert not any((ROOT / "internal" / package).glob("*.go"))

    racctl = read("cmd/racctl/main.go")
    control = read("internal/control/engine.go")
    combined = racctl + "\n" + control
    for package in RETIRED_PACKAGES:
        assert f"internal/{package}" not in combined
    assert 'engine.register("runtime.status"' in control
    assert 'engine.register("runtime.manager"' not in control


def test_runtime_paths_no_longer_model_activation_store_or_update_state():
    paths = read("internal/paths/paths.go")
    for token in (
        "RuntimeActivationState",
        "RuntimeActivationLock",
        "RuntimeStoreDir",
        "RuntimeTransactionsDir",
        "RuntimeSourcesDir",
        "RuntimeSourceRegistry",
        "RuntimeUpdateDir",
        "RuntimeUpdatePolicy",
        "RuntimeUpdateState",
        "ManagedRuntimeDir",
        "ManagedRcloneBin",
    ):
        assert token not in paths


def test_mount_and_migration_paths_cannot_be_redirected_by_legacy_activation_state():
    mounts = read("internal/mounts/lifecycle.go") + read("internal/mounts/registry.go")
    migration = read("internal/migration/migration.go")
    assert "runtimestate" not in mounts
    assert "runtimestate" not in migration
    assert "runtimeauth.Executable" in migration


def test_boot_has_no_mutable_runtime_lifecycle():
    service = read("module/service.sh")
    assert "runtime status --json --require-operational" in service
    assert "runtime recover" not in service
    assert "runtime update boot-activate" not in service
    assert "RunRuntimeUpdateScheduler" not in read("cmd/racctl/main.go")
    assert not (ROOT / "internal/daemon/runtime_update_scheduler.go").exists()


def test_packaging_contract_requires_exact_bundled_runtime():
    package_py = read("scripts/dev/package_module.py")
    inputs_py = read("scripts/dev/runtime_inputs.py")
    package_gate = read("scripts/dev/check-package.py")
    release_py = read("scripts/dev/release_artifacts.py")
    release_gate = read("scripts/dev/check_release.py")
    customize = read("module/customize.sh")
    combined = "\n".join((package_py, inputs_py, package_gate, release_py, release_gate, customize))
    assert "system/bin/rclone" in combined
    assert "system/vendor/bin/fusermount3" in combined
    assert "runtime.provenance.json" in combined
    assert "NewFuture/rclone-fuse3-magisk" in inputs_py
    assert "BenjiThatFoxGuy/bclone" in inputs_py
    assert "newfuture" in inputs_py and "bclone" in inputs_py and "prebuilt" in inputs_py
    assert "RNEXUS_RCLONE_PREBUILT" in inputs_py
    assert "release packaging requires RNEXUS_RCLONE_PREBUILT" not in release_py
    assert "provider_invariant" in inputs_py
    assert "NewFuture-derived system/vendor/bin/fusermount3" in customize

def test_devtool_exposes_native_static_runtime_exo_campaign():
    cfg = tomllib.loads(read(".devtool.toml"))
    commands = cfg["wrapper"]["commands"]
    assert commands["static-runtime-finalize"] == {
        "description": "Run the singular bundled-runtime EXO final-seal workflow",
        "workflow": "final-seal",
        "target": "rclone_static_runtime",
    }
    assert RETIRED_WORKFLOWS.isdisjoint(commands)

    assert commands["runtime-newfuture"]["target"] == "rclone_runtime_inputs"
    assert commands["runtime-newfuture"]["workflow"] == "newfuture"
    assert commands["runtime-bclone"]["target"] == "rclone_runtime_inputs"
    assert commands["runtime-bclone"]["workflow"] == "bclone"
    assert commands["build-newfuture"]["workflow"] == "package"
    assert commands["build-bclone"]["workflow"] == "package-bclone"
    assert commands["release-newfuture"]["workflow"] == "release"
    assert commands["release-bclone"]["workflow"] == "release-bclone"

    runtime_inputs = cfg["targets"]["rclone_runtime_inputs"]
    assert runtime_inputs["execution_environment"] == "auto"
    assert set(runtime_inputs["jobs"]) == {"contract", "newfuture", "bclone", "verify-newfuture", "verify-bclone"}
    assert [n["id"] for n in runtime_inputs["workflows"]["newfuture"]] == ["contract", "materialize", "verify"]
    assert [n["id"] for n in runtime_inputs["workflows"]["bclone"]] == ["contract", "materialize", "verify"]

    main = cfg["targets"]["rclone_nexus"]
    for workflow_name, provider_workflow in (("package", "newfuture"), ("package-bclone", "bclone"), ("release", "newfuture"), ("release-bclone", "bclone")):
        by_id = {node["id"]: node for node in main["workflows"][workflow_name]}
        assert by_id["runtime-inputs"]["ref"] == f"target:rclone_runtime_inputs#{provider_workflow}"

    target = cfg["targets"]["rclone_static_runtime"]
    assert target["execution_environment"] == "auto"
    assert set(target["jobs"]) == {
        "surface-contract",
        "go-authority",
        "shell-syntax",
        "module-contract",
        "go-repository",
        "package-contract",
        "artifact-audit",
        "final-audit",
        "webui-static",
    }
    artifact = target["workflows"]["artifact-seal"]
    assert len(artifact) == 7
    artifact_by_id = {node["id"]: node for node in artifact}
    assert set(artifact_by_id) == {"surface", "authority", "webui-static", "shell-syntax", "module-contract", "repository", "seal"}
    assert artifact_by_id["repository"]["depends_on"] == ["authority"]
    assert set(artifact_by_id["seal"]["depends_on"]) == {"surface", "webui-static", "repository", "shell-syntax", "module-contract"}
    assert artifact_by_id["seal"]["ref"] == "job:artifact-audit"
    assert all(target["jobs"][node["ref"].removeprefix("job:")]["metadata"]["read_only"] is True for node in artifact)

    final = target["workflows"]["final-seal"]
    assert len(final) == 8
    final_by_id = {node["id"]: node for node in final}
    assert set(final_by_id) == {"surface", "authority", "webui-static", "shell-syntax", "module-contract", "repository", "package", "seal"}
    assert final_by_id["repository"]["depends_on"] == ["authority"]
    assert set(final_by_id["package"]["depends_on"]) == {"repository", "shell-syntax", "module-contract"}
    assert set(final_by_id["seal"]["depends_on"]) == {"surface", "webui-static", "package"}
    assert final_by_id["package"]["ref"] == "job:package-contract"
    assert final_by_id["seal"]["ref"] == "job:final-audit"
    test_specs = {item["id"]: item for item in cfg.get("test", [])}
    expected_artifact_tests = {
        "static-runtime-artifact-surface",
        "static-runtime-artifact-authority",
        "static-runtime-artifact-webui",
        "static-runtime-artifact-module",
        "static-runtime-artifact-audit",
        "runtime-inputs-artifact-contract",
    }
    assert expected_artifact_tests.issubset(test_specs)
    assert "static-runtime-artifact-repository" not in test_specs
    assert target["jobs"]["go-repository"]["command"] == ["go", "test", "-count=1", "./..."]


def test_live_devtool_graph_has_no_retired_mutable_runtime_campaigns():
    cfg = tomllib.loads(read(".devtool.toml"))
    main = cfg["targets"]["rclone_nexus"]
    assert RETIRED_JOBS.isdisjoint(main["jobs"])
    assert RETIRED_WORKFLOWS.isdisjoint(main["workflows"])
    for workflow_name in ("grand-g1-source", "release"):
        by_id = {node["id"]: node for node in main["workflows"][workflow_name]}
        assert by_id["static-runtime-final-seal"]["ref"] == "target:rclone_static_runtime#final-seal"


def test_final_web_contract_uses_static_runtime_gate_only():
    final = read("scripts/dev/check_webui_final.mjs")
    assert "check_static_runtime_ux.mjs" in final
    assert "check_runtime_manager_ux.mjs" not in final
    assert not (ROOT / "scripts/dev/check_runtime_manager_ux.mjs").exists()
