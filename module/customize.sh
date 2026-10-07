#!/system/bin/sh
SKIPUNZIP=0

ui_print "- Rclone Nexus"
ui_print "- Static runtime mode: bundled rclone-family binary + NewFuture FUSE helper are authoritative"

case "${ARCH:-}" in
  arm64|arm64-v8a|aarch64|'') ;;
  *) abort "! Rclone Nexus v0.1 currently ships racctl for arm64 only (detected: ${ARCH:-unknown})" ;;
esac

if [ -d /data/adb/modules/rclone ] || [ -d /data/adb/modules_update/rclone ]; then
  ui_print "- NewFuture provider detected; it is only a possible build-time source, not runtime authority"
fi
ui_print "- Runtime replacement is done by flashing a new module ZIP"

set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm_recursive "$MODPATH/system/bin" 0 0 0755 0755
set_perm_recursive "$MODPATH/system/vendor" 0 0 0755 0644
set_perm "$MODPATH/system/vendor/bin/fusermount3" 0 0 0755
set_perm_recursive "$MODPATH/lib" 0 0 0755 0644
set_perm_recursive "$MODPATH/webroot" 0 0 0755 0644
set_perm "$MODPATH/post-fs-data.sh" 0 0 0755
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/action.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755

# KernelSU/Magisk/APatch install into a staging MODPATH. Runtime/platform
# validation is intentionally deferred until the module is active after reboot;
# invoking racctl here would validate the staging directory as if it were the
# installed runtime. Keep customize.sh limited to package-local checks.
[ -x "$MODPATH/system/bin/racctl" ] || abort "! Rclone Nexus package is missing system/bin/racctl"
[ -x "$MODPATH/system/bin/rclone" ] || abort "! Rclone Nexus package is missing bundled system/bin/rclone"
[ -x "$MODPATH/system/vendor/bin/fusermount3" ] || abort "! Rclone Nexus package is missing NewFuture-derived system/vendor/bin/fusermount3"
[ -f "$MODPATH/runtime.provenance.json" ] || abort "! Rclone Nexus package is missing runtime.provenance.json"
[ -f "$MODPATH/integrity.manifest.json" ] || abort "! Rclone Nexus package is missing integrity.manifest.json"
ui_print "- Package staging checks passed"
ui_print "- Runtime/state/integrity checks will run via install-verify after reboot"
