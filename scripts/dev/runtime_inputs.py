#!/usr/bin/env python3
"""Materialize the immutable build-time runtime inputs for Rclone Nexus.

This is deliberately a *build-time* source resolver. It does not install, stage,
activate, or switch live runtimes. A completed output directory contains exactly
one rclone-family runtime plus the NewFuture FUSE helper payload that packaging
will copy into the flashable module.

Providers:
  newfuture  - use the rclone binary shipped by NewFuture/rclone-fuse3-magisk
  bclone     - build BenjiThatFoxGuy/bclone from the latest stable source tag
  prebuilt   - advanced/offline rclone-family override; FUSE helper is still
               sourced from a NewFuture archive

The fusermount3 authority is invariant: it always comes from a NewFuture
rclone-fuse3-magisk release/archive, regardless of the selected runtime provider.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import urllib.parse
import urllib.request
from zipfile import BadZipFile, ZipFile

ROOT = Path(__file__).resolve().parents[2]
NEWFUTURE_REPOSITORY = "NewFuture/rclone-fuse3-magisk"
BCLONE_REPOSITORY = "BenjiThatFoxGuy/bclone"
NEWFUTURE_ASSET_RE = re.compile(r"^magisk-rclone[_-]arm64-v8a\.zip$")
COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
PROVENANCE_NAME = "runtime-provenance.json"


class ProvisionError(RuntimeError):
    pass


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        while chunk := fh.read(1024 * 1024):
            h.update(chunk)
    return h.hexdigest()


def run(argv: list[str], *, cwd: Path | None = None, env: dict[str, str] | None = None) -> str:
    cp = subprocess.run(argv, cwd=cwd, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if cp.returncode:
        detail = (cp.stdout or "") + (cp.stderr or "")
        raise ProvisionError(f"command failed ({cp.returncode}): {' '.join(argv)}\n{detail[-6000:]}")
    return cp.stdout.strip()


def github_token() -> str:
    for key in ("RNEXUS_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"):
        token = os.environ.get(key, "").strip()
        if token:
            return token
    gh = shutil.which("gh")
    if gh:
        try:
            cp = subprocess.run([gh, "auth", "token"], text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=15)
        except (OSError, subprocess.TimeoutExpired):
            cp = None
        if cp is not None and cp.returncode == 0 and cp.stdout.strip():
            return cp.stdout.strip()
    return ""


class SafeRedirect(urllib.request.HTTPRedirectHandler):
    _allowed = {
        "api.github.com",
        "github.com",
        "objects.githubusercontent.com",
        "release-assets.githubusercontent.com",
        "codeload.github.com",
    }

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[override]
        parsed = urllib.parse.urlparse(newurl)
        if parsed.scheme != "https" or (parsed.hostname or "").lower() not in self._allowed:
            raise ProvisionError(f"refusing download redirect outside trusted GitHub hosts: {newurl}")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def github_json(repository: str, endpoint: str) -> dict:
    url = f"https://api.github.com/repos/{repository}/{endpoint.lstrip('/')}"
    headers = {
        "Accept": "application/vnd.github+json",
        "User-Agent": "rclone-nexus-runtime-inputs",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    token = github_token()
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, headers=headers)
    opener = urllib.request.build_opener(SafeRedirect())
    try:
        with opener.open(req, timeout=30) as response:
            value = json.load(response)
    except Exception as exc:
        raise ProvisionError(f"GitHub metadata request failed for {repository}/{endpoint}: {exc}") from exc
    if not isinstance(value, dict):
        raise ProvisionError("GitHub metadata response was not an object")
    return value


def download(url: str, output: Path) -> None:
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme != "https" or (parsed.hostname or "").lower() not in SafeRedirect._allowed:
        raise ProvisionError(f"refusing untrusted download URL: {url}")
    headers = {"User-Agent": "rclone-nexus-runtime-inputs"}
    token = github_token()
    if token and parsed.hostname in {"api.github.com", "github.com"}:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(url, headers=headers)
    opener = urllib.request.build_opener(SafeRedirect())
    output.parent.mkdir(parents=True, exist_ok=True)
    try:
        with opener.open(req, timeout=120) as response, output.open("wb") as fh:
            shutil.copyfileobj(response, fh, length=1024 * 1024)
    except Exception as exc:
        try:
            output.unlink()
        except FileNotFoundError:
            pass
        raise ProvisionError(f"download failed: {exc}") from exc


def latest_release(repository: str) -> dict:
    release = github_json(repository, "releases/latest")
    tag = str(release.get("tag_name") or "").strip()
    if not tag:
        raise ProvisionError(f"{repository} latest release has no tag")
    if release.get("draft") or release.get("prerelease"):
        raise ProvisionError(f"{repository} latest release is not stable")
    return release


def release_by_tag(repository: str, tag: str) -> dict:
    return github_json(repository, f"releases/tags/{urllib.parse.quote(tag, safe='')}")


def resolve_commit(repository: str, ref: str) -> str:
    data = github_json(repository, f"commits/{urllib.parse.quote(ref, safe='')}")
    commit = str(data.get("sha") or "").lower()
    if not COMMIT_RE.fullmatch(commit):
        raise ProvisionError(f"{repository} ref {ref!r} did not resolve to an immutable commit")
    return commit


def select_newfuture_asset(release: dict) -> dict:
    candidates = [
        item for item in release.get("assets", [])
        if isinstance(item, dict) and NEWFUTURE_ASSET_RE.fullmatch(str(item.get("name") or ""))
    ]
    if len(candidates) != 1:
        names = [str(item.get("name") or "") for item in release.get("assets", []) if isinstance(item, dict)]
        raise ProvisionError(f"expected exactly one NewFuture arm64 module asset, found {len(candidates)} among {names}")
    asset = candidates[0]
    url = str(asset.get("browser_download_url") or "")
    if not url:
        raise ProvisionError("NewFuture arm64 asset has no browser_download_url")
    return asset


def _normalized_member(name: str) -> PurePosixPath:
    p = PurePosixPath(name)
    if p.is_absolute() or ".." in p.parts:
        raise ProvisionError(f"unsafe archive path: {name}")
    parts = tuple(part for part in p.parts if part not in ("", "."))
    if not parts:
        raise ProvisionError("empty archive member")
    return PurePosixPath(*parts)


def _pick_member(zf: ZipFile, basename: str, preferred_suffixes: tuple[str, ...]) -> str:
    members = []
    for info in zf.infolist():
        if info.is_dir():
            continue
        path = _normalized_member(info.filename)
        if path.name == basename:
            members.append(path.as_posix())
    if not members:
        raise ProvisionError(f"NewFuture archive is missing {basename}")
    for suffix in preferred_suffixes:
        matched = [name for name in members if name.endswith(suffix)]
        if len(matched) == 1:
            return matched[0]
    if len(members) == 1:
        return members[0]
    raise ProvisionError(f"NewFuture archive has ambiguous {basename} members: {members}")


def _map_fuse_payload_path(name: str) -> str:
    p = _normalized_member(name)
    text = p.as_posix()
    # Preserve NewFuture's system/vendor layout when present. Normalize bare
    # vendor paths into the equivalent Magisk system/vendor projection.
    if text.startswith("system/"):
        return text
    if text.startswith("vendor/"):
        return "system/" + text
    if p.name == "fusermount3":
        return "system/vendor/bin/fusermount3"
    if p.name.startswith("libfuse"):
        return "system/vendor/lib64/" + p.name
    raise ProvisionError(f"unsupported NewFuture FUSE payload path: {name}")


def arm64_elf(path: Path) -> bool:
    try:
        header = path.read_bytes()[:20]
    except OSError:
        return False
    if len(header) < 20 or header[:4] != b"\x7fELF":
        return False
    little = header[5] == 1
    if header[5] not in (1, 2):
        return False
    machine = int.from_bytes(header[18:20], "little" if little else "big")
    return machine == 183  # EM_AARCH64


def require_arm64(path: Path, label: str, *, allow_fixture: bool) -> None:
    if path.stat().st_size <= 0:
        raise ProvisionError(f"{label} is empty: {path}")
    if allow_fixture:
        return
    if not arm64_elf(path):
        raise ProvisionError(f"{label} is not an AArch64 ELF: {path}")


def newfuture_archive(args: argparse.Namespace, temp: Path) -> tuple[Path, dict]:
    local = str(args.newfuture_archive or os.environ.get("RNEXUS_NEWFUTURE_ARCHIVE", "")).strip()
    explicit_tag = str(args.newfuture_tag or os.environ.get("RNEXUS_NEWFUTURE_TAG", "")).strip()
    if local:
        archive = Path(local).expanduser().resolve()
        if not archive.is_file():
            raise ProvisionError(f"NewFuture archive does not exist: {archive}")
        return archive, {
            "repository": NEWFUTURE_REPOSITORY,
            "release_tag": explicit_tag or "local-archive",
            "release_id": 0,
            "asset_id": 0,
            "asset_name": archive.name,
            "asset_url": "",
            "archive_sha256": sha256_file(archive),
            "acquisition": "local-official-archive",
        }

    release = release_by_tag(NEWFUTURE_REPOSITORY, explicit_tag) if explicit_tag else latest_release(NEWFUTURE_REPOSITORY)
    asset = select_newfuture_asset(release)
    archive = temp / str(asset["name"])
    download(str(asset["browser_download_url"]), archive)
    return archive, {
        "repository": NEWFUTURE_REPOSITORY,
        "release_tag": str(release.get("tag_name") or ""),
        "release_id": int(release.get("id") or 0),
        "asset_id": int(asset.get("id") or 0),
        "asset_name": str(asset.get("name") or ""),
        "asset_url": str(asset.get("browser_download_url") or ""),
        "archive_sha256": sha256_file(archive),
        "acquisition": "github-release",
    }


def extract_newfuture_payload(archive: Path, output: Path, *, need_runtime: bool, allow_fixture: bool) -> dict:
    output.mkdir(parents=True, exist_ok=True)
    try:
        with ZipFile(archive) as zf:
            helper_member = _pick_member(
                zf,
                "fusermount3",
                ("system/vendor/bin/fusermount3", "vendor/bin/fusermount3", "system/bin/fusermount3", "bin/fusermount3"),
            )
            runtime_member = None
            if need_runtime:
                runtime_member = _pick_member(
                    zf,
                    "rclone",
                    ("system/vendor/bin/rclone", "vendor/bin/rclone", "system/bin/rclone", "bin/rclone"),
                )

            fuse_members: list[str] = [helper_member]
            for info in zf.infolist():
                if info.is_dir():
                    continue
                p = _normalized_member(info.filename)
                if p.name.startswith("libfuse") and ".so" in p.name:
                    fuse_members.append(p.as_posix())

            copied: list[dict] = []
            seen_dest: set[str] = set()
            for member in sorted(set(fuse_members)):
                dest_rel = _map_fuse_payload_path(member)
                if dest_rel in seen_dest:
                    raise ProvisionError(f"NewFuture FUSE payload maps multiple archive entries to {dest_rel}")
                seen_dest.add(dest_rel)
                data = zf.read(member)
                dest = output / "newfuture" / dest_rel
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_bytes(data)
                # Binaries and shared libraries must be traversable/readable by Android.
                dest.chmod(0o755 if dest.name == "fusermount3" else 0o644)
                copied.append({"archive_path": member, "package_path": dest_rel, "sha256": sha256_bytes(data), "size": len(data)})

            helper = output / "newfuture" / _map_fuse_payload_path(helper_member)
            require_arm64(helper, "NewFuture fusermount3", allow_fixture=allow_fixture)

            runtime_info = None
            if runtime_member is not None:
                data = zf.read(runtime_member)
                runtime = output / "rclone"
                runtime.write_bytes(data)
                runtime.chmod(0o755)
                require_arm64(runtime, "NewFuture rclone", allow_fixture=allow_fixture)
                runtime_info = {
                    "archive_path": runtime_member,
                    "sha256": sha256_bytes(data),
                    "size": len(data),
                }
            return {
                "runtime": runtime_info,
                "helper": next(item for item in copied if item["package_path"].endswith("/fusermount3")),
                "fuse_payload": copied,
            }
    except BadZipFile as exc:
        raise ProvisionError(f"NewFuture archive is not a valid ZIP: {archive}") from exc


def discover_ndk() -> dict:
    # Reuse the repository's already-qualified Termux/Linux NDK discovery. It
    # supports native Termux clang + an official Linux NDK sysroot when the
    # NDK's x86_64 host binaries cannot execute on the phone.
    sys.path.insert(0, str(ROOT / "scripts" / "dev"))
    try:
        import source_g1_device  # type: ignore
    except Exception as exc:
        raise ProvisionError(f"cannot load repository Android NDK discovery: {exc}") from exc
    probe = source_g1_device.find_ndk()
    if probe.get("supported") is not True:
        raise ProvisionError("bclone Android build requires a runnable NDK: " + str(probe.get("reason") or "unavailable"))
    return probe


def build_bclone(args: argparse.Namespace, temp: Path, output: Path, *, allow_fixture: bool) -> dict:
    prebuilt = str(args.runtime_prebuilt or os.environ.get("RNEXUS_RCLONE_PREBUILT", "")).strip()
    if prebuilt:
        source = Path(prebuilt).expanduser().resolve()
        if not source.is_file():
            raise ProvisionError(f"runtime prebuilt does not exist: {source}")
        shutil.copy2(source, output)
        output.chmod(0o755)
        require_arm64(output, "prebuilt bclone runtime", allow_fixture=allow_fixture)
        return {
            "provider": "bclone",
            "repository": BCLONE_REPOSITORY,
            "requested_ref": "prebuilt",
            "resolved_commit": "",
            "release_tag": "prebuilt",
            "build": "prebuilt-override",
            "sha256": sha256_file(output),
            "size": output.stat().st_size,
        }

    requested = str(args.bclone_ref or os.environ.get("RNEXUS_BCLONE_REF", "")).strip()
    release = release_by_tag(BCLONE_REPOSITORY, requested) if requested else latest_release(BCLONE_REPOSITORY)
    tag = str(release.get("tag_name") or "").strip()
    if not tag:
        raise ProvisionError("bclone stable release has no tag")
    commit = resolve_commit(BCLONE_REPOSITORY, tag)
    source = temp / "bclone-source"
    clone_url = f"https://github.com/{BCLONE_REPOSITORY}.git"
    run(["git", "clone", "--depth", "1", "--branch", tag, "--single-branch", clone_url, str(source)])
    head = run(["git", "rev-parse", "HEAD"], cwd=source).lower()
    if head != commit:
        raise ProvisionError(f"bclone checkout HEAD {head} does not match GitHub-resolved {commit}")
    if run(["git", "status", "--porcelain"], cwd=source):
        raise ProvisionError("bclone checkout is unexpectedly dirty")

    ndk = discover_ndk()
    built = temp / "bclone-build"
    argv = [
        sys.executable,
        str(ROOT / "scripts" / "dev" / "runtime_source_build.py"),
        "build",
        "--source-dir", str(source),
        "--output-dir", str(built),
        "--repository", BCLONE_REPOSITORY,
        "--requested-ref", tag,
        "--resolved-commit", commit,
        "--source-id", "bclone",
        "--engine", "bclone",
        "--ndk", str(ndk["ndk"]),
        "--ndk-version", str(ndk["version"]),
        "--ndk-host", str(ndk["host"]),
        "--compiler", str(ndk.get("compiler") or ""),
        "--compiler-mode", str(ndk.get("compiler_mode") or "ndk-prebuilt"),
        "--compiler-resource-dir", str(ndk.get("compiler_resource_dir") or ""),
        "--api-level", "21",
        "--binary-name", "bclone-android-arm64",
    ]
    run(argv, cwd=ROOT)
    binary = built / "bclone-android-arm64"
    if not binary.is_file():
        raise ProvisionError("bclone builder did not emit bclone-android-arm64")
    shutil.copy2(binary, output)
    output.chmod(0o755)
    require_arm64(output, "built bclone runtime", allow_fixture=allow_fixture)
    build_prov = json.loads((built / "provenance.json").read_text(encoding="utf-8"))
    return {
        "provider": "bclone",
        "repository": BCLONE_REPOSITORY,
        "requested_ref": tag,
        "resolved_commit": commit,
        "release_tag": tag,
        "release_id": int(release.get("id") or 0),
        "build": "android-ndk-source",
        "sha256": sha256_file(output),
        "size": output.stat().st_size,
        "builder_provenance": build_prov,
    }


def prebuilt_runtime(args: argparse.Namespace, output: Path, *, allow_fixture: bool) -> dict:
    raw = str(args.runtime_prebuilt or os.environ.get("RNEXUS_RCLONE_PREBUILT", "")).strip()
    if not raw:
        raise ProvisionError("prebuilt provider requires --runtime-prebuilt or RNEXUS_RCLONE_PREBUILT")
    source = Path(raw).expanduser().resolve()
    if not source.is_file():
        raise ProvisionError(f"runtime prebuilt does not exist: {source}")
    shutil.copy2(source, output)
    output.chmod(0o755)
    require_arm64(output, "prebuilt rclone-family runtime", allow_fixture=allow_fixture)
    return {
        "provider": "prebuilt",
        "repository": "operator-supplied",
        "requested_ref": "prebuilt",
        "resolved_commit": "",
        "release_tag": "prebuilt",
        "build": "prebuilt-override",
        "sha256": sha256_file(output),
        "size": output.stat().st_size,
    }


def materialize(args: argparse.Namespace) -> int:
    provider = str(args.provider or os.environ.get("RNEXUS_RUNTIME_PROVIDER", "newfuture")).strip().lower()
    if provider not in {"newfuture", "bclone", "prebuilt"}:
        raise ProvisionError(f"unsupported runtime provider: {provider}")
    output = Path(args.output_dir).expanduser().resolve()
    if output == ROOT or ROOT in output.parents and output == ROOT:
        raise ProvisionError("runtime output directory cannot be repository root")
    if output.exists():
        shutil.rmtree(output)
    output.mkdir(parents=True, exist_ok=True)
    allow_fixture = os.environ.get("RNEXUS_ALLOW_RUNTIME_TEST_FIXTURES", "").strip() == "1"

    try:
        with tempfile.TemporaryDirectory(prefix="rnexus-runtime-inputs-") as td:
            temp = Path(td)
            archive, nf_meta = newfuture_archive(args, temp)
            extracted = extract_newfuture_payload(archive, output, need_runtime=(provider == "newfuture"), allow_fixture=allow_fixture)

            runtime = output / "rclone"
            if provider == "newfuture":
                assert extracted["runtime"] is not None
                runtime_meta = {
                    "provider": "newfuture",
                    "repository": NEWFUTURE_REPOSITORY,
                    "release_tag": nf_meta["release_tag"],
                    "release_id": nf_meta["release_id"],
                    "asset_id": nf_meta["asset_id"],
                    "asset_name": nf_meta["asset_name"],
                    "archive_sha256": nf_meta["archive_sha256"],
                    "archive_path": extracted["runtime"]["archive_path"],
                    "build": "newfuture-prebuilt",
                    "sha256": extracted["runtime"]["sha256"],
                    "size": extracted["runtime"]["size"],
                }
            elif provider == "bclone":
                runtime_meta = build_bclone(args, temp, runtime, allow_fixture=allow_fixture)
            else:
                runtime_meta = prebuilt_runtime(args, runtime, allow_fixture=allow_fixture)

            helper = extracted["helper"]
            provenance = {
                "schema_version": 1,
                "runtime": runtime_meta,
                "fuse_helper": {
                    **nf_meta,
                    "provider_invariant": "newfuture",
                    "helper_archive_path": helper["archive_path"],
                    "helper_package_path": helper["package_path"],
                    "helper_sha256": helper["sha256"],
                    "helper_size": helper["size"],
                    "payload": extracted["fuse_payload"],
                },
            }
            (output / PROVENANCE_NAME).write_text(json.dumps(provenance, indent=2, sort_keys=True) + "\n", encoding="utf-8")
            print(json.dumps({
                "provider": provider,
                "runtime": str(runtime),
                "runtime_sha256": runtime_meta["sha256"],
                "fusermount3": str(output / "newfuture" / helper["package_path"]),
                "fusermount3_sha256": helper["sha256"],
                "newfuture_release": nf_meta["release_tag"],
                "provenance": str(output / PROVENANCE_NAME),
            }, sort_keys=True))
    except Exception:
        shutil.rmtree(output, ignore_errors=True)
        raise
    return 0


def verify(args: argparse.Namespace) -> int:
    root = Path(args.runtime_dir).expanduser().resolve()
    provenance_path = root / PROVENANCE_NAME
    runtime = root / "rclone"
    if not runtime.is_file() or not provenance_path.is_file():
        raise ProvisionError("materialized runtime directory is incomplete")
    provenance = json.loads(provenance_path.read_text(encoding="utf-8"))
    if provenance.get("schema_version") != 1:
        raise ProvisionError("runtime provenance schema mismatch")
    runtime_meta = provenance.get("runtime") or {}
    helper_meta = provenance.get("fuse_helper") or {}
    if sha256_file(runtime) != runtime_meta.get("sha256"):
        raise ProvisionError("materialized runtime bytes do not match provenance")
    if helper_meta.get("repository") != NEWFUTURE_REPOSITORY or helper_meta.get("provider_invariant") != "newfuture":
        raise ProvisionError("fusermount3 provenance is not fixed to NewFuture")
    helper_rel = str(helper_meta.get("helper_package_path") or "")
    helper = root / "newfuture" / helper_rel
    if not helper.is_file() or sha256_file(helper) != helper_meta.get("helper_sha256"):
        raise ProvisionError("NewFuture fusermount3 bytes do not match provenance")
    for item in helper_meta.get("payload", []):
        if not isinstance(item, dict):
            raise ProvisionError("invalid FUSE payload provenance entry")
        rel = str(item.get("package_path") or "")
        path = root / "newfuture" / rel
        if not path.is_file() or sha256_file(path) != item.get("sha256"):
            raise ProvisionError(f"NewFuture FUSE payload mismatch: {rel}")
    print(json.dumps({"status": "ok", "provider": runtime_meta.get("provider"), "runtime_sha256": runtime_meta.get("sha256"), "fusermount3_sha256": helper_meta.get("helper_sha256")}, sort_keys=True))
    return 0


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="command", required=True)
    m = sub.add_parser("materialize", help="resolve/build runtime and NewFuture FUSE helper into one immutable input directory")
    m.add_argument("--provider", choices=("newfuture", "bclone", "prebuilt"), default="")
    m.add_argument("--output-dir", required=True)
    m.add_argument("--newfuture-tag", default="")
    m.add_argument("--newfuture-archive", default="")
    m.add_argument("--bclone-ref", default="")
    m.add_argument("--runtime-prebuilt", default="")
    m.set_defaults(func=materialize)
    v = sub.add_parser("verify", help="verify a previously materialized runtime input directory")
    v.add_argument("--runtime-dir", required=True)
    v.set_defaults(func=verify)
    return p


def main() -> int:
    try:
        args = parser().parse_args()
        return args.func(args)
    except ProvisionError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
