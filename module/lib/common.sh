#!/system/bin/sh

RNEXUS_MODULE_ID=rclone_nexus
RNEXUS_PROVIDER_MODULE_ID=rclone
RNEXUS_MODULE_DIR=${RNEXUS_MODULE_DIR:-/data/adb/modules/$RNEXUS_MODULE_ID}
RNEXUS_PROVIDER_MODULE_DIR=${RNEXUS_PROVIDER_MODULE_DIR:-/data/adb/modules/$RNEXUS_PROVIDER_MODULE_ID}
RNEXUS_STATE_DIR=${RNEXUS_STATE_DIR:-/data/adb/rclone-nexus}
RNEXUS_MOUNTS_DIR=${RNEXUS_MOUNTS_DIR:-$RNEXUS_STATE_DIR/mounts.d}
RNEXUS_RUN_DIR=${RNEXUS_RUN_DIR:-$RNEXUS_STATE_DIR/run}
RNEXUS_LOG_DIR=${RNEXUS_LOG_DIR:-$RNEXUS_STATE_DIR/logs}
RNEXUS_CACHE_DIR=${RNEXUS_CACHE_DIR:-$RNEXUS_STATE_DIR/cache}
RNEXUS_CONFIG_DIR=${RNEXUS_CONFIG_DIR:-$RNEXUS_STATE_DIR/config}
RNEXUS_RUNTIME_DIR=${RNEXUS_RUNTIME_DIR:-$RNEXUS_STATE_DIR/runtime}
RNEXUS_MANAGED_RUNTIME_DIR=${RNEXUS_MANAGED_RUNTIME_DIR:-$RNEXUS_RUNTIME_DIR/active}
RNEXUS_MANAGED_RCLONE_BIN=${RNEXUS_MANAGED_RCLONE_BIN:-$RNEXUS_MANAGED_RUNTIME_DIR/bin/rclone}
RNEXUS_MANAGED_CONFIG_DIR=${RNEXUS_MANAGED_CONFIG_DIR:-$RNEXUS_CONFIG_DIR/rclone}
RNEXUS_MANAGED_RCLONE_CONFIG=${RNEXUS_MANAGED_RCLONE_CONFIG:-$RNEXUS_MANAGED_CONFIG_DIR/rclone.conf}
RNEXUS_DESIRED_DIR=${RNEXUS_DESIRED_DIR:-$RNEXUS_STATE_DIR/desired}
RNEXUS_MOUNT_RUN_DIR=${RNEXUS_MOUNT_RUN_DIR:-$RNEXUS_RUN_DIR/mounts}
RNEXUS_LOCK_DIR=${RNEXUS_LOCK_DIR:-$RNEXUS_RUN_DIR/locks}
RNEXUS_DIAGNOSTICS_DIR=${RNEXUS_DIAGNOSTICS_DIR:-$RNEXUS_STATE_DIR/diagnostics}
RNEXUS_SUPPORT_DIR=${RNEXUS_SUPPORT_DIR:-$RNEXUS_DIAGNOSTICS_DIR/support}
RNEXUS_PLATFORM_DIR=${RNEXUS_PLATFORM_DIR:-$RNEXUS_STATE_DIR/platform}

# Runtime authority is native (`internal/runtimeauth`), not shell-derived.
# Shell only loads legacy provider environment after the caller explicitly opts
# into external compatibility.  Provider-controlled env must never select or
# rewrite managed runtime mode.
case "${RNEXUS_RUNTIME_MODE:-}" in
  ''|managed|external|migration-required) ;;
  *)
    printf 'rclone-nexus: invalid RNEXUS_RUNTIME_MODE: %s\n' "$RNEXUS_RUNTIME_MODE" >&2
    return 1 2>/dev/null || exit 1
    ;;
esac

rnexus_external_compat_requested() {
  [ "${RNEXUS_RUNTIME_MODE:-}" = external ] || \
    [ -n "${RNEXUS_EXTERNAL_RCLONE_BIN:-}" ] || \
    [ -n "${RNEXUS_RCLONE_BIN:-}" ]
}

if rnexus_external_compat_requested && [ -f "$RNEXUS_PROVIDER_MODULE_DIR/env" ]; then
  rnexus_explicit_runtime_mode=${RNEXUS_RUNTIME_MODE:-}
  RNEXUS_PROVIDER_MODPATH=$RNEXUS_PROVIDER_MODULE_DIR
  MODPATH=$RNEXUS_PROVIDER_MODULE_DIR
  export MODPATH
  set -a
  # shellcheck disable=SC1090
  . "$RNEXUS_PROVIDER_MODULE_DIR/env"
  set +a
  MODPATH=$RNEXUS_MODULE_DIR
  export MODPATH
  # A provider env is compatibility input, never runtime-mode authority.
  if [ -n "$rnexus_explicit_runtime_mode" ]; then
    RNEXUS_RUNTIME_MODE=$rnexus_explicit_runtime_mode
    export RNEXUS_RUNTIME_MODE
  else
    unset RNEXUS_RUNTIME_MODE
  fi
fi

# RCLONE_CONFIG is a compatibility environment projection only.  Managed
# execution receives the canonical config path from the native resolver and
# always passes it explicitly to rclone.
case "${RNEXUS_RUNTIME_MODE:-}" in
  managed)
    RCLONE_CONFIG=$RNEXUS_MANAGED_RCLONE_CONFIG
    export RCLONE_CONFIG
    ;;
  external)
    RNEXUS_DEFAULT_RCLONE_CONFIG=${RNEXUS_EXTERNAL_RCLONE_CONFIG:-${RCLONE_CONFIG:-$RNEXUS_PROVIDER_MODULE_DIR/conf/rclone.conf}}
    RCLONE_CONFIG=${RCLONE_CONFIG:-$RNEXUS_DEFAULT_RCLONE_CONFIG}
    export RCLONE_CONFIG
    ;;
esac

rnexus_now() {
  date '+%Y-%m-%dT%H:%M:%S%z' 2>/dev/null || date
}

rnexus_init_state() {
  umask 077
  mkdir -p "$RNEXUS_STATE_DIR" "$RNEXUS_MOUNTS_DIR" "$RNEXUS_RUN_DIR" "$RNEXUS_LOG_DIR" "$RNEXUS_CACHE_DIR" \
    "$RNEXUS_CONFIG_DIR" "$RNEXUS_RUNTIME_DIR" "$RNEXUS_MANAGED_CONFIG_DIR" "$RNEXUS_DESIRED_DIR" "$RNEXUS_MOUNT_RUN_DIR" "$RNEXUS_LOCK_DIR" \
    "$RNEXUS_DIAGNOSTICS_DIR" "$RNEXUS_SUPPORT_DIR" "$RNEXUS_PLATFORM_DIR"
  chmod 0700 "$RNEXUS_STATE_DIR" "$RNEXUS_MOUNTS_DIR" "$RNEXUS_RUN_DIR" "$RNEXUS_LOG_DIR" "$RNEXUS_CACHE_DIR" \
    "$RNEXUS_CONFIG_DIR" "$RNEXUS_RUNTIME_DIR" "$RNEXUS_MANAGED_CONFIG_DIR" "$RNEXUS_DESIRED_DIR" "$RNEXUS_MOUNT_RUN_DIR" "$RNEXUS_LOCK_DIR" \
    "$RNEXUS_DIAGNOSTICS_DIR" "$RNEXUS_SUPPORT_DIR" "$RNEXUS_PLATFORM_DIR" 2>/dev/null || true
}

rnexus_rotate_log() {
  path=$1
  max=${RNEXUS_LOG_MAX_BYTES:-1048576}
  backups=${RNEXUS_LOG_BACKUPS:-4}
  [ -f "$path" ] || return 0
  size=$(wc -c <"$path" 2>/dev/null || printf '0')
  [ "$size" -ge "$max" ] 2>/dev/null || return 0
  i=$backups
  while [ "$i" -gt 1 ]; do
    prev=$((i - 1))
    [ -f "$path.$prev" ] && mv -f "$path.$prev" "$path.$i"
    i=$prev
  done
  mv -f "$path" "$path.1"
}

rnexus_log() {
  rnexus_init_state
  rnexus_rotate_log "$RNEXUS_LOG_DIR/nexus.log"
  printf '%s %s\n' "$(rnexus_now)" "$*" >>"$RNEXUS_LOG_DIR/nexus.log"
}

rnexus_die() {
  printf 'rclone-nexus: %s\n' "$*" >&2
  return 1
}

rnexus_rclone_bin() {
  racctl=$(rnexus_racctl_bin) || return 1
  "$racctl" runtime executable
}

rnexus_rclone_config() {
  racctl=$(rnexus_racctl_bin) || return 1
  "$racctl" runtime config
}

rnexus_racctl_bin() {
  if [ -n "${RNEXUS_RACCTL_BIN:-}" ] && [ -x "$RNEXUS_RACCTL_BIN" ]; then
    printf '%s\n' "$RNEXUS_RACCTL_BIN"
    return 0
  fi
  candidate="$RNEXUS_MODULE_DIR/system/bin/racctl"
  if [ -x "$candidate" ]; then
    printf '%s\n' "$candidate"
    return 0
  fi
  rnexus_die "racctl backend not found: $candidate"
}
