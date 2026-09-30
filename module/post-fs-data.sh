#!/system/bin/sh
MODDIR=${0%/*}
export RNEXUS_MODULE_DIR="$MODDIR"
# shellcheck disable=SC1091
. "$MODDIR/lib/common.sh"

rnexus_init_state
rnexus_log "post-fs-data: persistent state ready"
