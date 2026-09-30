#!/system/bin/sh
MODDIR=${0%/*}
export RNEXUS_MODULE_DIR="$MODDIR"
# shellcheck disable=SC1091
. "$MODDIR/lib/common.sh"

rnexus_init_state
racctl=$(rnexus_racctl_bin) || exit 1
ready="$RNEXUS_RUN_DIR/platform-ready"
rm -f "$ready"

# A corrupt/incomplete update must be rejected before persistent state is
# migrated. This keeps rollback to the previous module version safe.
if ! "$racctl" platform verify-integrity >>"$RNEXUS_LOG_DIR/platform.log" 2>&1; then
  rnexus_log 'post-fs-data: module integrity verification failed; service will not start'
  exit 1
fi
if ! "$racctl" platform migrate >>"$RNEXUS_LOG_DIR/platform.log" 2>&1; then
  rnexus_log 'post-fs-data: platform state migration failed; service will not start'
  exit 1
fi
: >"$ready"
chmod 0600 "$ready" 2>/dev/null || true
