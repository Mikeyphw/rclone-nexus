export const DEFAULT_MOUNT = Object.freeze({
  name: '', enabled: true, remote: '', mountpoint: '', vfs_cache_mode: 'full', vfs_cache_max_size: '',
  vfs_cache_max_age: '', dir_cache_time: '', poll_interval: '', allow_other: true, read_only: false,
  log_level: 'INFO', require_network: false, probe_remote: false, network_mode: 'offline-allowed',
  charging_only: false, min_battery: 0, min_free_cache_space: '', boot_settle: '', network_settle: '',
  vfs_profile: 'custom', cache_high_water: 90, cache_low_water: 75,
});

function number(value, fallback = 0) {
  const parsed = Number.parseInt(String(value ?? ''), 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

export function candidate(value = {}) {
  const out = { ...DEFAULT_MOUNT, ...value };
  out.name = String(out.name ?? '').trim();
  out.remote = String(out.remote ?? '').trim();
  out.mountpoint = String(out.mountpoint ?? '').trim();
  out.vfs_cache_mode = String(out.vfs_cache_mode || 'full').trim().toLowerCase();
  out.vfs_profile = String(out.vfs_profile || 'custom').trim().toLowerCase();
  out.log_level = String(out.log_level || 'INFO').trim().toUpperCase();
  out.network_mode = String(out.network_mode || 'offline-allowed').trim().toLowerCase();
  for (const key of ['vfs_cache_max_size','vfs_cache_max_age','dir_cache_time','poll_interval','min_free_cache_space','boot_settle','network_settle']) {
    out[key] = String(out[key] ?? '').trim();
  }
  for (const key of ['enabled','allow_other','read_only','require_network','probe_remote','charging_only']) out[key] = Boolean(out[key]);
  out.min_battery = number(out.min_battery, 0);
  out.cache_high_water = number(out.cache_high_water, 90);
  out.cache_low_water = number(out.cache_low_water, 75);
  delete out.has_args_file;
  return out;
}

export function replaceMount(mounts, originalName, next) {
  const clean = candidate(next);
  const source = Array.isArray(mounts) ? mounts : [];
  const result = source.filter((item) => item?.name !== originalName).map(candidate);
  result.push(clean);
  result.sort((a, b) => a.name.localeCompare(b.name));
  return result;
}

export function removeMount(mounts, name) {
  return (Array.isArray(mounts) ? mounts : []).filter((item) => item?.name !== name).map(candidate);
}

export function mountRows(snapshot, statuses, health) {
  const statusMap = new Map((Array.isArray(statuses) ? statuses : []).map((item) => [item.name, item]));
  const healthMap = new Map((Array.isArray(health) ? health : []).map((item) => [item.name, item]));
  return (snapshot?.mounts || []).map((config) => ({ config, status: statusMap.get(config.name) || {}, health: healthMap.get(config.name) || {} }));
}

export function previewIsUsable(preview, snapshot) {
  return Boolean(preview?.preview_proof && preview?.candidate_digest && Number(preview?.current_revision) === Number(snapshot?.revision));
}

export function isDestructivePreview(preview) {
  return (preview?.changes || []).some((change) => change.kind === 'delete' || (change.reasons || []).includes('mountpoint'));
}

export function operationSummary(record) {
  const events = Array.isArray(record?.events) ? record.events : [];
  const last = events.at(-1) || {};
  return {
    requestId: record?.request_id || '', operation: record?.operation || 'operation', state: record?.state || 'UNKNOWN',
    cancellable: Boolean(record?.cancellable), message: last.message || record?.error?.message || '',
    event: last.event || '', updatedUnixMS: Number(record?.updated_unix_ms || 0), events,
  };
}

export function consequenceLabel(value) {
  const labels = {
    lifecycle_start: 'start mount', lifecycle_stop: 'stop mount', lifecycle_restart: 'restart mount',
    namespace_requalify: 're-qualify app visibility', namespace_release: 'release Nexus namespace binds',
    cache_preserved: 'preserve cache files', cache_policy_recheck: 're-check VFS/cache policy',
    policy_recheck: 're-check resource policy', configuration_create: 'create configuration',
  };
  return labels[value] || String(value || '').replaceAll('_', ' ');
}
