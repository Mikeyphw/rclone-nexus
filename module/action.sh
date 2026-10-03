#!/system/bin/sh
MODDIR=${0%/*}
export RNEXUS_MODULE_DIR="$MODDIR"
# shellcheck disable=SC1091
. "$MODDIR/lib/common.sh" || exit 1
racctl=$(rnexus_racctl_bin) || exit 1
exec "$racctl" webui start --open
