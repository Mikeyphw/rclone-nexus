#!/usr/bin/env python3
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import zipfile
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts' / 'dev'))
import runtime_standalone_g1_device as g1  # noqa: E402

SCHEMA_VERSION = 1
HARNESS_VERSION = 7
DEFAULT_EVIDENCE = ROOT / 'release/evidence/source-g1-supply-chain-qualification.json'

BOUND_SOURCE_PATHS = [
    '.github/workflows/runtime-source-build.yml',
    'cmd/racctl/main.go',
    'internal/runtimebuild/bundle.go',
    'internal/runtimeactivation/activation.go',
    'internal/runtimesource/github.go',
    'internal/runtimesource/source.go',
    'internal/runtimeupdate/update.go',
    'internal/paths/paths.go',
    'internal/provider/provider.go',
    'internal/runtimestore/helper.go',
    'internal/runtimestore/qualify.go',
    'internal/runtimestore/store.go',
    'module/service.sh',
    'scripts/dev/runtime_source_build.py',
    'scripts/dev/runtime_standalone_g1_device.py',
    'scripts/dev/source_x01_gate.py',
    'scripts/dev/source_x02_gate.py',
    'scripts/dev/update_x01_gate.py',
    'scripts/dev/source_g1_device.py',
    'scripts/dev/source_g1_gate.py',
]

# The SOURCE-G1 replacement artifact is validated before its commit exists.
# Permit only files owned by this gate overlay; any other dirty path makes a
# rooted/network proof ambiguous and is rejected.
OVERLAY_OWNED_PATHS = frozenset({
    # Exact repository inventory of the SOURCE-G1 overlay. Artifact metadata
    # such as .devtool-artifact/validate.sh is intentionally excluded because
    # it is not a target-worktree path and must not widen the dirty-tree trust
    # boundary.
    '.devtool.toml', '.gitignore', 'CHANGELOG.md', 'README.md',
    'docs/ARCHITECTURE.md', 'docs/implementation/SOURCE-G1.md',
    'internal/runtimesource/source.go', 'internal/runtimesource/source_test.go',
    'release/canonical-promise-ledger.json', 'release/evidence/README.md', 'release/final-seal-policy.json',
    'scripts/dev/cleanup_validation_outputs.py', 'scripts/dev/release_artifacts.py',
    'scripts/dev/source_g1_device.py', 'scripts/dev/source_g1_gate.py', 'scripts/dev/update_x01_gate.py',
    'tests/test_source_g1_supply_chain.py',
})


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def canonical(v: object) -> bytes:
    return json.dumps(v, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()


def digest(v: object) -> str:
    return hashlib.sha256(canonical(v)).hexdigest()


def file_sha256(p: Path) -> str:
    h = hashlib.sha256()
    with p.open('rb') as f:
        for b in iter(lambda: f.read(1024 * 1024), b''):
            h.update(b)
    return h.hexdigest()


def source_bindings() -> dict[str, str]:
    out: dict[str, str] = {}
    for rel in BOUND_SOURCE_PATHS:
        p = ROOT / rel
        if not p.is_file():
            raise RuntimeError(f'SOURCE-G1 bound source missing: {rel}')
        out[rel] = file_sha256(p)
    return out


def parse_obj(result, label: str) -> dict:
    return g1.parse_json_output(result, label)


def racctl(binary: str, env: dict[str, str], args: list[str], *, check: bool = True, timeout: int = 420):
    return g1.racctl(binary, args, env, check=check, timeout=timeout)


def mark(name: str) -> None:
    print(f'SOURCE-G1 device step: {name}', file=sys.stderr, flush=True)


def mount_fixture_config() -> str:
    # SOURCE-G1 reuses the sealed RUNTIME-G1 local-backend mount definition,
    # whose remote is runtimeg1:. Keep the config section name identical to
    # that production fixture so qualification proves the same resolver path.
    return '[runtimeg1]\ntype = local\n'


def preflight_mount_fixture(binary: str, env: dict[str, str], source_dir: str, expected_entry: str = 'proof.txt') -> None:
    selected = racctl(binary, env, ['runtime', 'executable']).stdout.strip()
    if not selected:
        raise RuntimeError('SOURCE-G1 active runtime executable is empty before mount fixture preflight')
    config = f"{env['RNEXUS_STATE_DIR']}/config/rclone/rclone.conf"
    result = g1.root_run([selected, 'lsf', f'runtimeg1:{source_dir}', '--config', config], env=env, timeout=30)
    if result.returncode != 0:
        raise RuntimeError('SOURCE-G1 runtimeg1 fixture preflight failed: ' + (result.stderr.strip() or result.stdout.strip()))
    entries = {line.strip().rstrip('/') for line in result.stdout.splitlines() if line.strip()}
    if expected_entry not in entries:
        raise RuntimeError(f'SOURCE-G1 runtimeg1 fixture preflight did not expose {expected_entry}: {sorted(entries)!r}')


def proc_hash(pid: int, state: str) -> str:
    if pid <= 1:
        raise RuntimeError('SOURCE-G1 mount has no live process pid')
    script = f'''set -eu
proc=/proc/{pid}/exe
if sha256sum "$proc" 2>/dev/null; then exit 0; fi
target=$(readlink "$proc")
target=${{target% (deleted)}}
case "$target" in
  {state}/runtimes/*/rclone|{state}/runtimes/*/bclone) ;;
  *) echo "unexpected SOURCE-G1 process executable target: $target" >&2; exit 44 ;;
esac
sha256sum "$target"
'''
    r = g1.root_run(['/system/bin/sh', '-c', script], timeout=30)
    if r.returncode != 0 or not r.stdout.strip():
        raise RuntimeError(f'cannot hash SOURCE-G1 live process: {r.stderr.strip()}')
    value = r.stdout.split()[0].strip().lower()
    if len(value) != 64:
        raise RuntimeError('invalid SOURCE-G1 process hash')
    return value


def snapshot(root_path: str, snapshot_dir: str, name: str, kind: str) -> dict:
    return g1.snapshot_evidence_ref(root_path, snapshot_dir, name, kind)


def resolution_state_path(state: str, resolution_id: str) -> str:
    # Keep the gate aligned with internal/paths.Paths.Normalize():
    # RuntimeSourcesDir = <state>/runtime/sources and immutable resolution
    # records live below its resolutions/ child.  Do not invent a parallel
    # <state>/runtime-sources namespace in qualification code.
    if not resolution_id.startswith('src-') or len(resolution_id) != 36:
        raise RuntimeError(f'invalid SOURCE-G1 resolution ID for durable state path: {resolution_id!r}')
    return f'{state}/runtime/sources/resolutions/{resolution_id}.json'


def resolution_identity(r: dict) -> None:
    # Immutable identity is source-kind/channel specific. SOURCE-X02 build-backed
    # GitHub sources (notably bclone) deliberately resolve a repository/release/
    # commit without selecting downloadable release bytes: the exact commit plus
    # build_repository is the acquisition authority. Download-backed GitHub and
    # NewFuture sources must instead bind a concrete immutable asset.
    required = ('resolution_id', 'source_id', 'spec_digest', 'engine', 'kind', 'channel', 'repository', 'repository_id', 'commit_sha')
    for key in required:
        if not r.get(key):
            raise RuntimeError(f'external source resolution missing immutable identity field: {key}')

    kind = str(r.get('kind', ''))
    channel = str(r.get('channel', ''))
    build_required = bool(r.get('build_required'))
    build_repository = str(r.get('build_repository', '')).strip()

    if channel in {'latest-stable', 'pinned-release'}:
        for key in ('release_id', 'release_tag'):
            if not r.get(key):
                raise RuntimeError(f'external release resolution missing immutable identity field: {key}')

    sha = str(r.get('commit_sha', ''))
    if len(sha) != 40 or any(c not in '0123456789abcdef' for c in sha.lower()):
        raise RuntimeError('external source did not resolve to exact commit')

    asset = r.get('asset')
    needs_asset = kind == 'newfuture-derived' or (kind == 'github-release' and not build_required and channel != 'pinned-commit')
    if needs_asset:
        if not isinstance(asset, dict) or int(asset.get('id') or 0) <= 0 or not asset.get('api_url') or not asset.get('name'):
            raise RuntimeError('download-backed external source resolution did not select a concrete numeric asset')
    elif build_required:
        if not build_repository:
            raise RuntimeError('build-backed external source resolution has no SOURCE-X02 build_repository')
        if asset not in (None, {}):
            raise RuntimeError('build-backed external source unexpectedly selected release asset bytes')


def update_snapshot(binary: str, env: dict[str, str]) -> dict:
    return parse_obj(racctl(binary, env, ['runtime', 'update', 'status']), 'runtime update status')


def assert_authority_unchanged(before: dict, after: dict, label: str) -> None:
    keys = ('current_runtime_id', 'current_binary_sha256', 'staged_runtime_id', 'staged_binary_sha256')
    for k in keys:
        if str(before.get(k, '')) != str(after.get(k, '')):
            raise RuntimeError(f'{label} changed update authority {k}: {before.get(k)!r} -> {after.get(k)!r}')


def make_bad_archives(tmp: Path) -> dict[str, Path]:
    malformed = tmp / 'malformed.zip'
    malformed.write_bytes(b'not-a-zip\n')
    traversal = tmp / 'traversal.zip'
    with zipfile.ZipFile(traversal, 'w') as z:
        z.writestr('../rclone', b'evil')
    symlink = tmp / 'symlink.zip'
    with zipfile.ZipFile(symlink, 'w') as z:
        info = zipfile.ZipInfo('rclone')
        info.create_system = 3
        info.external_attr = (0o120777 << 16)
        z.writestr(info, '/system/bin/sh')
    return {'malformed': malformed, 'traversal': traversal, 'symlink': symlink}


def start_http(root: Path):
    # Pick a free local port without adding a separate server dependency.
    import socket
    s = socket.socket(); s.bind(('127.0.0.1', 0)); port = int(s.getsockname()[1]); s.close()
    proc = subprocess.Popen([sys.executable, '-m', 'http.server', str(port), '--bind', '127.0.0.1', '--directory', str(root)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    deadline = time.time() + 5
    while time.time() < deadline:
        try:
            with socket.create_connection(('127.0.0.1', port), timeout=.2):
                break
        except OSError:
            time.sleep(.05)
    else:
        proc.terminate(); raise RuntimeError('cannot start SOURCE-G1 loopback adversarial server')
    return proc, port


def register_url(binary: str, env: dict[str, str], source_id: str, url: str, sha: str) -> None:
    racctl(binary, env, ['runtime', 'source', 'register', '--id', source_id, '--kind', 'url', '--engine', 'rclone', '--channel', 'manual-only', '--url', url, '--sha256', sha])


def run_negative(binary: str, env: dict[str, str], source_id: str, before: dict) -> dict:
    result = racctl(binary, env, ['runtime', 'update', 'check', source_id], check=False, timeout=180)
    if result.returncode == 0:
        raise RuntimeError(f'adversarial update {source_id} unexpectedly succeeded')
    after = update_snapshot(binary, env)
    assert_authority_unchanged(before, after, source_id)
    state = after.get('state') if isinstance(after.get('state'), dict) else {}
    return {'returncode': result.returncode, 'last_result': state.get('last_result', ''), 'retryable': bool(state.get('retryable')), 'error_redacted': 'Authorization' not in str(state.get('last_error', '')) and 'token=' not in str(state.get('last_error', ''))}


def _android_arm64_linked_elf(path: Path) -> bool:
    try:
        data = path.read_bytes()
    except OSError:
        return False
    if len(data) < 64 or data[:4] != b'\x7fELF' or data[4] != 2 or data[5] != 1:
        return False
    # ELF64 little-endian e_machine.
    if int.from_bytes(data[18:20], 'little') != 183:
        return False
    return b'/system/bin/linker64\x00' in data


def _ndk_clang_resource_dir(host: Path) -> tuple[Path, Path, Path] | None:
    root = host / 'lib' / 'clang'
    if not root.is_dir():
        return None
    candidates = sorted((p for p in root.iterdir() if p.is_dir()), reverse=True)
    for resource in candidates:
        linux = resource / 'lib' / 'linux'
        builtins_candidates = [
            linux / 'libclang_rt.builtins-aarch64-android.a',
            linux / 'aarch64' / 'libclang_rt.builtins.a',
        ]
        unwind_candidates = [
            linux / 'aarch64' / 'libunwind.a',
        ]
        builtins = next((x for x in builtins_candidates if x.is_file()), None)
        unwind = next((x for x in unwind_candidates if x.is_file()), None)
        if builtins is not None and unwind is not None:
            return resource, builtins, unwind
    return None


def _probe_native_clang(ndk: Path, host: Path, compiler: str, api_level: int = 21) -> dict:
    """Prove a native host clang can drive the pinned NDK target/sysroot.

    Official Android NDK Linux prebuilts are x86_64-hosted.  On native ARM64
    Termux those executables can exist but be unrunnable.  The NDK sysroot and
    target libraries are still architecture-independent build inputs, so a
    native Termux clang may drive them.  We only accept this mode after a real
    compile+link probe emits an Android/AArch64 ELF using the NDK sysroot.
    """
    sysroot = host / 'sysroot'
    if not sysroot.is_dir():
        return {'supported': False, 'reason': f'NDK sysroot missing: {sysroot}'}
    try:
        version = subprocess.run(
            [compiler, '--version'], text=True, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, timeout=20,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        return {'supported': False, 'reason': f'native clang probe failed: {type(exc).__name__}: {exc}'}
    if version.returncode != 0:
        detail = (version.stderr or version.stdout or '').strip().replace('\n', ' ')
        return {'supported': False, 'reason': f'native clang --version exited {version.returncode}: {detail[-500:]}'}
    target = f'aarch64-linux-android{api_level}'
    runtime = _ndk_clang_resource_dir(host)
    if runtime is None:
        return {'supported': False, 'reason': f'NDK clang resource directory is missing Android arm64 compiler-rt/libunwind under {host / "lib/clang"}'}
    resource_dir, builtins, libunwind = runtime
    with tempfile.TemporaryDirectory(prefix='rnx-ndk-native-clang-') as raw:
        td = Path(raw)
        src = td / 'probe.c'
        out = td / 'probe'
        src.write_text('#include <android/log.h>\nint main(void) { return __android_log_print(ANDROID_LOG_INFO, \"rnx-probe\", \"ok\") < 0; }\n', encoding='utf-8')
        argv = [compiler, f'--target={target}', f'--sysroot={sysroot}', f'-resource-dir={resource_dir}', '-fuse-ld=lld', str(src), '-llog', '-o', str(out)]
        try:
            linked = subprocess.run(argv, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        except (OSError, subprocess.TimeoutExpired) as exc:
            return {'supported': False, 'reason': f'native clang Android link probe failed: {type(exc).__name__}: {exc}'}
        if linked.returncode != 0:
            detail = (linked.stderr or linked.stdout or '').strip().replace('\n', ' ')
            return {'supported': False, 'reason': f'native clang Android link probe exited {linked.returncode}: {detail[-1000:]}'}
        if not _android_arm64_linked_elf(out):
            return {'supported': False, 'reason': 'native clang Android link probe did not emit Android arm64 ELF'}
    banner = (version.stdout or version.stderr or '').splitlines()
    return {
        'supported': True,
        'compiler': compiler,
        'compiler_banner': banner[0].strip() if banner else '',
        'compiler_mode': 'native-clang-ndk-sysroot',
        'compiler_target': target,
        'sysroot': str(sysroot),
        'compiler_resource_dir': str(resource_dir),
        'compiler_rt_builtins': str(builtins),
        'compiler_rt_builtins_sha256': file_sha256(builtins),
        'compiler_libunwind': str(libunwind),
        'compiler_libunwind_sha256': file_sha256(libunwind),
        'android_system_libraries': ['log'],
    }


def find_ndk() -> dict:
    """Find a runnable Android arm64 toolchain bound to an installed NDK.

    Prefer the NDK's own host compiler.  On ARM64 Termux official Linux NDKs
    normally contain only linux-x86_64 host executables; when those are not
    runnable, prove a native clang can drive the pinned NDK sysroot/target and
    use that mode instead.  Mere file presence never counts as support.
    """
    explicit = os.environ.get('RNEXUS_SOURCE_G1_NDK', '').strip() or os.environ.get('ANDROID_NDK_HOME', '').strip() or os.environ.get('ANDROID_NDK_ROOT', '').strip()
    candidates: list[Path] = []
    if explicit:
        candidates.append(Path(explicit))
    sdk = os.environ.get('ANDROID_HOME', '').strip() or os.environ.get('ANDROID_SDK_ROOT', '').strip()
    if sdk:
        ndks = Path(sdk) / 'ndk'
        if ndks.is_dir():
            candidates.extend(sorted((p for p in ndks.iterdir() if p.is_dir()), reverse=True))

    seen: set[str] = set()
    failures: list[str] = []
    fallback_hosts: list[tuple[Path, Path, str]] = []
    present = False
    for ndk in candidates:
        key = str(ndk.resolve()) if ndk.exists() else str(ndk)
        if key in seen:
            continue
        seen.add(key)
        pre = ndk / 'toolchains/llvm/prebuilt'
        if not pre.is_dir():
            continue
        version = ndk.name
        props = ndk / 'source.properties'
        if props.is_file():
            for line in props.read_text(errors='replace').splitlines():
                if line.startswith('Pkg.Revision') and '=' in line:
                    version = line.split('=', 1)[1].strip()
        for host in sorted(pre.iterdir()):
            c = host / 'bin/aarch64-linux-android21-clang'
            if not c.is_file():
                continue
            present = True
            fallback_hosts.append((ndk, host, version))
            if not os.access(c, os.X_OK):
                failures.append(f'{host.name}: compiler exists but is not executable')
                continue
            try:
                cp = subprocess.run(
                    [str(c), '--version'], text=True, stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE, timeout=20,
                )
            except (OSError, subprocess.TimeoutExpired) as exc:
                failures.append(f'{host.name}: compiler probe failed: {type(exc).__name__}: {exc}')
                continue
            if cp.returncode != 0:
                detail = (cp.stderr or cp.stdout or '').strip().replace('\n', ' ')
                if len(detail) > 500:
                    detail = detail[-500:]
                failures.append(f'{host.name}: compiler probe exited {cp.returncode}: {detail or "no diagnostic"}')
                continue
            banner = (cp.stdout or cp.stderr or '').splitlines()
            return {
                'supported': True,
                'ndk': str(ndk),
                'host': host.name,
                'version': version,
                'compiler': str(c),
                'compiler_banner': banner[0].strip() if banner else '',
                'compiler_mode': 'ndk-prebuilt',
                'compiler_target': 'aarch64-linux-android21',
                'sysroot': str(host / 'sysroot'),
            }

    # Official Linux NDK prebuilts are x86_64-hosted.  Native ARM64 Termux can
    # still use their sysroot/target libraries with its own runnable clang.
    native = os.environ.get('RNEXUS_SOURCE_G1_NATIVE_CLANG', '').strip() or shutil.which('clang') or ''
    if native and fallback_hosts:
        for ndk, host, version in fallback_hosts:
            probe = _probe_native_clang(ndk, host, native, 21)
            if probe.get('supported') is True:
                return {
                    'supported': True,
                    'present': True,
                    'ndk': str(ndk),
                    'host': host.name,
                    'version': version,
                    **probe,
                }
            failures.append(f'{host.name} via native clang: {probe.get("reason", "probe failed")}')

    if present:
        return {
            'supported': False,
            'present': True,
            'reason': 'Android NDK aarch64 toolchain is installed but not runnable in this validation environment: ' + '; '.join(failures or ['no runnable host prebuilt or native clang+NDK sysroot path']),
        }
    return {
        'supported': False,
        'present': False,
        'reason': 'Android NDK aarch64 toolchain not present in validation environment',
    }


def github_release_candidates(repository: str, exclude_tag: str, limit: int = 8) -> list[str]:
    # This request is only candidate enumeration. Every selected tag is then
    # re-resolved through the production SOURCE-X01 resolver, which binds the
    # repository ID, release ID, peeled commit SHA and numeric asset ID.
    url = f"https://api.github.com/repos/{repository}/releases?per_page={max(limit + 2, 10)}"
    req = urllib.request.Request(
        url,
        headers={
            'Accept': 'application/vnd.github+json',
            'User-Agent': 'rclone-nexus-source-g1',
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=45) as resp:
            if resp.geturl().split('?', 1)[0] != url.split('?', 1)[0]:
                raise RuntimeError('SOURCE-G1 release enumeration escaped trusted GitHub API URL')
            raw = json.load(resp)
    except Exception as exc:
        raise RuntimeError(f'SOURCE-G1 cannot enumerate historical NewFuture releases: {exc}') from exc
    if not isinstance(raw, list):
        raise RuntimeError('SOURCE-G1 GitHub release enumeration returned non-list metadata')
    tags: list[str] = []
    for item in raw:
        if not isinstance(item, dict) or item.get('draft') or item.get('prerelease'):
            continue
        tag = str(item.get('tag_name', '')).strip()
        if not tag or tag == exclude_tag or tag in tags:
            continue
        tags.append(tag)
        if len(tags) >= limit:
            break
    if not tags:
        raise RuntimeError('SOURCE-G1 found no prior stable NewFuture release for real update transition proof')
    return tags


def import_historical_newfuture_baseline(binary: str, env: dict[str, str], latest: dict) -> tuple[dict, dict]:
    latest_tag = str(latest.get('release_tag', '')).strip()
    if not latest_tag:
        raise RuntimeError('SOURCE-G1 latest NewFuture resolution has no release tag')
    attempts: list[str] = []
    for tag in github_release_candidates('NewFuture/rclone-fuse3-magisk', latest_tag):
        attempts.append(tag)
        resolved_result = racctl(
            binary, env,
            ['runtime', 'source', 'resolve', 'newfuture', '--channel', 'pinned-release', '--ref', tag],
            check=False, timeout=180,
        )
        if resolved_result.returncode != 0:
            continue
        historical = parse_obj(resolved_result, f'NewFuture historical resolve {tag}')
        resolution_identity(historical)
        if historical.get('source_id') != 'newfuture' or historical.get('channel') != 'pinned-release':
            continue
        if historical.get('repository', '').lower() != 'newfuture/rclone-fuse3-magisk':
            continue
        if historical.get('release_tag') == latest_tag or historical.get('release_id') == latest.get('release_id'):
            continue
        if historical.get('asset', {}).get('name') != 'magisk-rclone_arm64-v8a.zip':
            continue
        imported_result = racctl(
            binary, env, ['runtime', 'source', 'import-resolution', historical['resolution_id']],
            check=False, timeout=600,
        )
        try:
            manifest = parse_obj(imported_result, f'NewFuture historical import {tag}')
        except RuntimeError:
            continue
        q = manifest.get('qualification') if isinstance(manifest.get('qualification'), dict) else {}
        if imported_result.returncode == 0 and q.get('qualified') is True:
            if not manifest.get('runtime_id') or len(str(manifest.get('binary_sha256', ''))) != 64:
                raise RuntimeError('SOURCE-G1 historical NewFuture import returned malformed runtime identity')
            return historical, manifest
    raise RuntimeError('SOURCE-G1 could not import a qualified historical NewFuture baseline from: ' + ', '.join(attempts))

def real_builder_probe(ndk_probe: dict, tmp: Path) -> dict:
    if ndk_probe.get('supported') is not True:
        return {
            'supported': False,
            'reason': str(ndk_probe.get('reason', 'Android NDK compiler unavailable')),
            'compiler_present': bool(ndk_probe.get('present', False)),
        }
    ndk = str(ndk_probe['ndk'])
    host = str(ndk_probe['host'])
    version = str(ndk_probe['version'])
    repo = tmp / 'builder-fixture'; out = tmp / 'builder-output'
    repo.mkdir()
    (repo / 'go.mod').write_text('module example.invalid/sourceg1\n\ngo 1.23\n', encoding='utf-8')
    (repo / 'main.go').write_text('package main\n/* int nexus(void) { return 7; } */\nimport "C"\nimport "fmt"\nfunc main(){fmt.Println(C.nexus())}\n', encoding='utf-8')
    subprocess.run(['git','init','-q'], cwd=repo, check=True)
    subprocess.run(['git','config','user.email','source-g1@example.invalid'], cwd=repo, check=True)
    subprocess.run(['git','config','user.name','SOURCE-G1'], cwd=repo, check=True)
    subprocess.run(['git','add','.'], cwd=repo, check=True); subprocess.run(['git','commit','-qm','fixture'], cwd=repo, check=True)
    commit = subprocess.check_output(['git','rev-parse','HEAD'], cwd=repo, text=True).strip()
    cp = subprocess.run([sys.executable, str(ROOT/'scripts/dev/runtime_source_build.py'), 'build', '--source-dir', str(repo), '--output-dir', str(out), '--repository', 'source-g1/fixture', '--requested-ref', 'fixture', '--resolved-commit', commit, '--source-id', 'source-g1-build', '--engine', 'rclone', '--ndk', ndk, '--ndk-version', version, '--ndk-host', host, '--compiler', str(ndk_probe.get('compiler','')), '--compiler-mode', str(ndk_probe.get('compiler_mode','ndk-prebuilt')), '--compiler-resource-dir', str(ndk_probe.get('compiler_resource_dir','')), '--api-level', '21'], cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=300)
    if cp.returncode != 0:
        raise RuntimeError('SOURCE-X02 real Android NDK builder failed in supported environment: ' + cp.stderr[-3000:])
    verify = subprocess.run(['go','run','./cmd/racctl','runtime','source','verify-build',str(out)], cwd=ROOT, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180)
    if verify.returncode != 0:
        raise RuntimeError('SOURCE-X02 real built bundle failed production verifier: ' + verify.stderr[-3000:])
    prov = json.loads((out/'provenance.json').read_text())
    return {'supported': True, 'resolved_commit': commit, 'binary_sha256': prov['binary_sha256'], 'goos': prov['goos'], 'goarch': prov['goarch'], 'abi': prov['abi'], 'ndk_version': prov['ndk_version'], 'ndk_host': prov.get('ndk_host', host), 'compiler_mode': prov.get('compiler_mode', ndk_probe.get('compiler_mode','ndk-prebuilt')), 'compiler_target': prov.get('compiler_target',''), 'compiler_resource_dir': prov.get('compiler_resource_dir',''), 'compiler_rt_builtins_sha256': prov.get('compiler_rt_builtins_sha256',''), 'compiler_libunwind_sha256': prov.get('compiler_libunwind_sha256',''), 'compiler_banner': str(ndk_probe.get('compiler_banner', ''))}


def capture(out_path: Path) -> dict:
    mark('require rooted Android and clean qualification ownership')
    g1.require_android_root()
    status = g1.run(['git','status','--porcelain','--untracked-files=all'], timeout=10)
    if status.returncode != 0:
        raise RuntimeError('cannot inspect repository state before SOURCE-G1 capture')
    dirty = g1.capture_dirty_paths(status.stdout)
    unexpected = sorted(dirty - OVERLAY_OWNED_PATHS)
    if unexpected:
        raise RuntimeError('SOURCE-G1 capture found unrelated repository changes: ' + ', '.join(unexpected))

    session = 'source-g1-' + secrets.token_hex(8)
    root_dir = f'/data/adb/rclone-nexus/qualification/{session}'
    state = f'{root_dir}/state'; provider_absent = f'{root_dir}/provider-absent'; snapshot_dir = f'{root_dir}/evidence/snapshot'
    g1.root_run(['mkdir','-p',root_dir,state,snapshot_dir], check=True)
    g1.root_run(['chmod','0700',root_dir,state,f'{root_dir}/evidence',snapshot_dir], check=True)

    with tempfile.TemporaryDirectory(prefix='rnx-source-g1-') as raw:
        tmp = Path(raw); built = tmp/'racctl'
        mark('build current production racctl')
        g1.build_current_racctl(built)
        mark('stage providerless qualification module')
        module_dir, binary = g1.copy_gate_module(root_dir, built)
        env = g1.racctl_env(state, module_dir, provider_absent)
        g1.root_run(['mkdir','-p',f'{state}/tmp',f'{state}/config/rclone',f'{state}/mounts.d',f'{state}/run'], check=True)

        cfg = tmp/'rclone.conf'; cfg.write_text(mount_fixture_config(), encoding='utf-8'); g1.root_write_from_local(cfg, f'{state}/config/rclone/rclone.conf')
        src_dir=f'{root_dir}/source'; mountpoint=f'{root_dir}/mountpoint'; g1.root_run(['mkdir','-p',src_dir,mountpoint],check=True)
        g1.root_run(['/system/bin/sh','-c',f"printf %s {shlex.quote(session)} > {shlex.quote(src_dir+'/proof.txt')}"],check=True)

        mark('resolve real external bclone, official rclone and latest NewFuture sources')
        resolutions={}
        for sid in ('bclone','rclone','newfuture'):
            r=parse_obj(racctl(binary,env,['runtime','source','resolve',sid],timeout=180),f'{sid} resolve')
            resolution_identity(r); resolutions[sid]=r
        if resolutions['newfuture']['asset']['name'] != 'magisk-rclone_arm64-v8a.zip':
            raise RuntimeError('NewFuture built-in did not select the Android arm64 Magisk runtime asset')
        if resolutions['bclone']['repository'].lower() != 'benjithatfoxguy/bclone' or resolutions['rclone']['repository'].lower()!='rclone/rclone' or resolutions['newfuture']['repository'].lower()!='newfuture/rclone-fuse3-magisk':
            raise RuntimeError('real external resolver changed canonical repository identity')

        # Resolution records are immutable production authority. Snapshot each
        # one immediately after the production resolver persists it, before
        # later update/rollback/adversarial operations can alter unrelated
        # state. The snapshot source path is the canonical Paths.Normalize()
        # layout used by runtimesource.persistResolution.
        resolution_refs=[]
        for sid,r in resolutions.items():
            rp=resolution_state_path(state,str(r['resolution_id']))
            resolution_refs.append(snapshot(rp,snapshot_dir,f'{sid}-resolution.json','source-resolution'))

        mark('select, import and qualify a real historical NewFuture baseline A')
        baseline_resolution, imported_a = import_historical_newfuture_baseline(binary, env, resolutions['newfuture'])
        aid=str(imported_a.get('runtime_id','')); ahash=str(imported_a.get('binary_sha256',''))
        if not aid or len(ahash) != 64:
            raise RuntimeError('historical baseline A has malformed runtime identity')
        if baseline_resolution.get('release_tag') == resolutions['newfuture'].get('release_tag'):
            raise RuntimeError('historical baseline unexpectedly resolved to latest NewFuture release')
        managed_fuse_helper = f'{state}/runtime/helpers/fusermount3/current/fusermount3'
        managed_fuse_manifest = f'{state}/runtime/helpers/fusermount3/current-v1.json'
        helper_manifest = g1.root_json(managed_fuse_manifest)
        if helper_manifest.get('repository') != 'NewFuture/rclone-fuse3-magisk' or helper_manifest.get('asset_name') != 'magisk-rclone_arm64-v8a.zip':
            raise RuntimeError('SOURCE-G1 managed fusermount3 is not bound to canonical NewFuture helper authority')
        helper_hash = g1.root_hash(managed_fuse_helper)
        if helper_hash != str(helper_manifest.get('helper_sha256', '')):
            raise RuntimeError('SOURCE-G1 managed NewFuture fusermount3 bytes do not match helper manifest')
        baseline_checks = {str(item.get('name','')): str(item.get('status','')) for item in imported_a.get('qualification',{}).get('checks',[]) if isinstance(item,dict)}
        if baseline_checks.get('fuse_helper_authority') != 'pass':
            raise RuntimeError('SOURCE-G1 baseline qualification did not prove canonical NewFuture fusermount3 authority')
        resolution_refs.append(snapshot(
            resolution_state_path(state,str(baseline_resolution['resolution_id'])),
            snapshot_dir,'newfuture-baseline-resolution.json','source-resolution-baseline',
        ))
        racctl(binary,env,['runtime','activate',aid])
        mark('preflight runtimeg1 local fixture through historical active runtime A')
        preflight_mount_fixture(binary, env, src_dir)
        mount_cfg=tmp/'source-g1.conf'; mount_cfg.write_text(g1.g1_mount_definition(src_dir,mountpoint),encoding='utf-8'); g1.root_write_from_local(mount_cfg,f'{state}/mounts.d/source-g1.conf')
        racctl(binary,env,['compat','mountctl','start','source-g1'])
        ma=g1.mount_status(binary,env,'source-g1'); pida=int(ma.get('pid') or 0); phash_a=proc_hash(pida,state)
        if phash_a != ahash or g1.root_run(['cat',f'{mountpoint}/proof.txt'],check=True).stdout.strip()!=session: raise RuntimeError('historical baseline mount is not executing runtime A bytes')

        mark('exercise latest NewFuture resolution -> download -> hash/archive -> qualification -> stage')
        before=update_snapshot(binary,env)
        checked=parse_obj(racctl(binary,env,['runtime','update','check','newfuture'],timeout=600),'NewFuture update check')
        staged=str(checked.get('staged_runtime_id','')); shash=str(checked.get('staged_binary_sha256',''))
        if not staged or not shash or str(checked.get('current_runtime_id','')) != aid:
            raise RuntimeError('real external update did not stage a distinct qualified runtime while leaving active A unchanged')
        if staged == aid or shash == ahash: raise RuntimeError('real external update did not produce distinct staged runtime bytes')
        state_obj=checked.get('state') if isinstance(checked.get('state'),dict) else {}
        if state_obj.get('last_result')!='staged' or state_obj.get('last_resolution_id') != resolutions['newfuture']['resolution_id']:
            # update check re-resolves; identity should be stable for unchanged latest endpoint.
            raise RuntimeError('update manager stage state is not bound to immutable NewFuture resolution')
        sman=parse_obj(racctl(binary,env,['runtime','inspect',staged]),'staged runtime inspect')
        if sman.get('qualification',{}).get('qualified') is not True or sman.get('source',{}).get('resolution_id') != resolutions['newfuture']['resolution_id']:
            raise RuntimeError('staged runtime manifest is not qualified and source-resolution bound')
        archive_sha=str(sman.get('archive_sha256','')); binary_sha=str(sman.get('binary_sha256',''))
        asset_digest=str(resolutions['newfuture']['asset'].get('digest','')).lower().removeprefix('sha256:')
        if asset_digest and archive_sha != asset_digest: raise RuntimeError('downloaded NewFuture archive bytes do not match GitHub SHA-256 identity')
        if binary_sha != shash: raise RuntimeError('staged update binary identity diverged from runtime manifest')
        # P411: checking/staging must not hot-swap the active mount process.
        ma_after=g1.mount_status(binary,env,'source-g1')
        if int(ma_after.get('pid') or 0)!=pida or proc_hash(pida,state)!=ahash: raise RuntimeError('update check hot-swapped beneath an active mount')

        mark('explicitly activate staged external runtime and prove live process bytes')
        activated=parse_obj(racctl(binary,env,['runtime','update','activate'],timeout=360),'update activate')
        sb=update_snapshot(binary,env); mb=g1.mount_status(binary,env,'source-g1'); pidb=int(mb.get('pid') or 0); phash_b=proc_hash(pidb,state)
        if str(sb.get('current_runtime_id',''))!=staged or phash_b!=shash or pidb==pida: raise RuntimeError('explicit update activation did not restart mount under staged external bytes')

        mark('one-click rollback and prove prior executable bytes restored')
        rolled=parse_obj(racctl(binary,env,['runtime','update','rollback'],timeout=360),'update rollback')
        sr=update_snapshot(binary,env); mr=g1.mount_status(binary,env,'source-g1'); pidr=int(mr.get('pid') or 0); phash_r=proc_hash(pidr,state)
        if str(sr.get('current_runtime_id',''))!=aid or phash_r!=ahash or pidr==pidb: raise RuntimeError('one-click rollback did not restore runtime A process bytes')

        mark('exercise adversarial acquisition failures through production update path')
        negative_dir=tmp/'negative'; negative_dir.mkdir(); bads=make_bad_archives(negative_dir)
        goodish=negative_dir/'payload.bin'; goodish.write_bytes(b'not-rclone-source-g1')
        server,port=start_http(negative_dir)
        negatives={}
        try:
            stable=update_snapshot(binary,env)
            wrong='0'*64 if file_sha256(goodish)!='0'*64 else '1'*64
            register_url(binary,env,'g1-wrong-hash',f'http://127.0.0.1:{port}/{goodish.name}',wrong)
            negatives['hash_mismatch']=run_negative(binary,env,'g1-wrong-hash',stable)
            for label,path in bads.items():
                sid='g1-'+label; register_url(binary,env,sid,f'http://127.0.0.1:{port}/{path.name}',file_sha256(path)); negatives[label]=run_negative(binary,env,sid,stable)
        finally:
            server.terminate(); server.wait(timeout=5)
        mark('exercise disappeared/offline source retry without corrupting state')
        stable=update_snapshot(binary,env)
        # Reuse now-dead loopback port: the source existed syntactically but acquisition is unavailable.
        register_url(binary,env,'g1-offline',f'http://127.0.0.1:{port}/{goodish.name}',file_sha256(goodish))
        negatives['offline_source']=run_negative(binary,env,'g1-offline',stable)
        if not negatives['offline_source']['retryable']:
            raise RuntimeError('offline update failure was not retained as retryable state')

        mark('exercise SOURCE-X02 real Android build when NDK is available')
        build_probe=real_builder_probe(find_ndk(),tmp)

        # Snapshot mutable final state before teardown/cleanup.
        mark('snapshot durable source/update authority evidence')
        refs=[]
        for rel,name,kind in ((f'{state}/runtime-update/state.json','update-state.json','update-state'),(f'{state}/runtime-update/policy.json','update-policy.json','update-policy'),(f'{state}/runtimes/{aid}/manifest.json','baseline-manifest.json','runtime-manifest'),(f'{state}/runtimes/{staged}/manifest.json','external-manifest.json','runtime-manifest')):
            if g1.root_run(['test','-f',rel],timeout=5).returncode==0: refs.append(snapshot(rel,snapshot_dir,name,kind))
        refs.extend(resolution_refs)
        refs.append(g1.evidence_ref(f'{state}/runtimes/{aid}/rclone','runtime-binary'))
        refs.append(g1.evidence_ref(f'{state}/runtimes/{staged}/rclone','runtime-binary'))
        refs.append(g1.evidence_ref(managed_fuse_helper,'fuse-helper'))
        refs.append(g1.evidence_ref(managed_fuse_manifest,'fuse-helper-manifest'))

        # Stop the isolated mount only after immutable snapshots are captured.
        racctl(binary,env,['compat','mountctl','stop','source-g1'],check=False,timeout=60)

        evidence={
            'schema_version':SCHEMA_VERSION,'harness_version':HARNESS_VERSION,'status':'PASS','captured_at':now(),'session':session,
            'source_bindings':source_bindings(),
            'device':{'sdk':g1.prop('ro.build.version.sdk'),'abi':g1.prop('ro.product.cpu.abi'),'fingerprint_sha256':hashlib.sha256(g1.prop('ro.build.fingerprint').encode()).hexdigest()},
            'sources':resolutions,
            'baseline_source':baseline_resolution,
            'source_build':build_probe,
            'fuse_helper':{
                'repository':str(helper_manifest.get('repository','')),
                'release_tag':str(helper_manifest.get('release_tag','')),
                'asset_id':int(helper_manifest.get('asset_id') or 0),
                'asset_name':str(helper_manifest.get('asset_name','')),
                'archive_sha256':str(helper_manifest.get('archive_sha256','')),
                'helper_sha256':helper_hash,
            },
            'flow':{
                'baseline':{'runtime_id':aid,'binary_sha256':ahash,'mount_pid':pida,'process_sha256':phash_a,'resolution_id':baseline_resolution['resolution_id'],'release_tag':baseline_resolution['release_tag']},
                'staged':{'runtime_id':staged,'binary_sha256':shash,'archive_sha256':archive_sha,'resolution_id':resolutions['newfuture']['resolution_id']},
                'activated':{'runtime_id':str(sb.get('current_runtime_id','')),'mount_pid':pidb,'process_sha256':phash_b},
                'rollback':{'runtime_id':str(sr.get('current_runtime_id','')),'mount_pid':pidr,'process_sha256':phash_r},
            },
            'negatives':negatives,'references':refs,
        }
        evidence['document_sha256']=digest({k:v for k,v in evidence.items() if k!='document_sha256'})
        out_path.parent.mkdir(parents=True,exist_ok=True); out_path.write_text(json.dumps(evidence,indent=2,sort_keys=True)+'\n',encoding='utf-8')
        return evidence


def verify(path: Path, physical: bool = True) -> dict:
    try: data=json.loads(path.read_text(encoding='utf-8'))
    except Exception as e: raise RuntimeError(f'cannot read SOURCE-G1 evidence: {e}')
    if data.get('schema_version')!=SCHEMA_VERSION or data.get('harness_version')!=HARNESS_VERSION or data.get('status')!='PASS': raise RuntimeError('SOURCE-G1 evidence schema/status mismatch')
    copy=dict(data); got=str(copy.pop('document_sha256','')); 
    if got!=digest(copy): raise RuntimeError('SOURCE-G1 evidence document digest mismatch')
    if data.get('source_bindings') != source_bindings(): raise RuntimeError('SOURCE-G1 evidence source bindings are stale')
    sources=data.get('sources');
    if not isinstance(sources,dict) or set(sources)!= {'bclone','rclone','newfuture'}: raise RuntimeError('SOURCE-G1 did not prove all canonical external sources')
    for r in sources.values(): resolution_identity(r)
    nf=sources['newfuture'];
    if nf.get('asset',{}).get('name')!='magisk-rclone_arm64-v8a.zip': raise RuntimeError('SOURCE-G1 NewFuture Android asset binding missing')
    baseline_source=data.get('baseline_source') or {}
    resolution_identity(baseline_source)
    if baseline_source.get('source_id')!='newfuture' or baseline_source.get('channel')!='pinned-release': raise RuntimeError('SOURCE-G1 baseline is not a production pinned NewFuture release')
    if baseline_source.get('release_tag')==nf.get('release_tag') or baseline_source.get('release_id')==nf.get('release_id'): raise RuntimeError('SOURCE-G1 historical baseline is not distinct from latest NewFuture release')
    if baseline_source.get('asset',{}).get('name')!='magisk-rclone_arm64-v8a.zip': raise RuntimeError('SOURCE-G1 historical baseline Android asset binding missing')
    flow=data.get('flow') or {}; a=flow.get('baseline') or {}; s=flow.get('staged') or {}; act=flow.get('activated') or {}; rb=flow.get('rollback') or {}
    if not a.get('runtime_id') or a.get('binary_sha256')!=a.get('process_sha256'): raise RuntimeError('SOURCE-G1 baseline execution is not byte-bound')
    if a.get('resolution_id')!=baseline_source.get('resolution_id') or a.get('release_tag')!=baseline_source.get('release_tag'): raise RuntimeError('SOURCE-G1 baseline runtime is not bound to historical NewFuture resolution')
    if not s.get('runtime_id') or s.get('runtime_id')==a.get('runtime_id') or s.get('binary_sha256')==a.get('binary_sha256'): raise RuntimeError('SOURCE-G1 staged candidate identity is not distinct')
    if s.get('resolution_id')!=nf.get('resolution_id'): raise RuntimeError('SOURCE-G1 staged runtime is not bound to NewFuture immutable resolution')
    if act.get('runtime_id')!=s.get('runtime_id') or act.get('process_sha256')!=s.get('binary_sha256'): raise RuntimeError('SOURCE-G1 activation did not execute staged bytes')
    if rb.get('runtime_id')!=a.get('runtime_id') or rb.get('process_sha256')!=a.get('binary_sha256'): raise RuntimeError('SOURCE-G1 rollback did not restore baseline bytes')
    neg=data.get('negatives') or {}
    for key in ('hash_mismatch','malformed','traversal','symlink','offline_source'):
        if key not in neg or int(neg[key].get('returncode',0))==0 or neg[key].get('error_redacted') is not True: raise RuntimeError(f'SOURCE-G1 negative proof missing/unsafe: {key}')
    if neg['offline_source'].get('retryable') is not True: raise RuntimeError('SOURCE-G1 offline failure not retryable')
    helper=data.get('fuse_helper') or {}
    if helper.get('repository')!='NewFuture/rclone-fuse3-magisk' or helper.get('asset_name')!='magisk-rclone_arm64-v8a.zip': raise RuntimeError('SOURCE-G1 managed FUSE helper authority is not NewFuture-bound')
    if int(helper.get('asset_id') or 0)<=0 or len(str(helper.get('archive_sha256','')))!=64 or len(str(helper.get('helper_sha256','')))!=64: raise RuntimeError('SOURCE-G1 managed FUSE helper immutable identity is incomplete')
    bp=data.get('source_build') or {}
    if bp.get('supported') is True:
        if bp.get('goos')!='android' or bp.get('goarch')!='arm64' or bp.get('abi')!='arm64-v8a' or len(str(bp.get('binary_sha256','')))!=64: raise RuntimeError('SOURCE-G1 real NDK build proof malformed')
        if bp.get('compiler_mode') not in ('ndk-prebuilt','native-clang-ndk-sysroot'): raise RuntimeError('SOURCE-G1 real NDK build compiler mode missing/invalid')
    else:
        reason=str(bp.get('reason','')).strip()
        if not reason: raise RuntimeError('SOURCE-G1 unsupported NDK build proof has no environment reason')
        if bp.get('compiler_present') is True and 'not runnable' not in reason: raise RuntimeError('SOURCE-G1 installed NDK compiler was skipped without a runnable-host failure reason')
    refs=data.get('references');
    if not isinstance(refs,list) or len(refs)<7: raise RuntimeError('SOURCE-G1 durable evidence references incomplete')
    if physical:
        resolution_refs = {}
        for ref in refs:
            if not isinstance(ref,dict) or len(str(ref.get('sha256','')))!=64 or not ref.get('path'): raise RuntimeError('SOURCE-G1 evidence reference malformed')
            if g1.root_hash(str(ref['path'])) != ref['sha256']: raise RuntimeError(f"SOURCE-G1 evidence reference missing/stale: {ref['path']}")
            if ref.get('kind') in {'source-resolution','source-resolution-baseline'}:
                try:
                    snap = json.loads(g1.root_text(str(ref['path'])))
                except Exception as exc:
                    raise RuntimeError(f"SOURCE-G1 source-resolution snapshot is unreadable: {ref['path']}: {exc}") from exc
                rid = str(snap.get('resolution_id',''))
                if not rid:
                    raise RuntimeError(f"SOURCE-G1 source-resolution snapshot has no resolution_id: {ref['path']}")
                resolution_refs[rid] = snap
        helper_refs = {str(ref.get('kind','')): ref for ref in refs if isinstance(ref,dict) and str(ref.get('kind','')).startswith('fuse-helper')}
        if set(helper_refs) != {'fuse-helper','fuse-helper-manifest'}:
            raise RuntimeError('SOURCE-G1 managed FUSE helper physical evidence is incomplete')
        try:
            persisted_helper = json.loads(g1.root_text(str(helper_refs['fuse-helper-manifest']['path'])))
        except Exception as exc:
            raise RuntimeError(f'SOURCE-G1 managed FUSE helper manifest is unreadable: {exc}') from exc
        if persisted_helper.get('repository') != helper.get('repository') or persisted_helper.get('release_tag') != helper.get('release_tag') or int(persisted_helper.get('asset_id') or 0) != int(helper.get('asset_id') or 0) or persisted_helper.get('asset_name') != helper.get('asset_name') or persisted_helper.get('archive_sha256') != helper.get('archive_sha256') or persisted_helper.get('helper_sha256') != helper.get('helper_sha256'):
            raise RuntimeError('SOURCE-G1 managed FUSE helper manifest diverges from evidence authority')
        if helper_refs['fuse-helper']['sha256'] != helper.get('helper_sha256'):
            raise RuntimeError('SOURCE-G1 managed FUSE helper bytes diverge from evidence authority')
        persisted_sources = dict(sources)
        persisted_sources['newfuture-baseline'] = baseline_source
        for sid, source in persisted_sources.items():
            rid = str(source.get('resolution_id',''))
            snap = resolution_refs.get(rid)
            if snap is None:
                raise RuntimeError(f'SOURCE-G1 durable source-resolution snapshot missing for {sid}: {rid}')
            # The immutable authority fields captured in the evidence document
            # must be the same ones persisted by the production resolution
            # store, not merely parallel JSON assembled by the harness.
            for key in ('resolution_id','source_id','spec_digest','engine','kind','channel','repository','repository_id','release_id','release_tag','commit_sha'):
                if snap.get(key) != source.get(key):
                    raise RuntimeError(f'SOURCE-G1 persisted {sid} resolution diverges at {key}')
            if snap.get('asset') != source.get('asset'):
                raise RuntimeError(f'SOURCE-G1 persisted {sid} resolution asset identity diverges')
    return data


def main()->int:
    ap=argparse.ArgumentParser(); sub=ap.add_subparsers(dest='cmd',required=True)
    c=sub.add_parser('capture'); c.add_argument('--output',default=str(DEFAULT_EVIDENCE))
    v=sub.add_parser('verify'); v.add_argument('--evidence',default=str(DEFAULT_EVIDENCE)); v.add_argument('--no-physical',action='store_true')
    a=ap.parse_args()
    try:
        if a.cmd=='capture':
            d=capture(Path(a.output)); verify(Path(a.output),physical=True); print(json.dumps({'status':'PASS','session':d['session'],'evidence':a.output},sort_keys=True))
        else:
            d=verify(Path(a.evidence),physical=not a.no_physical); print(json.dumps({'status':'PASS','session':d.get('session','')},sort_keys=True))
        return 0
    except Exception as e:
        print(f'SOURCE-G1 device qualification: FAIL: {e}',file=sys.stderr); return 1

if __name__=='__main__': raise SystemExit(main())
