#!/system/bin/sh
MODDIR=${0%/*}
export RNEXUS_MODULE_DIR="$MODDIR"
# shellcheck disable=SC1091
. "$MODDIR/lib/common.sh"

rnexus_init_state

# Late-start service: do not guess a fixed boot delay. Wait until Android tells
# us framework boot completed, while still giving up eventually if properties
# are unavailable on an unusual root environment.
i=0
while [ "$(getprop sys.boot_completed 2>/dev/null)" != "1" ] && [ "$i" -lt 180 ]; do
  sleep 1
  i=$((i + 1))
done

rnexus_log "service: boot reconcile starting"
"$MODDIR/system/bin/rclone-nexus" reconcile --boot >>"$RNEXUS_LOG_DIR/service.log" 2>&1 || \
  rnexus_log "service: reconcile reported failure"
