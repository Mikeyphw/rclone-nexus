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

const SIZE_RE = /^[0-9]+(?:\.[0-9]+)?(?:b|k|kb|kib|m|mb|mib|g|gb|gib|t|tb|tib|p|pb|pib)?$/i;
const DURATION_RE = /^(?:0|off|(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|µs|ms|s|m|h))+(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|µs|ms|s|m|h))*|[0-9]+(?:\.[0-9]+)?[dw])$/i;
const NAME_RE = /^[A-Za-z0-9._-]+$/;

function uiIssue(code, category, field, message, detail = '', suggestion = '', severity = 'error', relatedMount = '') {
  return { code, category, field, severity, message, detail, suggestion, related_mount: relatedMount };
}

export function parseRemoteEndpoint(value) {
  const raw = String(value ?? '').trim();
  const index = raw.indexOf(':');
  if (index <= 0) return { raw, remote: '', path: '' };
  return { raw, remote: raw.slice(0, index), path: raw.slice(index + 1).replace(/^\/+|\/+$/g, '') };
}

function titleCase(value) {
  return String(value || '').replace(/[._-]+/g, ' ').replace(/\s+/g, ' ').trim().replace(/\b\w/g, (m) => m.toUpperCase());
}

export function suggestMountDefaults(endpoint) {
  const parsed = parseRemoteEndpoint(endpoint);
  const segments = parsed.path.split('/').filter(Boolean);
  const label = titleCase(segments.at(-1) || parsed.remote || 'Mount');
  const source = [parsed.remote, ...segments.slice(-1)].filter(Boolean).join('-').toLowerCase();
  const name = source.replace(/[^a-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48) || 'mount';
  return { name, mountpoint: `/storage/emulated/0/Rclone/${label || 'Mount'}` };
}

export function localMountIssues(value, mounts = [], originalName = '') {
  const cfg = candidate(value);
  const issues = [];
  if (!cfg.name || !NAME_RE.test(cfg.name)) issues.push(uiIssue('invalid_mount_name', 'input', 'name', 'Use a valid mount name', 'Only letters, numbers, dot, underscore, and dash are allowed.', 'Example: drive-movies'));
  if (!cfg.remote) issues.push(uiIssue('remote_required', 'remote', 'remote', 'Choose a configured remote', '', 'Use the remote browser or enter remote:path manually.'));
  if (!cfg.mountpoint.startsWith('/')) issues.push(uiIssue('mountpoint_absolute_required', 'destination', 'mountpoint', 'Mount location must be an absolute path', cfg.mountpoint, '/storage/emulated/0/Rclone/<name>'));
  if (cfg.vfs_cache_max_size && !SIZE_RE.test(cfg.vfs_cache_max_size)) issues.push(uiIssue('invalid_cache_size', 'vfs', 'vfs_cache_max_size', 'Cache size is not valid', cfg.vfs_cache_max_size, 'Try 512MiB, 2GiB, or 8GiB.'));
  for (const [field, label] of [['vfs_cache_max_age','Cache maximum age'],['dir_cache_time','Directory cache duration'],['poll_interval','Poll interval'],['boot_settle','Boot settle delay'],['network_settle','Network settle delay']]) {
    const current = String(cfg[field] || '');
    if (current && !DURATION_RE.test(current)) issues.push(uiIssue('invalid_duration', field.includes('settle') ? 'advanced' : 'vfs', field, `${label} is not valid`, current, 'Try 30s, 15m, 24h, or 7d.'));
  }
  if (cfg.min_battery < 0 || cfg.min_battery > 100) issues.push(uiIssue('invalid_min_battery', 'policy', 'min_battery', 'Minimum battery must be between 0 and 100', String(cfg.min_battery), 'Use 0 to disable this threshold.'));
  if (!(cfg.cache_low_water >= 1 && cfg.cache_low_water < cfg.cache_high_water && cfg.cache_high_water <= 100)) issues.push(uiIssue('invalid_cache_watermarks', 'vfs', 'cache_low_water', 'Cache watermarks are inconsistent', `${cfg.cache_low_water}/${cfg.cache_high_water}`, 'Try 75% low-water and 90% high-water.'));
  if (cfg.mountpoint.startsWith('/')) {
    const clean = cfg.mountpoint.replace(/\/+$/g, '');
    for (const other of Array.isArray(mounts) ? mounts : []) {
      if (!other?.mountpoint || other?.name === originalName) continue;
      const theirs = String(other.mountpoint).replace(/\/+$/g, '');
      if (clean === theirs || clean.startsWith(`${theirs}/`) || theirs.startsWith(`${clean}/`)) {
        issues.push(uiIssue('mountpoint_overlap', 'destination', 'mountpoint', `Mount location overlaps “${other.name}”`, `${clean} ↔ ${theirs}`, 'Choose a separate folder.', 'error', other.name));
      }
    }
  }
  return issues;
}

export function profilePresentation(profile, profiles = [], recommendation = null) {
  const item = (Array.isArray(profiles) ? profiles : []).find((entry) => entry?.name === profile) || null;
  const recommended = recommendation?.profile === profile;
  return {
    name: profile,
    description: item?.description || (profile === 'custom' ? 'Use explicit VFS settings.' : ''),
    options: item?.options || {},
    recommended,
  };
}

export function runtimeManagerAction(manager, key) {
  const value = manager?.actions?.[key];
  if (!value || typeof value !== 'object') return { enabled: false, operation: '', class: '', reason: 'Backend did not expose this action' };
  return { enabled: value.enabled === true, operation: String(value.operation || ''), class: String(value.class || ''), reason: String(value.reason || '') };
}

export function runtimeCandidateAction(candidate, key = 'activate') {
  const value = candidate?.actions?.[key];
  if (!value || typeof value !== 'object') return { enabled: false, operation: '', class: '', reason: 'Backend did not expose this candidate action' };
  return { enabled: value.enabled === true, operation: String(value.operation || ''), class: String(value.class || ''), reason: String(value.reason || '') };
}

export function runtimeIssueCanRetry(issue) {
  return issue?.retryable === true && Array.isArray(issue?.recovery_actions) && issue.recovery_actions.length > 0;
}

export function runtimeSourceChoice(manager, sourceId) {
  const id = String(sourceId || '');
  const choice = (Array.isArray(manager?.source_choices) ? manager.source_choices : []).find((item) => item?.id === id);
  if (!choice || !Array.isArray(choice.channels)) return { id, default_channel: '', channels: [] };
  return {
    id,
    kind: String(choice.kind || ''),
    default_channel: String(choice.default_channel || ''),
    channels: choice.channels.map((entry) => ({ channel: String(entry?.channel || ''), requires_ref: entry?.requires_ref === true })).filter((entry) => entry.channel),
  };
}

export function runtimeSourceChannelOptions(manager, sourceId) {
  return runtimeSourceChoice(manager, sourceId).channels;
}

export function runtimeUpdatePolicyInput(current = {}, values = {}) {
  return {
    ...current,
    source_id: String(values.source_id ?? current.source_id ?? ''),
    activation_mode: String(values.activation_mode ?? current.activation_mode ?? ''),
    check_automatically: Boolean(values.check_automatically),
    stage_automatically: Boolean(values.stage_automatically),
    restart_active_mounts_automatically: Boolean(values.restart_active_mounts_automatically),
  };
}
