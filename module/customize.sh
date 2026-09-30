#!/system/bin/sh
SKIPUNZIP=0

ui_print "- Rclone Nexus"
ui_print "- Nexus module only: rclone/FUSE are not bundled"

case "${ARCH:-}" in
  arm64|arm64-v8a|aarch64|'') ;;
  *) abort "! Rclone Nexus v0.1 currently ships racctl for arm64 only (detected: ${ARCH:-unknown})" ;;
esac

if [ -d /data/adb/modules/rclone ] || [ -d /data/adb/modules_update/rclone ]; then
  ui_print "- NewFuture rclone module detected"
else
  ui_print "! NewFuture rclone module (id: rclone) not detected"
  ui_print "! Install it before using managed mounts"
fi

set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm_recursive "$MODPATH/system/bin" 0 0 0755 0755
set_perm_recursive "$MODPATH/lib" 0 0 0755 0644
set_perm "$MODPATH/post-fs-data.sh" 0 0 0755
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/action.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755
