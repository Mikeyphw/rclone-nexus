#!/system/bin/sh
MODDIR=${0%/*}
export RNEXUS_MODULE_DIR="$MODDIR"
# shellcheck disable=SC1091
. "$MODDIR/lib/common.sh"

rnexus_init_state
racctl=$(rnexus_racctl_bin) || {
  rnexus_log 'service: racctl backend unavailable'
  exit 1
}

if [ ! -f "$RNEXUS_RUN_DIR/platform-ready" ]; then
  rnexus_log 'service: platform preflight did not complete; refusing daemon startup'
  exit 1
fi

# Boot is a production ingress and must not silently run through a legacy
# provider/PATH authority. The native resolver is the canonical decision.
if ! "$racctl" runtime status --json --require-operational >>"$RNEXUS_LOG_DIR/service.log" 2>&1; then
  rnexus_log "service: canonical runtime authority is not operational; refusing boot reconcile"
  exit 1
fi

rnexus_rotate_log "$RNEXUS_LOG_DIR/racd.log"
rnexus_rotate_log "$RNEXUS_LOG_DIR/service.log"

# racd is single-instance locked. The native supervisor owns boot/framework/
# provider/storage/network readiness; shell does not duplicate that state machine.
"$racctl" racd >>"$RNEXUS_LOG_DIR/racd.log" 2>&1 &

i=0
while [ ! -S "$RNEXUS_RUN_DIR/racd.sock" ] && [ "$i" -lt 10 ]; do
  sleep 1
  i=$((i + 1))
done

if [ ! -S "$RNEXUS_RUN_DIR/racd.sock" ]; then
  rnexus_log 'service: racd socket did not become ready; reconcile will use local backend fallback'
fi

rnexus_log "service: boot reconcile starting"
"$MODDIR/system/bin/rclone-nexus" reconcile --boot >>"$RNEXUS_LOG_DIR/service.log" 2>&1 || \
  rnexus_log "service: reconcile reported failure"
