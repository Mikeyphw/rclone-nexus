#!/system/bin/sh
MODDIR=${0%/*}
export RNEXUS_MODULE_DIR="$MODDIR"
# shellcheck disable=SC1091
. "$MODDIR/lib/common.sh"

rnexus_init_state

# Late-start service: wait on Android's real framework readiness signal rather
# than guessing one fixed boot delay.
i=0
while [ "$(getprop sys.boot_completed 2>/dev/null)" != "1" ] && [ "$i" -lt 180 ]; do
  sleep 1
  i=$((i + 1))
done

racctl=$(rnexus_racctl_bin) || {
  rnexus_log 'service: racctl backend unavailable'
  exit 1
}

# racd is single-instance locked. A duplicate launch exits immediately and is
# harmless; the readiness loop below keys off the actual root-owned socket.
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
