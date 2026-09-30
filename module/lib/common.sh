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
RNEXUS_DESIRED_DIR=${RNEXUS_DESIRED_DIR:-$RNEXUS_STATE_DIR/desired}
RNEXUS_MOUNT_RUN_DIR=${RNEXUS_MOUNT_RUN_DIR:-$RNEXUS_RUN_DIR/mounts}
RNEXUS_LOCK_DIR=${RNEXUS_LOCK_DIR:-$RNEXUS_RUN_DIR/locks}
RNEXUS_DIAGNOSTICS_DIR=${RNEXUS_DIAGNOSTICS_DIR:-$RNEXUS_STATE_DIR/diagnostics}
RNEXUS_SUPPORT_DIR=${RNEXUS_SUPPORT_DIR:-$RNEXUS_DIAGNOSTICS_DIR/support}
RNEXUS_PLATFORM_DIR=${RNEXUS_PLATFORM_DIR:-$RNEXUS_STATE_DIR/platform}
RNEXUS_DEFAULT_RCLONE_CONFIG=${RNEXUS_DEFAULT_RCLONE_CONFIG:-$RNEXUS_PROVIDER_MODULE_DIR/conf/rclone.conf}

rnexus_now() {
  date '+%Y-%m-%dT%H:%M:%S%z' 2>/dev/null || date
}

rnexus_init_state() {
  umask 077
  mkdir -p "$RNEXUS_STATE_DIR" "$RNEXUS_MOUNTS_DIR" "$RNEXUS_RUN_DIR" "$RNEXUS_LOG_DIR" "$RNEXUS_CACHE_DIR" \
    "$RNEXUS_CONFIG_DIR" "$RNEXUS_DESIRED_DIR" "$RNEXUS_MOUNT_RUN_DIR" "$RNEXUS_LOCK_DIR" \
    "$RNEXUS_DIAGNOSTICS_DIR" "$RNEXUS_SUPPORT_DIR" "$RNEXUS_PLATFORM_DIR"
  chmod 0700 "$RNEXUS_STATE_DIR" "$RNEXUS_MOUNTS_DIR" "$RNEXUS_RUN_DIR" "$RNEXUS_LOG_DIR" "$RNEXUS_CACHE_DIR" \
    "$RNEXUS_CONFIG_DIR" "$RNEXUS_DESIRED_DIR" "$RNEXUS_MOUNT_RUN_DIR" "$RNEXUS_LOCK_DIR" \
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
  if [ -n "${RNEXUS_RCLONE_BIN:-}" ] && [ -x "$RNEXUS_RCLONE_BIN" ]; then
    printf '%s\n' "$RNEXUS_RCLONE_BIN"
    return 0
  fi
  if command -v rclone >/dev/null 2>&1; then
    command -v rclone
    return 0
  fi
  for candidate in \
    "$RNEXUS_PROVIDER_MODULE_DIR/system/bin/rclone" \
    "$RNEXUS_PROVIDER_MODULE_DIR/bin/rclone" \
    "$RNEXUS_PROVIDER_MODULE_DIR/rclone"; do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

rnexus_rclone_config() {
  if [ -n "${RCLONE_CONFIG:-}" ]; then
    printf '%s\n' "$RCLONE_CONFIG"
  else
    printf '%s\n' "$RNEXUS_DEFAULT_RCLONE_CONFIG"
  fi
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
