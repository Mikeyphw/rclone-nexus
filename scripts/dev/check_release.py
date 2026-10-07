#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import json
from pathlib import Path
from zipfile import ZipFile

ROOT = Path(__file__).resolve().parents[2]
PROP = ROOT / "module" / "module.prop"
props = dict(line.split("=", 1) for line in PROP.read_text().splitlines() if "=" in line)
version = props["version"].lstrip("v")
if version != "0.1.0":
    raise SystemExit(f"REL-X01 expects v0.1.0, got {version}")

archive = ROOT / "dist" / f"rclone-nexus-v{version}.zip"
sums = ROOT / "dist" / "SHA256SUMS"
manifest_path = ROOT / "dist" / "release-manifest.json"
for p in (archive, sums, manifest_path):
    if not p.is_file(): raise SystemExit(f"missing release artifact: {p.relative_to(ROOT)}")
data = archive.read_bytes(); digest = hashlib.sha256(data).hexdigest()
line = sums.read_text().strip()
if line != f"{digest}  {archive.name}": raise SystemExit("SHA256SUMS mismatch")
manifest = json.loads(manifest_path.read_text())
if manifest.get("schema_version") != 1 or manifest.get("version") != "v0.1.0" or manifest.get("evidence_schema") != 3: raise SystemExit("release manifest identity/evidence-schema mismatch")
arts = manifest.get("artifacts", [])
if len(arts) != 1 or arts[0].get("sha256") != digest or arts[0].get("size") != len(data): raise SystemExit("release manifest artifact mismatch")
with ZipFile(archive) as zf:
    if zf.read("module.prop").decode().find("version=v0.1.0\n") < 0: raise SystemExit("packaged module version mismatch")
    names = set(zf.namelist())
    if "system/bin/rclone" not in names: raise SystemExit("release is missing authoritative bundled system/bin/rclone")
    forbidden = [n for n in names if n.rstrip('/').split('/')[-1] in {"fusermount", "fusermount3", "libfuse.so", "libfuse3.so"}]
    if forbidden: raise SystemExit(f"release bundles forbidden FUSE runtime payload: {forbidden}")
    integrity = json.loads(zf.read("integrity.manifest.json"))
    manifest_paths = {entry.get("path") for entry in integrity.get("entries", [])}
    if "system/bin/rclone" not in manifest_paths: raise SystemExit("release integrity manifest does not bind system/bin/rclone")

required_docs = {
    "docs/release/INSTALLATION.md": ["Install", "Update", "Uninstall", "Recovery"],
    "docs/release/MIGRATION_v0.1.0.md": ["v0.1.0-dev", "v0.1.0", "persistent"],
    "docs/release/CONFIGURATION.md": ["Mounts", "Jobs", "VFS", "policy"],
    "docs/release/COMPATIBILITY.md": ["Magisk", "KernelSU", "APatch", "namespace"],
    "docs/release/RELEASE_NOTES_v0.1.0.md": ["Known limitations", "WebUI", "NewFuture"],
}
for rel, needles in required_docs.items():
    text = (ROOT / rel).read_text(encoding="utf-8")
    for needle in needles:
        if needle.lower() not in text.lower(): raise SystemExit(f"{rel} missing release promise: {needle}")
print("release contract: OK")
