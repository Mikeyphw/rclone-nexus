#!/system/bin/sh
MODDIR=${0%/*}
export RNEXUS_MODULE_DIR="$MODDIR"
STATE=${RNEXUS_STATE_DIR:-/data/adb/rclone-nexus}
RACCTL="$MODDIR/system/bin/racctl"

# Release only Nexus-owned namespace binds/mounts. Persistent configuration,
# cache, logs and journals survive by default. The user must explicitly arm the
# one-shot purge marker with `rclone-nexus platform purge-on-uninstall enable`.
if [ -x "$RACCTL" ]; then
  "$RACCTL" platform uninstall-hook >/dev/null 2>&1 || true
else
  rm -rf "$STATE/run" "$STATE/health"
fi
