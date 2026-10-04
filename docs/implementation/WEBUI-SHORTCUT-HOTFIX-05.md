# WEBUI-SHORTCUT-HOTFIX-05 — root-manager home-screen shortcut support

## Purpose

Expose Rclone Nexus as a shortcut-capable WebUI to WebUI-X/MMRL without adding a browser-side privileged shortcut API.

## Contract

- `module/webroot/config.json` declares the canonical WebUI-X module identity, shortcut title, and icon.
- `module/webroot/icon.png` is a packaged 512×512 shortcut/favicon asset.
- `index.html` advertises the same icon as the page favicon.
- No new JavaScript privilege bridge, Android intent launcher, generic shell surface, or manager-specific `$bindhosts` compatibility shim is introduced.
- Module/package contracts require the metadata and icon, and the final WebUI contract executes `check_webui_shortcut.mjs`.

With a compatible WebUI-X/MMRL host, shortcut creation is host-owned. MMRL documents that supported WebUIs can be added by holding the module card; the module supplies the title/icon metadata the host needs.

## Validation

```sh
node scripts/dev/check_webui_shortcut.mjs
python3 scripts/dev/check-module-contract.py
node scripts/dev/check_webui_final.mjs
```
