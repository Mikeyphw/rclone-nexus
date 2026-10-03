#!/system/bin/sh
SKIPUNZIP=0

ui_print "- Rclone Nexus"
ui_print "- Nexus module only: rclone/FUSE are not bundled"

case "${ARCH:-}" in
  arm64|arm64-v8a|aarch64|'') ;;
  *) abort "! Rclone Nexus v0.1 currently ships racctl for arm64 only (detected: ${ARCH:-unknown})" ;;
esac

if [ -d /data/adb/modules/rclone ] || [ -d /data/adb/modules_update/rclone ]; then
  ui_print "- Optional NewFuture provider detected for migration/external compatibility"
else
  ui_print "- No NewFuture provider detected; managed runtime mode is standalone"
fi
ui_print "- Managed mounts require a Nexus-qualified managed runtime; import and activate one after reboot"

set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm_recursive "$MODPATH/system/bin" 0 0 0755 0755
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
[ -f "$MODPATH/integrity.manifest.json" ] || abort "! Rclone Nexus package is missing integrity.manifest.json"
ui_print "- Package staging checks passed"
ui_print "- Runtime/state/integrity checks will run via install-verify after reboot"
