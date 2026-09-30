#!/system/bin/sh
MODDIR=${0%/*}
export RNEXUS_MODULE_DIR="$MODDIR"
exec "$MODDIR/system/bin/racctl" webui start --open
