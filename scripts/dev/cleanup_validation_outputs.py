#!/usr/bin/env python3
from __future__ import annotations

import json
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[2]
GENERATED_OUTPUTS = (
    "release/evidence/runtime-g1-device-qualification.json",
    "release/evidence/source-g1-supply-chain-qualification.json",
    "build/android/arm64-v8a/racctl",
    "dist/rclone-nexus-v0.1.0.zip",
    "dist/SHA256SUMS",
    "dist/release-manifest.json",
)


def git(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        cwd=ROOT,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=check,
    )


def tracked(path: str) -> bool:
    return git("ls-files", "--error-unmatch", "--", path, check=False).returncode == 0


def remove_untracked(path: Path) -> None:
    if path.is_symlink() or path.is_file():
        path.unlink()
    elif path.is_dir():
        shutil.rmtree(path)


def restore_generated_outputs() -> dict[str, object]:
    inside = git("rev-parse", "--is-inside-work-tree", check=False)
    if inside.returncode != 0 or inside.stdout.strip() != "true":
        raise SystemExit("GRAND-G1 validation cleanup requires a Git worktree")

    restored: list[str] = []
    removed: list[str] = []
    for rel in GENERATED_OUTPUTS:
        path = ROOT / rel
        if tracked(rel):
            # Restore the isolated transaction's Git baseline. Devtool preserves
            # primary-checkout dirty state separately, so copying that dirt into
            # this worktree would itself create an integration conflict.
            git("restore", "--worktree", "--source=HEAD", "--", rel)
            restored.append(rel)
        elif path.exists() or path.is_symlink():
            remove_untracked(path)
            removed.append(rel)

    status = git("status", "--porcelain=v1", "--untracked-files=all", "--", *GENERATED_OUTPUTS).stdout.strip()
    if status:
        raise SystemExit("GRAND-G1 validation cleanup left generated output drift:\n" + status)
    return {"status": "clean", "restored_tracked": restored, "removed_untracked": removed}


def main() -> None:
    print(json.dumps(restore_generated_outputs(), sort_keys=True))


if __name__ == "__main__":
    main()
