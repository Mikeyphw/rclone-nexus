from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
from zipfile import ZipFile

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "dev" / "runtime_inputs.py"


def load_module():
    spec = importlib.util.spec_from_file_location("runtime_inputs", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def make_archive(path: Path, *, runtime: bytes = b"newfuture-rclone", helper: bytes = b"newfuture-fusermount3") -> None:
    with ZipFile(path, "w") as zf:
        zf.writestr("system/vendor/bin/rclone", runtime)
        zf.writestr("system/vendor/bin/fusermount3", helper)
        zf.writestr("system/vendor/lib64/libfuse3.so.3", b"newfuture-libfuse3")


def run_materialize(tmp_path: Path, provider: str, *, runtime_prebuilt: Path | None = None) -> Path:
    archive = tmp_path / "magisk-rclone_arm64-v8a.zip"
    make_archive(archive)
    out = tmp_path / "out"
    env = os.environ.copy()
    env["RNEXUS_ALLOW_RUNTIME_TEST_FIXTURES"] = "1"
    cmd = [
        sys.executable,
        str(SCRIPT),
        "materialize",
        "--provider",
        provider,
        "--output-dir",
        str(out),
        "--newfuture-archive",
        str(archive),
        "--newfuture-tag",
        "v-fixture",
    ]
    if runtime_prebuilt is not None:
        cmd += ["--runtime-prebuilt", str(runtime_prebuilt)]
    subprocess.run(cmd, cwd=ROOT, env=env, check=True, stdout=subprocess.PIPE, text=True)
    return out


def test_newfuture_provider_materializes_runtime_and_fuse_payload(tmp_path: Path):
    out = run_materialize(tmp_path, "newfuture")
    assert (out / "rclone").read_bytes() == b"newfuture-rclone"
    assert (out / "newfuture/system/vendor/bin/fusermount3").read_bytes() == b"newfuture-fusermount3"
    assert (out / "newfuture/system/vendor/lib64/libfuse3.so.3").read_bytes() == b"newfuture-libfuse3"
    provenance = json.loads((out / "runtime-provenance.json").read_text())
    assert provenance["runtime"]["provider"] == "newfuture"
    assert provenance["runtime"]["repository"] == "NewFuture/rclone-fuse3-magisk"
    assert provenance["fuse_helper"]["repository"] == "NewFuture/rclone-fuse3-magisk"
    assert provenance["fuse_helper"]["provider_invariant"] == "newfuture"
    assert provenance["fuse_helper"]["helper_package_path"] == "system/vendor/bin/fusermount3"


def test_bclone_provider_still_uses_newfuture_fusermount3(tmp_path: Path):
    bclone = tmp_path / "bclone"
    bclone.write_bytes(b"bclone-runtime")
    bclone.chmod(0o755)
    out = run_materialize(tmp_path, "bclone", runtime_prebuilt=bclone)
    provenance = json.loads((out / "runtime-provenance.json").read_text())
    assert (out / "rclone").read_bytes() == b"bclone-runtime"
    assert provenance["runtime"]["provider"] == "bclone"
    assert provenance["runtime"]["repository"] == "BenjiThatFoxGuy/bclone"
    assert provenance["fuse_helper"]["repository"] == "NewFuture/rclone-fuse3-magisk"
    assert (out / "newfuture/system/vendor/bin/fusermount3").is_file()


def test_verify_detects_runtime_tampering(tmp_path: Path):
    out = run_materialize(tmp_path, "newfuture")
    (out / "rclone").write_bytes(b"tampered")
    cp = subprocess.run(
        [sys.executable, str(SCRIPT), "verify", "--runtime-dir", str(out)],
        cwd=ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    assert cp.returncode != 0
    assert "runtime bytes do not match provenance" in cp.stderr


def test_archive_selection_accepts_current_newfuture_arm64_name_and_rejects_ambiguity(tmp_path: Path):
    mod = load_module()
    release = {
        "assets": [
            {"name": "magisk-rclone_arm64-v8a.zip", "browser_download_url": "https://github.com/example/a"},
            {"name": "magisk-rclone_x86_64.zip", "browser_download_url": "https://github.com/example/b"},
        ]
    }
    assert mod.select_newfuture_asset(release)["name"] == "magisk-rclone_arm64-v8a.zip"
    release["assets"].append({"name": "magisk-rclone-arm64-v8a.zip", "browser_download_url": "https://github.com/example/c"})
    try:
        mod.select_newfuture_asset(release)
    except mod.ProvisionError as exc:
        assert "exactly one" in str(exc)
    else:
        raise AssertionError("ambiguous NewFuture arm64 assets were accepted")


def test_source_contract_pins_authoritative_repositories_and_builds_bclone_from_source():
    text = SCRIPT.read_text()
    assert 'NEWFUTURE_REPOSITORY = "NewFuture/rclone-fuse3-magisk"' in text
    assert 'BCLONE_REPOSITORY = "BenjiThatFoxGuy/bclone"' in text
    assert '"git", "clone"' in text
    assert 'runtime_source_build.py' in text
    assert '"--engine", "bclone"' in text
    assert 'provider_invariant": "newfuture"' in text


def test_devtool_current_product_builds_do_not_depend_on_legacy_mutable_runtime_suite():
    text = (ROOT / ".devtool.toml").read_text(encoding="utf-8")
    assert '[targets.rclone_nexus.jobs.legacy-unit-tests]' in text
    assert '[wrapper.commands.legacy-tests]' in text
    # Current product qualification is explicit: the default unit job must not
    # rediscover historical campaign tests that reference deleted activation,
    # store, update, and runtime-manager sources.
    start = text.index('[targets.rclone_nexus.jobs.unit-tests]')
    end = text.index('[targets.rclone_nexus.jobs.legacy-unit-tests]')
    current = text[start:end]
    assert 'unittest", "discover"' not in current
    assert 'tests.test_nexus' in current
    assert 'tests.test_install_stack' in current
    # Both shipping build workflows must cross the current static-runtime gate
    # before materializing provider inputs. Historical audit tests remain
    # separately runnable but are not a dependency of package production.
    flows = text[text.index('[targets.rclone_nexus.workflows]'):text.index('[targets.rclone_webui]')]
    for name in ('package = [', 'package-bclone = ['):
        block = flows[flows.index(name):]
        block = block[: block.index(']\n') + 2]
        assert 'target:rclone_static_runtime#quick' in block
        assert 'job:legacy-unit-tests' not in block
