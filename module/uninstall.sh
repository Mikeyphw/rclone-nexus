#!/system/bin/sh
# Persistent configuration intentionally survives module removal. This avoids
# destroying remote definitions during upgrades/reinstalls.
STATE_DIR=${RNEXUS_STATE_DIR:-/data/adb/rclone-nexus}
if [ -d "$STATE_DIR" ]; then
  printf '%s\n' "Rclone Nexus uninstalled; persistent state preserved at $STATE_DIR" \
    >"$STATE_DIR/UNINSTALLED.txt" 2>/dev/null || true
fi
