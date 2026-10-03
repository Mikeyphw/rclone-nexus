#!/usr/bin/env python3
"""SOURCE-X02 reproducible Android/arm64 source builder.

The script intentionally separates mutable-ref resolution from the build. The
build subcommand accepts only a full commit SHA and verifies the checkout is
already detached at those exact bytes before invoking the Android NDK recipe.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request

COMMIT_RE = re.compile(r"^[0-9a-fA-F]{40}$")
REPO_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")


def run(argv: list[str], cwd: Path | None = None, env: dict[str, str] | None = None) -> str:
    cp = subprocess.run(argv, cwd=cwd, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if cp.returncode:
        raise RuntimeError(f"command failed ({cp.returncode}): {' '.join(argv)}\n{cp.stdout}{cp.stderr}")
    return cp.stdout.strip()


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        while True:
            block = fh.read(1024 * 1024)
            if not block:
                break
            h.update(block)
    return h.hexdigest()


def github_json(repository: str, endpoint: str, token: str = "") -> dict:
    if not REPO_RE.fullmatch(repository):
        raise RuntimeError("repository must be OWNER/REPO")
    url = f"https://api.github.com/repos/{repository}/{endpoint.lstrip('/')}"
    req = urllib.request.Request(url, headers={"Accept": "application/vnd.github+json", "User-Agent": "rclone-nexus-source-x02"})
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    opener = urllib.request.build_opener(NoCrossOriginRedirect())
    with opener.open(req, timeout=30) as resp:
        if resp.geturl().split("?", 1)[0] != url:
            raise RuntimeError("GitHub metadata request redirected")
        return json.load(resp)


class NoCrossOriginRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[override]
        before = urllib.parse.urlparse(req.full_url)
        after = urllib.parse.urlparse(newurl)
        if before.scheme != "https" or after.scheme != "https" or before.hostname != after.hostname:
            raise RuntimeError("GitHub metadata redirect escaped trusted origin")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def resolve_ref(args: argparse.Namespace) -> int:
    if not args.ref.strip():
        raise RuntimeError("ref is required")
    data = github_json(args.repository, f"commits/{urllib.parse.quote(args.ref, safe='')}", args.token or os.environ.get("GITHUB_TOKEN", ""))
    commit = str(data.get("sha", "")).lower()
    if not COMMIT_RE.fullmatch(commit):
        raise RuntimeError("GitHub did not resolve ref to a full commit SHA")
    print(json.dumps({"repository": args.repository, "requested_ref": args.ref, "resolved_commit": commit}, sort_keys=True))
    return 0


def build(args: argparse.Namespace) -> int:
    source = Path(args.source_dir).resolve()
    out = Path(args.output_dir).resolve()
    if not REPO_RE.fullmatch(args.repository):
        raise RuntimeError("repository must be OWNER/REPO")
    commit = args.resolved_commit.lower().strip()
    if not COMMIT_RE.fullmatch(commit):
        raise RuntimeError("resolved commit must be immutable full 40-hex SHA")
    if not source.is_dir() or not (source / "go.mod").is_file():
        raise RuntimeError("source checkout is missing go.mod")
    head = run(["git", "rev-parse", "HEAD"], source).lower()
    if head != commit:
        raise RuntimeError(f"source checkout HEAD {head} does not match resolved commit {commit}")
    if run(["git", "status", "--porcelain"], source):
        raise RuntimeError("source checkout is dirty; reproducible build refused")
    ndk = Path(args.ndk).resolve()
    host = args.ndk_host
    compiler = ndk / "toolchains" / "llvm" / "prebuilt" / host / "bin" / f"aarch64-linux-android{args.api_level}-clang"
    if not compiler.is_file():
        raise RuntimeError(f"Android NDK compiler not found: {compiler}")
    go_version = run(["go", "version"])
    compiler_version = run([str(compiler), "--version"]).splitlines()[0]
    out.mkdir(parents=True, exist_ok=True)
    binary_name = args.binary_name or f"{args.engine}-android-arm64"
    if Path(binary_name).name != binary_name:
        raise RuntimeError("binary name must be a basename")
    binary = out / binary_name
    # Remove prior metadata first: failed/partial builds must never look complete.
    for name in ("provenance.json", "SHA256SUMS", binary_name):
        try:
            (out / name).unlink()
        except FileNotFoundError:
            pass
    tags = ["android"]
    ldflags = ["-s", "-w"]
    build_flags = ["-v", "-tags", "android", "-trimpath"]
    env = os.environ.copy()
    env.update({
        "GOOS": "android",
        "GOARCH": "arm64",
        "CGO_ENABLED": "1",
        "CC": str(compiler),
        "CC_FOR_TARGET": str(compiler),
        "CGO_LDFLAGS": "-fuse-ld=lld -s -w",
    })
    command = ["go", "build", *build_flags, "-ldflags", " ".join(ldflags), "-o", str(binary), "."]
    subprocess.run(command, cwd=source, env=env, check=True)
    binary.chmod(0o755)
    digest = sha256(binary)
    manifest = {
        "schema_version": 1,
        "state": "complete",
        "source_id": args.source_id,
        "engine": args.engine,
        "repository": args.repository,
        "requested_ref": args.requested_ref,
        "resolved_commit": commit,
        "go_version": go_version,
        "ndk_version": args.ndk_version,
        "compiler": str(compiler),
        "compiler_version": compiler_version,
        "api_level": args.api_level,
        "goos": "android",
        "goarch": "arm64",
        "abi": "arm64-v8a",
        "cgo_enabled": True,
        "tags": tags,
        "trimpath": True,
        "build_flags": build_flags,
        "ldflags": ldflags,
        "binary_name": binary_name,
        "binary_sha256": digest,
        "binary_size": binary.stat().st_size,
        "produced_unix_ms": int(time.time() * 1000),
    }
    payload = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode()
    prov = out / "provenance.json"
    prov.write_bytes(payload)
    sums = out / "SHA256SUMS"
    sums.write_text(f"{digest}  {binary_name}\n{sha256(prov)}  provenance.json\n", encoding="utf-8")
    print(json.dumps(manifest, sort_keys=True))
    return 0


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="command", required=True)
    r = sub.add_parser("resolve-ref")
    r.add_argument("--repository", required=True)
    r.add_argument("--ref", required=True)
    r.add_argument("--token", default="")
    r.set_defaults(func=resolve_ref)
    b = sub.add_parser("build")
    b.add_argument("--source-dir", required=True)
    b.add_argument("--output-dir", required=True)
    b.add_argument("--repository", required=True)
    b.add_argument("--requested-ref", required=True)
    b.add_argument("--resolved-commit", required=True)
    b.add_argument("--source-id", required=True)
    b.add_argument("--engine", required=True)
    b.add_argument("--ndk", required=True)
    b.add_argument("--ndk-version", required=True)
    b.add_argument("--ndk-host", default="linux-x86_64")
    b.add_argument("--api-level", type=int, default=21)
    b.add_argument("--binary-name", default="")
    b.set_defaults(func=build)
    return p


def main() -> int:
    args = parser().parse_args()
    try:
        return args.func(args)
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1

if __name__ == "__main__":
    raise SystemExit(main())
