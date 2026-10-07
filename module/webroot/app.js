import { capabilities, cancel, preview, query, reconcile, run, selectTransport, startOperation, transportName, validateCapabilities } from './bridge.js';
import { DEFAULT_MOUNT, candidate, consequenceLabel, isDestructivePreview, localMountIssues, mountRows, operationSummary, parseRemoteEndpoint, previewIsUsable, profilePresentation, removeMount, replaceMount, suggestMountDefaults } from './model.js';

const byId = (id) => document.getElementById(id);
const compatibility = byId('compatibility');
const badge = byId('transportBadge');
const state = {
  caps: null, snapshot: null, activeView: 'home', timer: null, editorOriginal: '', editorDelete: false,
  preview: null, previewCandidates: null, rollbackPreview: null, refreshing: false,
  editorDirty: false, editorInitial: '', editorExisting: false, editorValidationTimer: null, editorValidationSeq: 0, editorIssues: [],
  editorRemotes: [], editorProfiles: [], editorRecommendation: null, editorProviderStatus: null, editorRemotePath: '', editorManualRemote: false, editorRemoteReachable: null, editorNameTouched: false, editorMountpointTouched: false, editorProfileTouched: false, editorBackendValidated: false, editorTrigger: null, editorSession: 0, previewTimer: null, operationPollToken: 0,
  jobSnapshot: null, jobOriginal: '', jobPreview: null, jobCandidates: null, settingsSnapshot: null, settingsPreview: null, logCursor: 0, logRecords: [], remote: '', remotePath: '',
  runtimeResolution: null, migrationPreview: null, migrationRequest: null, migrationFinalizePreview: null, migrationSelectedMounts: [], migrationSelectedJobs: [],
};

function text(id, value) { const node = byId(id); if (node) node.textContent = String(value ?? '—'); }
function clear(node) { while (node?.firstChild) node.removeChild(node.firstChild); }
function backendError(envelope) {
  const error = new Error(envelope?.response?.error?.message || envelope?.response?.error?.code || 'Backend request failed');
  error.code = envelope?.response?.error?.code || 'backend_error';
  error.detail = envelope?.response?.error?.detail || '';
  error.category = envelope?.response?.error?.category || '';
  error.stage = envelope?.response?.error?.stage || '';
  error.exitCode = Number(envelope?.response?.error?.exit_code || 0);
  error.severity = envelope?.response?.error?.severity || 'error';
  error.retryable = Boolean(envelope?.response?.error?.retryable);
  error.issues = Array.isArray(envelope?.response?.error?.issues) ? envelope.response.error.issues : [];
  return error;
}
function result(envelope) {
  if (!envelope?.response) throw new Error('Backend envelope is missing a response');
  if (envelope.response.ok !== true) throw backendError(envelope);
  return envelope.response.result ?? {};
}
function countMounts(value) { return Array.isArray(value?.mounts) ? value.mounts.length : 0; }
function showNotice(node, message, kind = '') {
  if (!node) return;
  node.hidden = false; node.className = `notice compact ${kind}`.trim(); node.textContent = String(message);
}
function hideNotice(node) { if (node) { node.hidden = true; node.textContent = ''; } }
function showMountOutcome(message, kind = '', actions = []) {
  const node = byId('mountsNotice'); if (!node) return; clear(node); node.hidden = false; node.className = `notice compact ${kind}`.trim();
  node.append(paragraph(message));
  if (actions.length) { const row = document.createElement('div'); row.className = 'button-row notice-actions'; for (const action of actions) row.append(button(action.label, action.run, action.className || '')); node.append(row); }
}
function backendErrorText(error) {
  const code = error?.code && error.code !== 'backend_error' ? error.code : '';
  const message = String(error?.message || error || 'Operation failed');
  const detail = String(error?.detail || '').trim();
  const stage = String(error?.stage || '').trim();
  const exit = Number(error?.exitCode || 0);
  const context = [stage ? `stage ${stage}` : '', exit ? `exit ${exit}` : ''].filter(Boolean).join(' · ');
  return `${code ? `${code}: ` : ''}${message}${context ? ` (${context})` : ''}${detail && !message.includes(detail) ? ` · ${detail}` : ''}`;
}
function mountConfigByName(name) { return (state.snapshot?.mounts || []).find((item) => item.name === name); }
function openMountForEdit(name) { const cfg = mountConfigByName(name); if (cfg) openEditor(cfg); }
function showLifecycleFailure(name, action, error) {
  const actions = [
    { label: 'Edit mount', run: () => openMountForEdit(name), className: error?.retryable ? '' : 'primary' },
    { label: 'View logs', run: () => void setView('logs') },
    { label: 'View operations', run: () => void setView('runtime') },
  ];
  if (error?.retryable) actions.unshift({ label: 'Retry now', run: () => void lifecycleAction(action === 'stop' ? 'start' : action, name), className: 'primary' });
  showMountOutcome(`${name}: ${backendErrorText(error)}`, 'error', actions);
}

function formatTime(value) { if (!value) return '—'; try { return new Date(Number(value)).toLocaleString(); } catch (_) { return '—'; } }
function label(textValue, className = 'state-pill') { const node = document.createElement('span'); node.className = className; node.textContent = String(textValue); return node; }
function paragraph(value, className = '') { const node = document.createElement('p'); if (className) node.className = className; node.textContent = String(value ?? ''); return node; }
function button(title, handler, className = '') { const node = document.createElement('button'); node.type = 'button'; node.textContent = title; if (className) node.className = className; node.addEventListener('click', handler); return node; }

function focusableNodes(root) {
  return [...(root?.querySelectorAll('button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), summary, [tabindex]:not([tabindex="-1"])') || [])].filter((node) => !node.hidden && node.offsetParent !== null);
}
function trapModalTab(event, root) {
  if (event.key !== 'Tab' || !root || root.hidden) return false;
  const nodes = focusableNodes(root); if (!nodes.length) return false;
  const first = nodes[0], last = nodes.at(-1);
  if (!root.contains(document.activeElement)) { event.preventDefault(); (event.shiftKey ? last : first).focus(); return true; }
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); return true; }
  if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); return true; }
  return false;
}

function requiredOperations(caps) {
  const names = new Set(caps.operations.map((item) => item?.name));
  const required = [
    'runtime.status','migration.status','migration.inspect','migration.preview','migration.apply','migration.finalize.preview','migration.finalize','migration.rollback','migration.recover','provider.status','platform.status','config.snapshot','config.validate','config.preview','config.apply','config.rollback.preview','config.rollback',
    'mount.status','mount.health','mount.start.preview','mount.start','mount.stop','mount.restart','mount.reconcile',
    'operation.status','operation.list','operation.cancel','namespace.inspect','namespace.preview','namespace.apply','namespace.rollback.preview','namespace.rollback',
    'policy.status','vfs.profiles','cache.status','cache.clear.preview','cache.clear','cache.forget.preview','cache.forget','rc.metrics',
    'jobs.snapshot','jobs.status','jobs.preview','jobs.apply','job.run.preview','job.run',
    'diagnostics.logs','doctor.report','doctor.bundle','doctor.bundle.read','provider.remotes','provider.browse',
    'ui.settings','ui.settings.preview','ui.settings.apply',
  ];
  for (const name of required) if (!names.has(name)) throw new Error(`Backend is missing required operation: ${name}`);
}

async function ensureConnected() {
  if (state.caps) return state.caps;
  const transport = await selectTransport();
  badge.textContent = transport === 'embedded' ? 'Embedded manager bridge' : 'Standalone loopback';
  const caps = validateCapabilities(await capabilities());
  requiredOperations(caps); state.caps = caps;
  compatibility.hidden = true;
  compatibility.className = 'notice ok';
  compatibility.textContent = `Compatible backend connected through ${transportName()}. Runtime truth remains backend-owned.`;
  return caps;
}

async function loadHome() {
  const caps = await ensureConnected();
  const [runtimeEnvelope, providerEnvelope, mountsEnvelope, platformEnvelope, operationsEnvelope] = await Promise.all([
    query('runtime.status'), query('provider.status'), query('mount.status'), query('platform.status'), query('operation.list', { limit: 30 }),
  ]);
  const runtime = result(runtimeEnvelope), provider = result(providerEnvelope), mounts = result(mountsEnvelope), platform = result(platformEnvelope), operations = result(operationsEnvelope);
  text('backendValue', caps.server?.version || caps.server?.name || 'Connected');
  text('backendDetail', `Schema ${caps.schema_version} · protocol ${caps.protocol.min}-${caps.protocol.max}`);
  text('providerValue', runtime?.operational === true ? 'Ready' : (runtime?.mode || 'Observed'));
  text('providerDetail', `${runtime?.mode || 'unknown'} · ${runtime?.source || 'runtime authority'}${provider?.rclone_version ? ` · ${provider.rclone_version}` : ''}`);
  text('mountValue', countMounts(mounts));
  const running = (mounts.mounts || []).filter((item) => item.state === 'running').length;
  text('mountDetail', `${running} running · ${countMounts(mounts)} configured`);
  const records = operations.operations || [];
  text('operationValue', records.filter((item) => item.state === 'RUNNING').length);
  text('operationDetail', `${records.length} recent journal entr${records.length === 1 ? 'y' : 'ies'}`);
  const manager = platform?.root_manager || platform?.manager || {};
  compatibility.title = manager?.name || manager?.kind || 'Root manager detected';
}

function healthClass(value) {
  if (value === 'RUNNING' || value === 'stopped' || value === 'running') return 'state-pill good';
  if (value === 'STOPPED' || value === 'RETRYING' || value === 'REMOTE_OFFLINE') return 'state-pill warn';
  if (value === 'DEGRADED' || value === 'AUTH_ERROR' || value === 'FUSE_ERROR' || value === 'MOUNT_STALE') return 'state-pill bad';
  return 'state-pill';
}

function renderMounts(snapshot, statuses, health) {
  const root = byId('mountCards'); clear(root);
  const rows = mountRows(snapshot, statuses, health);
  if (!rows.length) { root.append(paragraph('No mounts are configured. Add one to begin.', 'notice')); return; }
  for (const row of rows) {
    const card = document.createElement('article'); card.className = 'mount-card';
    const head = document.createElement('div'); head.className = 'mount-head';
    const titles = document.createElement('div'); const title = document.createElement('h3'); title.textContent = row.config.name; titles.append(title, paragraph(`${row.config.remote} → ${row.config.mountpoint}`, 'muted code'));
    const stateValue = row.health.state || row.status.state || 'unknown'; head.append(titles, label(stateValue, healthClass(stateValue))); card.append(head);
    const meta = document.createElement('div'); meta.className = 'mount-meta';
    const values = [
      ['Desired', row.health.desired || row.status.desired || '—'], ['Enabled', row.config.enabled ? 'yes' : 'no'],
      ['VFS', row.config.vfs_profile || 'custom'], ['Network', row.config.network_mode || 'offline-allowed'],
      ['Health reason', row.health.reason || '—'], ['PID', row.status.pid || row.health.pid || '—'],
    ];
    for (const [name, value] of values) { const cell = document.createElement('div'); const strong = document.createElement('strong'); strong.textContent = name; cell.append(strong, document.createTextNode(String(value))); meta.append(cell); }
    card.append(meta);
    const actions = document.createElement('div'); actions.className = 'mount-actions';
    const running = Boolean(row.health.process_alive || row.status.state === 'running');
    const desiredRunning = (row.health.desired || row.status.desired) === 'running';
    const terminalFailure = Boolean(row.health.failure_code && row.health.retryable === false);
    const retryableFailure = Boolean(row.health.failure_code && row.health.retryable === true);
    const start = button(retryableFailure ? 'Retry start' : terminalFailure ? 'Start blocked' : 'Start', () => void lifecycleAction('start', row.config.name));
    start.disabled = running || terminalFailure;
    const stop = button('Stop', () => void lifecycleAction('stop', row.config.name));
    stop.disabled = !running && !desiredRunning;
    const restart = button('Restart', () => void lifecycleAction('restart', row.config.name));
    restart.disabled = !running || terminalFailure;
    if (retryableFailure) start.title = 'Retryable runtime failure';
    if (terminalFailure) start.title = 'Correct the reported cause before starting again';
    actions.append(start, stop, restart, button('Edit', () => openEditor(row.config), terminalFailure ? 'primary' : ''));
    if (row.health.failure_code) {
      actions.append(button('View logs', () => void setView('logs')), button('View operations', () => void viewOperations()));
    }
    card.append(actions); root.append(card);
  }
}

async function loadMounts() {
  const [snapshotEnvelope, statusesEnvelope, healthEnvelope] = await Promise.all([
    query('config.snapshot'), query('mount.status'), query('mount.health'),
  ]);
  const snapshot = result(snapshotEnvelope); state.snapshot = snapshot;
  renderMounts(snapshot, result(statusesEnvelope).mounts || [], result(healthEnvelope).health || []);
}

async function lifecycleAction(action, name) {
  const notice = byId('mountsNotice'); hideNotice(notice);
  try {
    if (action === 'start') result(await preview('mount.start.preview', { name }));
    const handle = startOperation(`mount.${action}`, 'run', { name });
    showNotice(notice, `${action} operation ${handle.requestId} started. Progress is recorded under Operations.`);
    const envelope = await handle.completion; result(envelope);
    showNotice(notice, `${name}: ${action} completed.`, 'ok');
    await loadMounts();
  } catch (error) { showLifecycleFailure(name, action, error); }
}

async function reconcileAll() {
  const notice = byId('mountsNotice');
  try {
    const handle = startOperation('mount.reconcile', 'reconcile', {});
    showNotice(notice, `Reconcile ${handle.requestId} started.`);
    result(await handle.completion); showNotice(notice, 'Reconcile completed.', 'ok'); await loadMounts();
  } catch (error) { showNotice(notice, String(error?.message || error), 'error'); }
}

function renderOperations(records) {
  const root = byId('operationCards'); clear(root);
  if (!records.length) { root.append(paragraph('No operation journal entries yet.', 'notice')); return; }
  for (const record of records) {
    const model = operationSummary(record); const card = document.createElement('article'); card.className = 'operation-card';
    const head = document.createElement('div'); head.className = 'operation-head';
    const titles = document.createElement('div'); const title = document.createElement('h3'); title.textContent = model.operation; titles.append(title, paragraph(model.requestId, 'muted code'));
    head.append(titles, label(model.state, model.state === 'SUCCEEDED' ? 'state-pill good' : model.state === 'RUNNING' ? 'state-pill warn' : model.state === 'FAILED' ? 'state-pill bad' : 'state-pill'));
    card.append(head, paragraph(`Updated ${formatTime(model.updatedUnixMS)}${model.message ? ` · ${model.message}` : ''}`, 'muted'));
    if (model.events.length) {
      const list = document.createElement('ol'); list.className = 'operation-events';
      for (const event of model.events.slice(-6)) { const item = document.createElement('li'); item.textContent = `${event.event || 'event'}${event.message ? ` — ${event.message}` : ''}`; list.append(item); }
      card.append(list);
    }
    if (model.state === 'RUNNING' && model.cancellable) card.append(button('Cancel safely', () => void cancelOperation(model.requestId), 'danger'));
    root.append(card);
  }
}

async function loadOperations() {
  const records = result(await query('operation.list', { limit: 50 })).operations || [];
  renderOperations(records);
}

async function cancelOperation(requestId) {
  try { result(await cancel('operation.cancel', { request_id: requestId })); await loadOperations(); }
  catch (error) { compatibility.className = 'notice error'; compatibility.textContent = `Cancel failed: ${String(error?.message || error)}`; }
}

const fields = ['name','remote','mountpoint','vfs_profile','vfs_cache_mode','vfs_cache_max_size','vfs_cache_max_age','dir_cache_time','poll_interval','log_level','network_mode','min_battery','min_free_cache_space','boot_settle','network_settle','cache_high_water','cache_low_water'];
const checks = ['enabled','allow_other','read_only','probe_remote','charging_only'];
const issueCategoryLabels = { input:'Input', remote:'Remote', destination:'Destination', vfs:'Cache & performance', policy:'Behaviour', provider:'Provider', conflict:'Conflict', runtime:'Runtime', security:'Security', advanced:'Advanced', internal:'Internal' };

function clearPreviewTimer() { if (state.previewTimer) clearInterval(state.previewTimer); state.previewTimer = null; }
function serializeEditor() { try { return JSON.stringify(formValue()); } catch (_) { return ''; } }
function markEditorDirty() { state.editorDirty = serializeEditor() !== state.editorInitial; }
function issueFieldNode(field) { return document.querySelector(`#mountForm label[data-field="${field}"]`); }
function clearFieldIssues() {
  for (const node of document.querySelectorAll('#mountForm label.field-invalid')) node.classList.remove('field-invalid');
  for (const node of document.querySelectorAll('#mountForm .field-feedback')) node.remove();
}
function renderEditorIssues(issues = []) {
  state.editorIssues = Array.isArray(issues) ? issues : [];
  clearFieldIssues();
  const root = byId('editorIssueSummary'); clear(root);
  const visible = state.editorIssues.filter((item) => item?.message);
  const currentMount = byId('field-name')?.value?.trim() || state.editorOriginal || '';
  for (const item of visible) {
    if (!item.field || (item.mount && currentMount && item.mount !== currentMount)) continue;
    const labelNode = issueFieldNode(item.field); if (!labelNode) continue;
    labelNode.classList.toggle('field-invalid', item.severity !== 'warning');
    const note = document.createElement('span'); note.className = 'field-feedback';
    note.textContent = `${item.message}${item.suggestion ? ` · ${item.suggestion}` : ''}`; labelNode.append(note);
  }
  if (!visible.length) { root.hidden = true; renderDestinationStatus([]); return; }
  root.hidden = false;
  const errors = visible.filter((item) => item.severity !== 'warning').length;
  const warnings = visible.length - errors;
  const title = document.createElement('h3'); title.textContent = `${errors ? `${errors} thing${errors === 1 ? '' : 's'} need attention` : 'Configuration looks usable'}${warnings ? ` · ${warnings} warning${warnings === 1 ? '' : 's'}` : ''}`; root.append(title);
  const grouped = new Map();
  for (const item of visible) { const key = item.category || 'input'; if (!grouped.has(key)) grouped.set(key, []); grouped.get(key).push(item); }
  for (const [category, items] of grouped) {
    const block = document.createElement('div'); block.className = 'issue-category';
    const heading = document.createElement('strong'); heading.textContent = issueCategoryLabels[category] || category; block.append(heading);
    const list = document.createElement('ul'); list.className = 'issue-list';
    for (const item of items) {
      const currentMount = byId('field-name')?.value?.trim() || state.editorOriginal || '';
      const focusable = item.field && (!item.mount || !currentMount || item.mount === currentMount);
      const li = document.createElement('li'); const message = document.createElement(focusable ? 'button' : 'span');
      if (focusable) { message.type = 'button'; message.className = 'issue-action'; message.addEventListener('click', () => focusIssueField(item.field)); }
      message.textContent = `${item.mount && item.mount !== currentMount ? `“${item.mount}”: ` : ''}${item.message}`; li.append(message);
      if (item.suggestion) li.append(document.createTextNode(` — ${item.suggestion}`));
      list.append(li);
    }
    block.append(list); root.append(block);
  }
  renderDestinationStatus(visible);
}
function focusIssueField(field) { const node = byId(`field-${field}`); if (node) { const details = node.closest('details'); if (details) details.open = true; node.focus(); node.scrollIntoView({ block:'center', behavior:'smooth' }); } }
function renderDestinationStatus(issues = state.editorIssues) {
  const root = byId('destinationStatus'); clear(root);
  const currentMount = byId('field-name')?.value?.trim() || state.editorOriginal || '';
  const own = issues.filter((item) => !item.mount || !currentMount || item.mount === currentMount);
  const cfg = formValue();
  const nameIssue = own.find((item) => item.field === 'name' && item.severity !== 'warning');
  const pathIssue = own.find((item) => item.field === 'mountpoint' && item.severity !== 'warning');
  if (cfg.name) root.append(label(nameIssue ? `✕ ${nameIssue.message}` : '✓ Name ready', nameIssue ? 'validation-chip bad' : 'validation-chip good'));
  const absolute = cfg.mountpoint.startsWith('/');
  if (cfg.mountpoint) root.append(label(absolute ? '✓ Absolute path' : '✕ Absolute path required', absolute ? 'validation-chip good' : 'validation-chip bad'));
  if (state.editorBackendValidated && absolute) {
    const protectedIssue = own.find((item) => ['mountpoint_protected','mountpoint_root_forbidden'].includes(item.code));
    const overlapIssue = own.find((item) => item.code === 'mountpoint_overlap');
    root.append(label(protectedIssue ? '✕ Protected location' : '✓ Outside Nexus/provider state', protectedIssue ? 'validation-chip bad' : 'validation-chip good'));
    root.append(label(overlapIssue ? `✕ Overlaps ${overlapIssue.related_mount || 'another mount'}` : '✓ No mount overlap', overlapIssue ? 'validation-chip bad' : 'validation-chip good'));
  } else if (absolute && !pathIssue) {
    root.append(label('… Checking backend location rules','validation-chip'));
  }
}
function invalidatePreview({ validate = true } = {}) {
  state.preview = null; state.previewCandidates = null; state.editorBackendValidated = false; clearPreviewTimer(); byId('previewPanel').hidden = true; byId('applyMountButton').disabled = true; hideNotice(byId('editorError')); byId('operationProgress').hidden = true;
  byId('applyMountButton').textContent = state.editorDelete ? 'Delete mount' : (state.editorExisting ? 'Apply changes' : 'Create mount');
  markEditorDirty();
  if (validate) scheduleEditorValidation();
}

function formValue() {
  const value = {};
  for (const key of fields) value[key] = byId(`field-${key}`).value;
  for (const key of checks) value[key] = byId(`field-${key}`).checked;
  value.require_network = value.network_mode !== 'offline-allowed';
  return candidate(value);
}

function fillForm(config, existing) {
  const clean = candidate(config);
  for (const key of fields) byId(`field-${key}`).value = clean[key] ?? '';
  for (const key of checks) byId(`field-${key}`).checked = Boolean(clean[key]);
  byId('field-name').readOnly = existing;
  byId('argsFileHint').hidden = !config?.has_args_file;
  const parsed = parseRemoteEndpoint(clean.remote); state.editorRemotePath = parsed.path; byId('mountRemotePath').value = parsed.path ? `/${parsed.path}` : '/';
  byId('mountRemoteEndpoint').textContent = clean.remote || '—';
}

function editorHasBlockingIssues(issues = state.editorIssues) { return issues.some((item) => item?.severity !== 'warning'); }
function localEditorIssues() { return localMountIssues(formValue(), state.snapshot?.mounts || [], state.editorOriginal); }
async function validateEditorNow() {
  state.editorBackendValidated = false;
  const local = localEditorIssues(); renderEditorIssues(local);
  if (editorHasBlockingIssues(local)) return { valid:false, issues:local };
  const seq = ++state.editorValidationSeq;
  try {
    const candidates = candidateRegistry(false); const value = result(await query('config.validate', { mounts:candidates }));
    if (seq !== state.editorValidationSeq || byId('editorBackdrop').hidden) return value;
    state.editorBackendValidated = true; renderEditorIssues(value.issues || []); return value;
  } catch (error) {
    if (seq !== state.editorValidationSeq || byId('editorBackdrop').hidden) return { valid:false, issues:[] };
    const issues = error?.issues?.length ? error.issues : [{ code:error?.code||'validation_failed', category:error?.category||'internal', severity:'error', message:String(error?.message||error), detail:error?.detail||'' }];
    renderEditorIssues(issues); return { valid:false, issues };
  }
}
function scheduleEditorValidation() {
  if (state.editorValidationTimer) clearTimeout(state.editorValidationTimer);
  state.editorValidationTimer = setTimeout(() => { state.editorValidationTimer = null; void validateEditorNow(); }, 350);
}

function endpointFor(remote, path='') { return remote ? `${remote}:${String(path||'').replace(/^\/+|\/+$/g,'')}` : ''; }
function updateRemoteEndpoint({ applyDefaults = true } = {}) {
  const selected = byId('mountRemoteSelect').value;
  if (!state.editorManualRemote && selected) byId('field-remote').value = endpointFor(selected, state.editorRemotePath);
  const endpoint = byId('field-remote').value.trim(); byId('mountRemoteEndpoint').textContent = endpoint || '—';
  const parsed = parseRemoteEndpoint(endpoint);
  const configured = Boolean(parsed.remote && state.editorRemotes.includes(parsed.remote));
  byId('remoteStatusPill').textContent = state.editorRemoteReachable === true ? 'Reachable' : configured ? 'Configured' : parsed.remote ? 'Manual' : 'Not selected';
  byId('remoteStatusPill').className = state.editorRemoteReachable === false ? 'state-pill bad' : (configured || state.editorRemoteReachable === true) ? 'state-pill good' : parsed.remote ? 'state-pill warn' : 'state-pill';
  if (applyDefaults && !state.editorExisting && endpoint) {
    const defaults = suggestMountDefaults(endpoint);
    if (!state.editorNameTouched || !byId('field-name').value) byId('field-name').value = defaults.name;
    if (!state.editorMountpointTouched || !byId('field-mountpoint').value) byId('field-mountpoint').value = defaults.mountpoint;
  }
  renderProviderReadiness();
  invalidatePreview();
}
function renderProviderReadiness() {
  const root = byId('providerReadiness'); if (!root) return; clear(root);
  const status = state.editorProviderStatus;
  if (!status) { root.append(label('… Checking provider','validation-chip')); return; }
  root.append(label(status.binary_ready ? '✓ rclone available' : '✕ rclone unavailable', status.binary_ready ? 'validation-chip good' : 'validation-chip bad'));
  root.append(label(status.fuse_helper_ready ? '✓ FUSE helper ready' : '✕ FUSE helper unavailable', status.fuse_helper_ready ? 'validation-chip good' : 'validation-chip bad'));
  root.append(label(status.config_ready ? '✓ rclone config ready' : '✕ rclone config missing', status.config_ready ? 'validation-chip good' : 'validation-chip bad'));
  const parsed = parseRemoteEndpoint(byId('field-remote')?.value || '');
  if (parsed.remote) {
    const configured = state.editorRemotes.includes(parsed.remote);
    root.append(label(configured ? `✓ ${parsed.remote}: configured` : `! ${parsed.remote}: manual/unlisted`, configured ? 'validation-chip good' : 'validation-chip'));
  }
  if (state.editorRemoteReachable === true) root.append(label('✓ Selected path reachable','validation-chip good'));
  if (state.editorRemoteReachable === false) root.append(label('✕ Selected path not reachable','validation-chip bad'));
}
function renderMountRemoteSelect() {
  const select = byId('mountRemoteSelect'); const current = parseRemoteEndpoint(byId('field-remote').value).remote; clear(select);
  const prompt = document.createElement('option'); prompt.value=''; prompt.textContent='Select a remote…'; select.append(prompt);
  for (const name of state.editorRemotes) { const option=document.createElement('option'); option.value=name; option.textContent=`${name}:`; select.append(option); }
  if (current && state.editorRemotes.includes(current)) select.value=current;
}
async function loadMountEditorData(existing, session = state.editorSession) {
  const settled = await Promise.allSettled([query('provider.remotes'), query('vfs.profiles'), query('provider.status')]);
  if (session !== state.editorSession || byId('editorBackdrop').hidden) return;
  const helperErrors=[];
  try { state.editorRemotes = settled[0].status === 'fulfilled' ? (result(settled[0].value).remotes || []) : []; if(settled[0].status !== 'fulfilled')throw settled[0].reason; } catch(error){state.editorRemotes=[];helperErrors.push(`remote list: ${String(error?.message||error)}`);}
  try { const profileData=settled[1].status === 'fulfilled' ? result(settled[1].value) : (()=>{throw settled[1].reason;})(); state.editorProfiles=profileData.profiles||[];state.editorRecommendation=profileData.recommendation||null; } catch(error){state.editorProfiles=[];state.editorRecommendation=null;helperErrors.push(`profiles: ${String(error?.message||error)}`);}
  try { state.editorProviderStatus=settled[2].status === 'fulfilled' ? result(settled[2].value) : (()=>{throw settled[2].reason;})(); } catch(error){state.editorProviderStatus=null;helperErrors.push(`provider status: ${String(error?.message||error)}`);}
  renderMountRemoteSelect(); renderProfileCards(); renderProviderReadiness();
  const currentRemote = parseRemoteEndpoint(byId('field-remote').value).remote;
  if (existing && currentRemote && !state.editorRemotes.includes(currentRemote)) setManualRemote(true);
  const current = byId('field-vfs_profile').value;
  if (!existing && !state.editorProfileTouched && (current === 'custom' || !current) && state.editorRecommendation?.profile) { byId('field-vfs_profile').value = state.editorRecommendation.profile; }
  renderProfileCards(); updateProfilePresentation();
  if(helperErrors.length)showNotice(byId('editorError'),`Some setup helpers are unavailable (${helperErrors.join(' · ')}). Available fields remain editable and backend review still decides whether the mount can be applied.`,'error');
}
function renderProfileCards() {
  const root = byId('profileCards'); clear(root); const selected = byId('field-vfs_profile').value || 'custom';
  const profiles = state.editorProfiles.length ? state.editorProfiles : [
    {name:'balanced',description:'General-purpose full VFS caching.'},{name:'streaming',description:'Larger cache for media.'},{name:'offline',description:'Persistent cache for intermittent connectivity.'},{name:'minimal',description:'Small cache footprint.'},{name:'custom',description:'Use explicit VFS values.'},
  ];
  for (const item of profiles) {
    const model = profilePresentation(item.name, profiles, state.editorRecommendation); const card=document.createElement('label'); card.className=`profile-card${selected===item.name?' selected':''}${model.recommended?' recommended':''}`;
    const radio=document.createElement('input'); radio.type='radio'; radio.name='profile-card'; radio.value=item.name; radio.checked=selected===item.name;
    radio.addEventListener('change',()=>{ if(!radio.checked)return; state.editorProfileTouched=true; byId('field-vfs_profile').value=item.name; renderProfileCards(); updateProfilePresentation(); invalidatePreview(); });
    const strong=document.createElement('strong'); strong.textContent=item.name; const desc=paragraph(model.description,''); card.append(radio,strong,desc);
    const opts=model.options||{}; const meta=document.createElement('span'); meta.className='profile-meta'; meta.textContent=[opts.vfs_cache_mode,opts.vfs_cache_max_size].filter(Boolean).join(' · ') || 'Explicit settings'; card.append(meta); root.append(card);
  }
  const rec=state.editorRecommendation; const recNode=byId('profileRecommendation');
  if (rec?.profile) { const resources=[rec.memory_bytes?`${bytes(rec.memory_bytes)} RAM`:'',rec.cache_free_bytes?`${bytes(rec.cache_free_bytes)} cache free`:''].filter(Boolean).join(' · '); recNode.hidden=false; recNode.textContent=`Recommended for this device: ${rec.profile} · ${rec.reason}. ${resources ? `${resources} · ` : ''}cache target ${rec.cache_max_size || 'automatic'}.`; } else recNode.hidden=true;
}
function updateProfilePresentation() {
  const profile=byId('field-vfs_profile').value||'custom'; const model=profilePresentation(profile,state.editorProfiles,state.editorRecommendation); const root=byId('profileEffective'); clear(root); const custom=profile==='custom';
  byId('customVfsDetails').open=custom; byId('customVfsDetails').classList.toggle('profile-managed',!custom);
  for (const node of document.querySelectorAll('#customVfsDetails input, #customVfsDetails select')) node.disabled=!custom;
  if (custom) { root.append(label('Custom values','state-pill')); return; }
  const o=model.options||{};
  const profileFields={vfs_cache_mode:o.vfs_cache_mode||'full',vfs_cache_max_size:o.vfs_cache_max_size||'',vfs_cache_max_age:o.vfs_cache_max_age||'',dir_cache_time:o.dir_cache_time||'',poll_interval:o.poll_interval||''};
  for(const [field,value] of Object.entries(profileFields))byId(`field-${field}`).value=value;
  root.append(label('Profile-managed values','state-pill good'));
  for (const value of [o.vfs_cache_mode && `Cache ${o.vfs_cache_mode}`, o.vfs_cache_max_size && `Max ${o.vfs_cache_max_size}`, o.vfs_cache_max_age && `Age ${o.vfs_cache_max_age}`, o.dir_cache_time && `Dir cache ${o.dir_cache_time}`].filter(Boolean)) root.append(label(value,'state-pill'));
}
function setManualRemote(enabled) {
  state.editorManualRemote=Boolean(enabled); byId('manualRemoteWrap').hidden=!state.editorManualRemote; byId('manualRemoteButton').textContent=state.editorManualRemote?'Use remote browser':'Enter manually';
  if (state.editorManualRemote) byId('field-remote').focus(); else updateRemoteEndpoint({applyDefaults:false});
}
async function browseMountRemote(remote=byId('mountRemoteSelect').value,path=state.editorRemotePath) {
  if (!remote) { showNotice(byId('remoteTestResult'),'Choose a remote first.','error'); return; }
  const panel=byId('mountRemoteBrowser'), root=byId('mountRemoteEntries'); panel.hidden=false; clear(root); byId('mountRemoteBreadcrumb').textContent=`${remote}:${path||''}`; byId('mountRemoteParentButton').disabled=!path;
  try {
    const value=result(await query('provider.browse',{remote,path,limit:180})); state.editorRemotePath=value.path||''; byId('mountRemotePath').value=state.editorRemotePath?`/${state.editorRemotePath}`:'/'; byId('mountRemoteBreadcrumb').textContent=`${remote}:${state.editorRemotePath}`;
    const dirs=(value.entries||[]).filter((entry)=>entry.is_dir); if(!dirs.length) root.append(paragraph('No subfolders here. You can use the current folder.','muted'));
    for(const entry of dirs){const row=document.createElement('div');row.className='remote-folder';const main=document.createElement('div');main.className='folder-main';const icon=document.createElement('span');icon.textContent='📁';const name=document.createElement('span');name.className='folder-name';name.textContent=entry.name;main.append(icon,name);row.append(main,button('Open',()=>void browseMountRemote(remote,entry.path)));root.append(row);}
    if(value.truncated) root.append(paragraph('Folder list is truncated. Open a narrower folder to continue.','muted'));
  } catch(error) { const issues=error?.issues?.length?error.issues:[{category:'remote',field:'remote',severity:'error',message:String(error?.message||error)}]; renderEditorIssues([...localEditorIssues().filter((x)=>x.field!=='remote'),...issues]); root.append(paragraph(`Browse failed: ${String(error?.message||error)}`,'notice error')); }
}
function chooseCurrentRemoteFolder() { const remote=byId('mountRemoteSelect').value; if(!remote)return; state.editorManualRemote=false; byId('manualRemoteWrap').hidden=true; byId('field-remote').value=endpointFor(remote,state.editorRemotePath); byId('mountRemoteBrowser').hidden=true; updateRemoteEndpoint(); }
async function testMountRemote() {
  const node=byId('remoteTestResult'); const parsed=parseRemoteEndpoint(byId('field-remote').value); hideNotice(node);
  if(!parsed.remote){showNotice(node,'Choose a remote first.','error');return;}
  showNotice(node,`Testing ${parsed.remote}:…`);
  try { result(await query('provider.browse',{remote:parsed.remote,path:parsed.path,limit:1})); state.editorRemoteReachable=true; renderProviderReadiness(); updateRemoteEndpoint({applyDefaults:false}); showNotice(node,`Connection to ${parsed.remote}: is working${parsed.path?` and ${parsed.path} is reachable`:''}.`,'ok'); }
  catch(error){ state.editorRemoteReachable=false; renderProviderReadiness(); updateRemoteEndpoint({applyDefaults:false}); showNotice(node,`${String(error?.message||error)}${error?.detail?` · ${error.detail}`:''}`,'error'); if(error?.issues?.length)renderEditorIssues([...localEditorIssues().filter((x)=>x.field!=='remote'),...error.issues]); }
}

function safeMountLabel() {
  const endpoint = parseRemoteEndpoint(byId('field-remote').value); const raw = endpoint.path.split('/').filter(Boolean).at(-1) || byId('field-name').value || endpoint.remote || 'Mount';
  return String(raw).replace(/[\/:*?"<>|]+/g, ' ').replace(/\s+/g, ' ').trim() || 'Mount';
}
function applyMountpointSuggestion(template) {
  const name = byId('field-name').value.trim() || suggestMountDefaults(byId('field-remote').value).name || 'mount';
  byId('field-mountpoint').value = String(template).replace('{name}', name).replace('{label}', safeMountLabel()); state.editorMountpointTouched = true; invalidatePreview();
}
function updateNetworkPolicyHelp() {
  const mode = byId('field-network_mode').value; const copy = {
    any:'Start may use Wi-Fi or metered mobile data.', wifi:'Start only while Wi-Fi is available.', unmetered:'Start only on an unmetered network.', 'offline-allowed':'Cached content may remain usable while disconnected; remote access resumes when connectivity returns.',
  };
  byId('networkPolicyHelp').textContent = copy[mode] || 'Nexus enforces this policy before lifecycle start.';
}

function openEditor(config = null) {
  if (!state.snapshot) return;
  state.editorTrigger = document.activeElement; const session=++state.editorSession;
  const existing=Boolean(config?.name); state.editorExisting=existing; state.editorOriginal=existing?config.name:''; state.editorDelete=false; state.editorManualRemote=false; state.editorRemoteReachable=null; state.editorProviderStatus=null; state.editorNameTouched=false; state.editorMountpointTouched=false; state.editorProfileTouched=false; state.editorBackendValidated=false; state.editorIssues=[];
  fillForm(config||DEFAULT_MOUNT,existing); renderEditorIssues([]); hideNotice(byId('remoteTestResult')); byId('mountRemoteBrowser').hidden=true; byId('manualRemoteWrap').hidden=true; byId('mountAdvancedDetails').open=false; renderProviderReadiness();
  text('editorTitle',existing?`Edit ${config.name}`:'Add mount'); text('editorSubtitle',existing?'Review changes before Nexus updates the active configuration.':'Choose a source and destination. Most advanced settings can stay at their recommended defaults.');
  byId('deleteMountButton').hidden=!existing; byId('previewMountButton').textContent=existing?'Review changes':'Review mount'; byId('applyMountButton').textContent=existing?'Apply changes':'Create mount'; byId('editorBackdrop').hidden=false;
  state.editorInitial=serializeEditor(); state.editorDirty=false; invalidatePreview({validate:false}); state.editorInitial=serializeEditor(); state.editorDirty=false; updateRemoteEndpoint({applyDefaults:false}); state.editorInitial=serializeEditor(); state.editorDirty=false;
  updateNetworkPolicyHelp();
  void loadMountEditorData(existing,session).then(()=>{if(session!==state.editorSession||byId('editorBackdrop').hidden)return;if(!state.editorDirty)state.editorInitial=serializeEditor();void validateEditorNow();});
  byId('mountRemoteSelect').focus();
}
function forceCloseEditor() { const returnFocus=state.editorTrigger; state.editorSession+=1; byId('editorBackdrop').hidden=true; byId('discardBackdrop').hidden=true; state.editorOriginal='';state.editorDelete=false;state.editorExisting=false;state.editorDirty=false;state.editorIssues=[];state.editorProviderStatus=null;state.editorRemoteReachable=null;state.editorTrigger=null; if(state.editorValidationTimer)clearTimeout(state.editorValidationTimer);state.editorValidationTimer=null;state.editorValidationSeq+=1;clearPreviewTimer();state.operationPollToken+=1;invalidatePreview({validate:false});renderEditorIssues([]);if(returnFocus?.focus)queueMicrotask(()=>returnFocus.focus()); }
function closeEditor() { markEditorDirty(); if(state.editorDirty && !byId('editorBackdrop').hidden){byId('discardBackdrop').hidden=false;queueMicrotask(()=>byId('keepEditingButton')?.focus());return;} forceCloseEditor(); }

function candidateRegistry(deleteMode = false) {
  if (!state.snapshot) throw new Error('Configuration snapshot is unavailable');
  if (deleteMode) return removeMount(state.snapshot.mounts, state.editorOriginal);
  return replaceMount(state.snapshot.mounts, state.editorOriginal, formValue());
}

function renderReviewCell(root, name, value) { const cell=document.createElement('div');cell.className='review-cell';const strong=document.createElement('strong');strong.textContent=name;const span=document.createElement('span');span.textContent=String(value||'—');cell.append(strong,span);root.append(cell); }
function renderPreviewCountdown() {
  const node=byId('previewCountdown'), help=byId('previewExpiryHelp'); if(!state.preview?.preview_expires_unix_ms){node.textContent='—';if(help)help.textContent='This approval is temporary and does not change configuration until you apply it.';return;}
  const ms=Number(state.preview.preview_expires_unix_ms)-Date.now();
  if(ms<=0){node.textContent='Expired';node.className='state-pill bad';if(help)help.textContent='Your safety approval expired. Nothing was changed; review the current configuration again.';byId('applyMountButton').disabled=true;byId('previewMountButton').textContent='Review again';return;}
  const total=Math.ceil(ms/1000),min=Math.floor(total/60),sec=String(total%60).padStart(2,'0');node.textContent=`Valid ${min}:${sec}`;node.className=total<=20?'state-pill warn':'state-pill good';if(help)help.textContent=total<=20?'This approval expires soon. Apply now or refresh the review.':'This approval is temporary and does not change configuration until you apply it.';
}
function renderPreview(value) {
  const root=byId('previewChanges');clear(root);const review=byId('reviewSummary');clear(review);const safety=byId('reviewSafety');const cfg=formValue();const profile=profilePresentation(cfg.vfs_profile,state.editorProfiles,state.editorRecommendation);const parsed=parseRemoteEndpoint(cfg.remote);
  byId('reviewTitle').textContent=state.editorDelete?`Delete mount “${state.editorOriginal}”?`:(state.editorExisting?`Ready to update “${cfg.name}”`:`Ready to create “${cfg.name}”`);
  if(state.editorDelete){renderReviewCell(review,'Configuration',state.editorOriginal);renderReviewCell(review,'Running mount','Will be stopped if required');renderReviewCell(review,'Cache','Preserved unless a separate cache action is approved');}
  else{renderReviewCell(review,'Source',`${parsed.remote||'—'}:${parsed.path||''}`);renderReviewCell(review,'Mount location',cfg.mountpoint);renderReviewCell(review,'Performance',`${cfg.vfs_profile}${profile.recommended?' · recommended':''}`);renderReviewCell(review,'Auto start',cfg.enabled?'Enabled':'Disabled');renderReviewCell(review,'Android visibility',cfg.allow_other?'Requested':'Owner only');renderReviewCell(review,'Access',cfg.read_only?'Read only':'Read/write');renderReviewCell(review,'Remote check',cfg.probe_remote?'Before start':'Not required');renderReviewCell(review,'Policy',`${cfg.network_mode}${cfg.charging_only?' · charging only':''}${cfg.min_battery?` · battery ≥ ${cfg.min_battery}%`:''}`);}
  const destructive=isDestructivePreview(value); safety.className=`notice compact ${destructive?'error':'ok'}`; safety.textContent=state.editorDelete?'Deletion removes this mount configuration and stops its lifecycle if needed. Nexus cache files are preserved by this action.':destructive?'This review includes a destructive or mount-location-sensitive change. Check the consequences below before applying.':'No destructive actions detected in this configuration review.';
  for(const change of value.changes||[]){const card=document.createElement('div');card.className='change-card';const title=document.createElement('strong');title.textContent=state.editorDelete?'What will happen':`${change.name}: ${change.kind}`;card.append(title);if(change.consequences?.length)card.append(paragraph(change.consequences.map((x)=>`✓ ${consequenceLabel(x)}`).join(' · '),'muted'));if(change.reasons?.length&&!state.editorDelete)card.append(paragraph(`Changed: ${change.reasons.join(', ')}`,'muted'));root.append(card);}
  if(!(value.changes||[]).length)root.append(paragraph('No configuration changes.','muted'));
  text('previewMeta',`Revision ${value.current_revision} · digest ${String(value.candidate_digest||'').slice(0,16)}… · proof expires ${formatTime(value.preview_expires_unix_ms)}${destructive?' · destructive change':''}`);byId('previewPanel').hidden=false;clearPreviewTimer();renderPreviewCountdown();state.previewTimer=setInterval(renderPreviewCountdown,1000);
  byId('applyMountButton').textContent=state.editorDelete?'Delete mount':(state.editorExisting?'Apply changes':'Create mount');
}

async function previewEditor(deleteMode = false) {
  const errorNode=byId('editorError');hideNotice(errorNode);state.editorDelete=deleteMode;
  if(!deleteMode){const form=byId('mountForm');if(!form.checkValidity()){form.reportValidity();return;}const validation=await validateEditorNow();if(!validation?.valid||editorHasBlockingIssues(validation.issues||state.editorIssues)){showNotice(errorNode,'Fix the highlighted fields before reviewing this mount.','error');return;}}
  try{const candidates=candidateRegistry(deleteMode);const value=result(await preview('config.preview',{mounts:candidates}));state.preview=value;state.previewCandidates=candidates;renderPreview(value);byId('applyMountButton').disabled=!previewIsUsable(value,state.snapshot)||!(value.changes||[]).length;byId('previewMountButton').textContent=deleteMode?'Refresh delete review':'Refresh review';}
  catch(error){invalidatePreview({validate:false});const issues=error?.issues?.length?error.issues:[];if(issues.length)renderEditorIssues(issues);showNotice(errorNode,`${error?.code?`${error.code}: `:''}${String(error?.message||error)}${error?.detail?` · ${error.detail}`:''}`,'error');}
}

function renderOperationProgress(record, initial=false) {
  const root=byId('operationProgressSteps');clear(root);const steps=[];
  if(initial)steps.push({text:'Backend preview validated',state:'done'},{text:'Publishing configuration revision',state:'current'});
  for(const event of record?.events||[])steps.push({text:event.message||event.event||'Operation event',state:'done'});
  if(record?.state==='RUNNING')steps.push({text:'Waiting for lifecycle actions to finish',state:'current'});
  if(record?.state==='FAILED')steps.push({text:record?.error?.message||'Operation failed',state:'failed'});
  if(record?.state==='SUCCEEDED')steps.push({text:'Configuration and lifecycle actions completed',state:'done'});
  for(const step of steps){const li=document.createElement('li');li.className=step.state;li.textContent=step.text;root.append(li);}
  const pill=byId('operationProgressState');pill.textContent=record?.state||'Running';pill.className=record?.state==='FAILED'?'state-pill bad':record?.state==='SUCCEEDED'?'state-pill good':'state-pill warn';
}
async function monitorOperation(requestId,token){byId('operationProgress').hidden=false;renderOperationProgress(null,true);while(token===state.operationPollToken&&!byId('editorBackdrop').hidden){await new Promise((resolve)=>setTimeout(resolve,500));if(token!==state.operationPollToken)break;try{const record=result(await query('operation.status',{request_id:requestId}));renderOperationProgress(record);if(record.state&&record.state!=='RUNNING')break;}catch(_){/* journal can appear shortly after dispatch */}}}

function editMountByName(name){const cfg=(state.snapshot?.mounts||[]).find((item)=>item?.name===name);if(cfg)openEditor(cfg);else showMountOutcome(`Mount ${name} is not present in the latest configuration snapshot.`,'error');}
async function viewOperations(){await setView('runtime');byId('operationCards')?.scrollIntoView({block:'start',behavior:'smooth'});}

async function applyEditor() {
  const errorNode=byId('editorError');if(!state.preview||!state.previewCandidates||!previewIsUsable(state.preview,state.snapshot)){showNotice(errorNode,'The review is stale or missing. Review the mount again before applying.','error');return;}
  byId('applyMountButton').disabled=true;const isDelete=state.editorDelete, wasExisting=state.editorExisting, submitted=formValue(), appliedName=submitted.name;byId('operationProgressTitle').textContent=isDelete?'Deleting mount…':(wasExisting?'Applying mount changes…':'Creating mount…');
  try{
    const handle=startOperation('config.apply','run',{expected_revision:state.preview.current_revision,candidate_digest:state.preview.candidate_digest,preview_proof:state.preview.preview_proof,mounts:state.previewCandidates});const token=++state.operationPollToken;void monitorOperation(handle.requestId,token);const envelope=await handle.completion;const applied=result(envelope);state.operationPollToken+=1;
    const failures=applied?.lifecycle_failures||[];forceCloseEditor();await loadMounts();
    if(failures.length){const codes=[...new Set(failures.map((item)=>item.code||'lifecycle_failed'))].join(', ');const first=failures[0]||{};const actions=[{label:'View operations',run:()=>void viewOperations()},{label:'View logs',run:()=>void setView('logs')}];if(!isDelete){if(submitted.enabled&&first.retryable)actions.unshift({label:'Retry start',run:()=>void lifecycleAction('start',appliedName),className:'primary'});actions.splice(submitted.enabled&&first.retryable?1:0,0,{label:'Edit mount',run:()=>editMountByName(appliedName),className:first.retryable?'':'primary'});}const detail=first.detail||first.error||'';showMountOutcome(`Configuration was saved, but ${failures.length} lifecycle action${failures.length===1?'':'s'} need attention (${codes})${detail?` · ${detail}`:''}.`, 'error', actions);}
    else if(isDelete) showMountOutcome('Mount deleted. Cache files were not removed by this configuration action.','ok');
    else showMountOutcome(wasExisting?'Mount changes applied successfully.':'Mount created successfully.','ok',[{label:'View runtime',run:()=>void setView('runtime')},{label:'View mount',run:()=>void setView('mounts'),className:'primary'}]);
  }catch(error){state.operationPollToken+=1;invalidatePreview({validate:false});if(error?.issues?.length)renderEditorIssues(error.issues);const prefix=['stale_revision','preview_required','preview_expired','preview_mismatch'].includes(error?.code)?'The configuration changed or your approval expired. Review again. ':'';showNotice(errorNode,`${prefix}${String(error?.message||error)}${error?.detail?` · ${error.detail}`:''}`,'error');}
}

async function openRollback() {
  try {
    const value = result(await preview('config.rollback.preview', {})); state.rollbackPreview = value; const root = byId('rollbackPreview'); clear(root);
    root.append(paragraph(`Current revision ${value.current_revision}. This action requires this fresh one-use preview.`, 'muted'));
    for (const change of value.changes || []) root.append(paragraph(`${change.name}: ${change.kind} · ${(change.consequences || []).map(consequenceLabel).join(', ') || 'configuration change'}`, 'change-card'));
    byId('rollbackBackdrop').hidden = false;
  } catch (error) { showNotice(byId('mountsNotice'), `Rollback unavailable: ${String(error?.message || error)}`, 'error'); }
}
function closeRollback() { state.rollbackPreview = null; byId('rollbackBackdrop').hidden = true; }
async function applyRollback() {
  const value = state.rollbackPreview; if (!value?.preview_proof) return;
  try {
    result(await run('config.rollback', { expected_revision: value.current_revision, previous_digest: value.candidate_digest, preview_proof: value.preview_proof }));
    closeRollback(); await loadMounts(); showNotice(byId('mountsNotice'), 'Previous-known-good configuration restored.', 'ok');
  } catch (error) { closeRollback(); showNotice(byId('mountsNotice'), `Rollback failed: ${String(error?.message || error)}. Preview again before retrying.`, 'error'); }
}



function bytes(value) {
  let n = Number(value || 0); if (!Number.isFinite(n)) return '—';
  const units = ['B','KiB','MiB','GiB','TiB']; let i = 0; while (n >= 1024 && i < units.length - 1) { n /= 1024; i += 1; }
  return `${n >= 10 || i === 0 ? n.toFixed(0) : n.toFixed(1)} ${units[i]}`;
}
function metric(root, name, value) { const cell=document.createElement('div'); const strong=document.createElement('strong'); strong.textContent=name; cell.append(strong,document.createTextNode(String(value ?? '—'))); root.append(cell); }

const jobFields = ['name','type','source','destination','every','network'];
const jobChecks = ['enabled','charging','confirm-sync'];
function normalizeJob(value) { return { name:String(value.name||'').trim(), enabled:Boolean(value.enabled), type:String(value.type||'copy'), source:String(value.source||'').trim(), destination:String(value.destination||'').trim(), every:String(value.every||'1h').trim(), network_mode:String(value.network_mode||'any'), charging_only:Boolean(value.charging_only), min_battery:Number(value.min_battery||0), confirm_destructive:Boolean(value.confirm_destructive) }; }
function invalidateJobPreview(){ state.jobPreview=null; state.jobCandidates=null; byId('applyJobButton').disabled=true; byId('jobPreviewText').textContent=''; hideNotice(byId('jobError')); }
function fillJob(job={},existing=false){ const v=normalizeJob(job); byId('job-name').value=v.name; byId('job-name').readOnly=existing; byId('job-type').value=v.type; byId('job-source').value=v.source; byId('job-destination').value=v.destination; byId('job-every').value=v.every; byId('job-network').value=v.network_mode; byId('job-battery').value=v.min_battery||''; byId('job-enabled').checked=v.enabled; byId('job-charging').checked=v.charging_only; byId('job-confirm-sync').checked=v.confirm_destructive; }
function jobFormValue(){ return normalizeJob({ name:byId('job-name').value,type:byId('job-type').value,source:byId('job-source').value,destination:byId('job-destination').value,every:byId('job-every').value,network_mode:byId('job-network').value,min_battery:byId('job-battery').value,enabled:byId('job-enabled').checked,charging_only:byId('job-charging').checked,confirm_destructive:byId('job-confirm-sync').checked }); }
function openJob(job=null){ if(!state.jobSnapshot)return; state.jobOriginal=job?.name||''; fillJob(job||{enabled:true,type:'copy',every:'1h',network_mode:'any'},Boolean(job)); invalidateJobPreview(); byId('deleteJobButton').hidden=!job; text('jobEditorTitle',job?`Edit ${job.name}`:'Add job'); byId('jobBackdrop').hidden=false; }
function closeJob(){ byId('jobBackdrop').hidden=true; state.jobOriginal=''; invalidateJobPreview(); }
function jobCandidates(deleteMode=false){ const list=[...(state.jobSnapshot?.jobs||[])]; if(deleteMode)return list.filter((j)=>j.name!==state.jobOriginal); const value=jobFormValue(); const index=list.findIndex((j)=>j.name===state.jobOriginal); if(index>=0)list[index]=value;else list.push(value); return list; }
async function previewJob(deleteMode=false){ try{ const candidates=jobCandidates(deleteMode); const value=result(await preview('jobs.preview',{jobs:candidates})); state.jobPreview=value;state.jobCandidates=candidates; byId('jobPreviewText').textContent=`Revision ${value.current_revision} · ${value.destructive_sync_jobs?.length?`destructive sync: ${value.destructive_sync_jobs.join(', ')}`:'no destructive sync jobs'}`;byId('applyJobButton').disabled=!value.preview_proof; }catch(error){invalidateJobPreview();showNotice(byId('jobError'),String(error?.message||error),'error');} }
async function applyJob(){ const v=state.jobPreview;if(!v?.preview_proof)return;try{ result(await run('jobs.apply',{expected_revision:v.current_revision,candidate_digest:v.candidate_digest,preview_proof:v.preview_proof,jobs:state.jobCandidates}));closeJob();await loadJobs();showNotice(byId('jobsNotice'),'Job registry applied from a fresh backend preview.','ok');}catch(error){invalidateJobPreview();showNotice(byId('jobError'),String(error?.message||error),'error');} }
async function runJob(name){ const notice=byId('jobsNotice');try{const pre=result(await preview('job.run.preview',{name}));if(pre?.destructive&&!pre?.confirmed){showNotice(notice,`${name}: destructive sync is not confirmed in its saved definition.`,'error');return;}const handle=startOperation('job.run','run',{name,trigger:'manual'});showNotice(notice,`Job ${name} started as ${handle.requestId}.`);result(await handle.completion);showNotice(notice,`${name} completed.`,'ok');await loadJobs();}catch(error){showNotice(notice,`${name}: ${String(error?.message||error)}`,'error');}}
function renderJobs(snapshot,status){ const root=byId('jobCards');clear(root);const stateMap=new Map((status||[]).map((j)=>[j.name,j]));if(!(snapshot.jobs||[]).length){root.append(paragraph('No scheduled jobs configured.','notice'));return;}for(const job of snapshot.jobs){const card=document.createElement('article');card.className='job-card';const head=document.createElement('div');head.className='mount-head';const t=document.createElement('div');const h=document.createElement('h3');h.textContent=job.name;t.append(h,paragraph(`${job.type}: ${job.source} → ${job.destination}`,'muted code'));const st=stateMap.get(job.name)||{};head.append(t,label(st.last_state|| (job.enabled?'scheduled':'disabled'),st.last_state==='SUCCEEDED'?'state-pill good':st.last_state==='FAILED'?'state-pill bad':'state-pill'));card.append(head);const grid=document.createElement('div');grid.className='runtime-grid';metric(grid,'Every',job.every);metric(grid,'Next run',formatTime(st.next_run_unix_ms));metric(grid,'Runs',st.run_count||0);metric(grid,'Policy',`${job.network_mode||'any'}${job.charging_only?' · charging':''}`);card.append(grid);const actions=document.createElement('div');actions.className='mount-actions';actions.append(button('Run now',()=>void runJob(job.name),'primary'),button('Edit',()=>openJob(job)));card.append(actions);root.append(card);}}
async function loadJobs(){ const [snap,status]=await Promise.all([query('jobs.snapshot'),query('jobs.status')]);state.jobSnapshot=result(snap);renderJobs(state.jobSnapshot,result(status).jobs||[]); }

async function namespaceAction(name,mode){const notice=byId('runtimeNotice');try{const previewName=mode==='apply'?'namespace.preview':'namespace.rollback.preview';const pre=result(await preview(previewName,{name}));if(pre.qualified===false&&mode==='apply'){showNotice(notice,`${name}: ${pre.reason||'namespace visibility is not qualified'}`,'error');return;}if(!pre.preview_proof){throw new Error('Backend did not issue a namespace preview proof');}result(await run(mode==='apply'?'namespace.apply':'namespace.rollback',{name,expected_revision:pre.current_revision,candidate_digest:pre.candidate_digest,preview_proof:pre.preview_proof}));showNotice(notice,`${name}: namespace ${mode} completed.`,'ok');await loadRuntime();}catch(error){showNotice(notice,`${name}: ${String(error?.message||error)}`,'error');}}
async function cacheAction(name,action){const notice=byId('runtimeNotice');try{const pre=result(await preview(`cache.${action}.preview`,{name}));const count=pre.delete_files||0;if(!pre.preview_proof){throw new Error('Backend did not issue a cache preview proof');}if(!globalThis.confirm(`Delete ${count} owned cache file(s) for ${name}?`))return;result(await run(`cache.${action}`,{name,expected_revision:pre.current_revision,candidate_digest:pre.candidate_digest,preview_proof:pre.preview_proof}));showNotice(notice,`${name}: cache ${action} completed.`,'ok');await loadRuntime();}catch(error){showNotice(notice,`${name}: ${String(error?.message||error)}`,'error');}}
function renderRuntime(snapshot, health, policy, cache, inspections, metrics) {
  const root = byId('runtimeCards'); clear(root);
  const hmap = new Map((health || []).map((v) => [v.name, v]));
  const pmap = new Map((policy || []).map((v) => [v.name, v]));
  const cmap = new Map((cache || []).map((v) => [v.name, v]));
  for (const cfg of snapshot.mounts || []) {
    const h = hmap.get(cfg.name) || {}, pd = pmap.get(cfg.name) || {}, cs = cmap.get(cfg.name) || {}, ns = inspections.get(cfg.name) || {}, rcm = metrics.get(cfg.name) || {};
    const card = document.createElement('article'); card.className = 'runtime-card';
    const head = document.createElement('div'); head.className = 'mount-head'; const hh = document.createElement('h3'); hh.textContent = cfg.name;
    head.append(hh, label(h.state || 'UNKNOWN', healthClass(h.state))); card.append(head);
    if (h.failure_code) {
      const failure = paragraph(`${h.failure_code}${h.retryable ? ' · retryable' : ' · manual intervention required'}`, h.retryable ? 'runtime-cause warn-text' : 'runtime-cause error-text');
      card.append(failure);
    }
    const sourceReady = Boolean(h.process_alive && h.mount_alive && h.owned_mount && ns.source_owned);
    const vis = document.createElement('div'); vis.className = 'visibility-matrix';
    for (const cls of ['service','root','shell','termux','app']) {
      const visible = (ns.achieved_classes || []).includes(cls);
      const textValue = sourceReady ? `${cls}: ${visible ? 'visible' : 'not proven'}` : `${cls}: not applicable`;
      vis.append(label(textValue, sourceReady ? (visible ? 'state-pill good' : 'state-pill warn') : 'state-pill neutral'));
    }
    for (const user of ns.users || []) {
      const detail = sourceReady ? `user ${user.user_id}${user.primary ? ' primary' : ''}: ${user.qualified ? 'qualified' : 'not qualified'} · ${user.visible_namespaces || 0}/${user.app_namespaces || 0} visible` : `user ${user.user_id}${user.primary ? ' primary' : ''}: not applicable`;
      vis.append(label(detail, sourceReady ? (user.qualified ? 'state-pill good' : 'state-pill warn') : 'state-pill neutral'));
    }
    card.append(vis);
    const grid = document.createElement('div'); grid.className = 'runtime-grid';
    metric(grid, 'Readiness', h.readiness?.ready ? 'ready' : h.readiness?.waiting_reason || 'waiting');
    metric(grid, 'Policy', pd.decision?.allowed === false ? pd.decision?.reason || 'blocked' : 'allowed');
    metric(grid, 'Recovery', h.failure_code ? (h.retryable ? `retry ${h.restart_attempts || 0}/${h.restart_budget || 0}` : 'automatic retry blocked') : `${h.restart_attempts || 0}/${h.restart_budget || 0}`);
    metric(grid, 'Network', `${h.readiness?.network_class || 'unknown'} · ${h.readiness?.remote_state || 'not probed'}`);
    metric(grid, 'Cache', `${bytes(cs.bytes)} / ${bytes(cs.max_bytes)}`); metric(grid, 'Open files', rcm.open_files ?? '—'); metric(grid, 'Rate', rcm.available ? `${bytes(rcm.speed_bytes_per_sec)}/s` : 'unavailable'); card.append(grid);
    const actions = document.createElement('div'); actions.className = 'mount-actions';
    const makeVisible = button('Make app-visible', () => void namespaceAction(cfg.name, 'apply'));
    makeVisible.disabled = !sourceReady; makeVisible.title = sourceReady ? '' : 'Requires a live Nexus-owned source mount first';
    const hasAppVisibility = (ns.achieved_classes || []).includes('app');
    const releaseVisible = button('Release app visibility', () => void namespaceAction(cfg.name, 'rollback'));
    releaseVisible.disabled = !hasAppVisibility; releaseVisible.title = hasAppVisibility ? '' : 'No observed app visibility to release';
    actions.append(makeVisible, releaseVisible, button('Clear cache', () => void cacheAction(cfg.name, 'clear')), button('Forget cache', () => void cacheAction(cfg.name, 'forget'), 'danger'));
    card.append(actions); root.append(card);
  }
}

function renderRuntimeStatus(runtime){
  const root=byId('runtimeEngineManager'); if(!root)return; clear(root);
  const card=document.createElement('article'); card.className='runtime-card runtime-status-hero';
  const head=document.createElement('div'); head.className='mount-head';
  const title=document.createElement('div'); const h=document.createElement('h3'); h.textContent='Bundled Runtime';
  title.append(h,paragraph('The module payload owns one immutable-at-install runtime. Replace it by building and flashing a new module ZIP.','muted'));
  head.append(title,label(runtime.operational?'operational':'attention',runtime.operational?'state-pill good':'state-pill bad')); card.append(head);
  const grid=document.createElement('div'); grid.className='runtime-grid';
  metric(grid,'Mode',runtime.mode||'managed'); metric(grid,'Source',runtime.source||'nexus-bundled');
  metric(grid,'Canonical',runtime.canonical?'yes':'no'); metric(grid,'Operational',runtime.operational?'yes':'no');
  metric(grid,'Binary',runtime.binary||'—'); metric(grid,'Config',runtime.config||'—'); card.append(grid);
  if(runtime.issues?.length) card.append(detailBlock('Runtime issues',runtime.issues.map((issue)=>['Issue',issue])));
  card.append(paragraph('Runtime import, source registry, live activation, rollback and on-device runtime updates are not part of the static-runtime product line.','muted'));
  root.append(card);
}

async function loadRuntime(){const [snapEnvelope,runtimeEnvelope,he,po,ca]=await Promise.all([query('config.snapshot'),query('runtime.status'),query('mount.health'),query('policy.status'),query('cache.status')]);const snap=result(snapEnvelope),runtime=result(runtimeEnvelope);const notice=byId('runtimeNotice');const runtimeMessage=`Runtime authority: ${runtime.mode||'unknown'} · ${runtime.source||'unknown'}${runtime.canonical?' · canonical':''}`;showNotice(notice,runtimeMessage,runtime.operational?'ok':'error');renderRuntimeStatus(runtime);const inspections=new Map(),metrics=new Map();await Promise.all((snap.mounts||[]).map(async(m)=>{try{inspections.set(m.name,result(await query('namespace.inspect',{name:m.name})));}catch(_){inspections.set(m.name,{});}try{metrics.set(m.name,result(await query('rc.metrics',{name:m.name})));}catch(_){metrics.set(m.name,{});}}));renderRuntime(snap,result(he).health||[],result(po).policies||[],result(ca).caches||[],inspections,metrics);await loadOperations();}

function logFilterValues() {
  return {
    severity: byId('logSeverityFilter')?.value || '',
    source: byId('logSourceFilter')?.value || '',
    search: (byId('logSearch')?.value || '').trim().toLowerCase(),
  };
}
function updateLogSources(records) {
  const select = byId('logSourceFilter'); if (!select) return;
  const current = select.value; const sources = [...new Set(records.map((r) => r.source || r.category || 'nexus'))].sort();
  while (select.options.length > 1) select.remove(1);
  for (const source of sources) { const option = document.createElement('option'); option.value = source; option.textContent = source; select.append(option); }
  if (sources.includes(current)) select.value = current;
}
function renderLogs(records = state.logRecords) {
  state.logRecords = Array.isArray(records) ? records : [];
  updateLogSources(state.logRecords);
  const filters = logFilterValues();
  const filtered = state.logRecords.filter((rec) => {
    const source = rec.source || rec.category || 'nexus'; const body = `${rec.message || ''} ${rec.code || ''} ${rec.name || ''} ${source}`.toLowerCase();
    return (!filters.severity || rec.severity === filters.severity) && (!filters.source || source === filters.source) && (!filters.search || body.includes(filters.search));
  }).slice().sort((a, b) => Number(b.time_unix_ms || 0) - Number(a.time_unix_ms || 0));
  const root = byId('logCards'); clear(root);
  if (!filtered.length) { root.append(paragraph('No matching diagnostic records.', 'notice')); return; }
  for (const rec of filtered) {
    const card = document.createElement('article'); card.className = `log-card severity-${rec.severity || 'INFO'}`;
    const head = document.createElement('div'); head.className = 'log-head';
    head.append(label(rec.severity || 'INFO'), paragraph(`${formatTime(rec.time_unix_ms)} · ${rec.source || rec.category || 'nexus'}`, 'muted'));
    const message = rec.message || `${rec.category || ''} ${rec.name || ''} ${rec.state || ''} ${rec.code || ''}`.trim(); card.append(head, paragraph(message, 'code'));
    if (rec.code && rec.message) card.append(paragraph(rec.code, 'log-code'));
    if (Number(rec.suppressed || 0) > 0) {
      const details = document.createElement('details'); details.className = 'log-details'; const summary = document.createElement('summary'); const shown = Array.isArray(rec.details) ? rec.details.length : 0; summary.textContent = shown < rec.suppressed ? `Show sample (${shown} of ${rec.suppressed} suppressed help lines)` : `Show ${rec.suppressed} suppressed help line${rec.suppressed === 1 ? '' : 's'}`; details.append(summary);
      const pre = document.createElement('pre'); pre.textContent = (rec.details || []).join('\n'); details.append(pre); card.append(details);
    }
    root.append(card);
  }
}
async function loadLogs(){const limit=Number(state.settingsSnapshot?.settings?.log_limit||100);const value=result(await query('diagnostics.logs',{limit}));state.logRecords=value.records||[];renderLogs();if(value.records?.length)state.logCursor=Math.max(...value.records.map((r)=>Number(r.time_unix_ms||0)));byId('logFollow').checked=Boolean(state.settingsSnapshot?.settings?.log_follow);}

function renderPlatform(value){const root=byId('platformDetails');clear(root);const mgr=value.root_manager||{};const rows=[['Manager',mgr.name||mgr.kind],['Compatible',mgr.compatible?'yes':'no'],['Embedded WebUI',mgr.capabilities?.embedded_webui?'available':'not claimed'],['Integrity',value.integrity?.ok?'verified':'not verified'],['State schema',value.state?.schema_version??'—'],['Transport',transportName()]];for(const [k,v] of rows){const row=document.createElement('div');row.className='detail-row';row.append(document.createTextNode(k),document.createTextNode(String(v??'—')));root.append(row);}}
function renderDoctor(report){const root=byId('doctorCards');clear(root);for(const check of report.checks||[]){const card=document.createElement('article');card.className=`doctor-card ${check.status}`;const h=document.createElement('strong');h.textContent=`${check.status} · ${check.summary}`;card.append(h);if(check.detail)card.append(paragraph(check.detail,'muted'));if(check.guidance)card.append(paragraph(`Next: ${check.guidance}`,'muted'));root.append(card);}}
async function runDoctorUI(){try{renderDoctor(result(await query('doctor.report')));}catch(error){showNotice(byId('settingsNotice'),String(error?.message||error),'error');}}
function decodeBase64(data){const binary=atob(data);const bytes=new Uint8Array(binary.length);for(let i=0;i<binary.length;i++)bytes[i]=binary.charCodeAt(i);return bytes;}
async function buildBundle(){const root=byId('bundleResult');try{const meta=result(await run('doctor.bundle',{}));const payload=result(await query('doctor.bundle.read',{bundle_id:meta.bundle_id}));const blob=new Blob([decodeBase64(payload.data)],{type:'application/zip'});const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=payload.filename;a.textContent=`Save ${payload.filename}`;const copy=button('Copy checksum',()=>void navigator.clipboard?.writeText(payload.sha256));clear(root);root.hidden=false;root.append(a,document.createTextNode(` · ${bytes(payload.size)} · `),copy);setTimeout(()=>URL.revokeObjectURL(url),60000);}catch(error){showNotice(root,String(error?.message||error),'error');}}

function settingsValue(){return {refresh_seconds:Number(byId('setting-refresh').value),log_limit:Number(byId('setting-log-limit').value),log_max_bytes:Number(byId('setting-log-max').value),log_backups:Number(byId('setting-log-backups').value),webui_idle_seconds:Number(byId('setting-idle').value),default_view:byId('setting-default-view').value,default_mount_view:byId('setting-mount-view').value,log_follow:byId('setting-log-follow').checked,dense_mode:byId('setting-dense').checked,reduced_motion:byId('setting-reduced-motion').checked};}
function applySettingsPresentation(){const s=state.settingsSnapshot?.settings||{};document.body.classList.toggle('dense',Boolean(s.dense_mode||s.default_mount_view==='dense'));document.body.classList.toggle('reduce-motion',Boolean(s.reduced_motion));byId('logFollow').checked=Boolean(s.log_follow);}
async function previewSettings(){try{const v=result(await preview('ui.settings.preview',{settings:settingsValue()}));state.settingsPreview=v;byId('applySettingsButton').disabled=!v.preview_proof;byId('settingsPreview').textContent=`Revision ${v.current_revision} · refresh ${v.settings.refresh_seconds}s · logs ${bytes(v.settings.log_max_bytes)} × ${v.settings.log_backups} · idle ${v.settings.webui_idle_seconds}s`;}catch(error){showNotice(byId('settingsNotice'),String(error?.message||error),'error');}}
async function applySettings(){const v=state.settingsPreview;if(!v?.preview_proof)return;try{state.settingsSnapshot=result(await run('ui.settings.apply',{expected_revision:v.current_revision,candidate_digest:v.candidate_digest,preview_proof:v.preview_proof,settings:v.settings}));state.settingsPreview=null;byId('applySettingsButton').disabled=true;applySettingsPresentation();showNotice(byId('settingsNotice'),'WebUI settings applied from fresh preview.','ok');scheduleRefresh();}catch(error){state.settingsPreview=null;byId('applySettingsButton').disabled=true;showNotice(byId('settingsNotice'),String(error?.message||error),'error');}}
function fillSettings(s){byId('setting-refresh').value=s.refresh_seconds;byId('setting-log-limit').value=s.log_limit;byId('setting-log-max').value=s.log_max_bytes;byId('setting-log-backups').value=s.log_backups;byId('setting-idle').value=s.webui_idle_seconds;byId('setting-default-view').value=s.default_view;byId('setting-mount-view').value=s.default_mount_view||'cards';byId('setting-log-follow').checked=Boolean(s.log_follow);byId('setting-dense').checked=Boolean(s.dense_mode);byId('setting-reduced-motion').checked=Boolean(s.reduced_motion);}
async function useRemoteForMount(endpoint){if(!state.snapshot){state.snapshot=result(await query('config.snapshot'));}openEditor();const parsed=parseRemoteEndpoint(endpoint);byId('field-remote').value=endpoint;state.editorRemotePath=parsed.path;byId('mountRemotePath').value=parsed.path?`/${parsed.path}`:'/';state.editorManualRemote=false;updateRemoteEndpoint();}
async function browseRemote(remote,path=''){state.remote=remote;state.remotePath=path;const root=byId('remoteEntries');clear(root);try{const value=result(await query('provider.browse',{remote,path,limit:150}));byId('remoteBreadcrumb').textContent=`${remote}:${value.path||''}`;if(path){root.append(button('↑ Parent',()=>void browseRemote(remote,path.split('/').slice(0,-1).join('/'))));}for(const entry of value.entries||[]){const row=document.createElement('div');row.className='remote-entry';row.append(paragraph(`${entry.is_dir?'📁':'📄'} ${entry.name}`,'code'));const actions=document.createElement('div');actions.className='button-row';if(entry.is_dir)actions.append(button('Open',()=>void browseRemote(remote,entry.path)));const endpoint=`${remote}:${entry.is_dir?entry.path:path}`;actions.append(button('Use for mount',()=>void useRemoteForMount(endpoint)));actions.append(button('Use as job source',()=>void useRemoteForJob(endpoint,'source')));actions.append(button('Use as job destination',()=>void useRemoteForJob(endpoint,'destination')));row.append(actions);root.append(row);}}catch(error){root.append(paragraph(`Browse failed: ${String(error?.message||error)}`,'notice error'));}}
async function useRemoteForJob(endpoint,field){if(!state.jobSnapshot){state.jobSnapshot=result(await query('jobs.snapshot'));}openJob();byId(field==='source'?'job-source':'job-destination').value=endpoint;invalidateJobPreview();}
async function loadRemotes(){const value=result(await query('provider.remotes'));const root=byId('remoteButtons');clear(root);for(const name of value.remotes||[])root.append(button(`${name}:`,()=>void browseRemote(name,'')));if(!(value.remotes||[]).length)root.append(paragraph('No configured remotes were returned.','muted'));}
async function loadSettings(){const [settings,platform]=await Promise.all([query('ui.settings'),query('platform.status')]);state.settingsSnapshot=result(settings);fillSettings(state.settingsSnapshot.settings);applySettingsPresentation();renderPlatform(result(platform));await Promise.all([runDoctorUI(),loadRemotes()]);}

function stopTimer() { if (state.timer) clearTimeout(state.timer); state.timer = null; }
function refreshDelay(){const configured=Number(state.settingsSnapshot?.settings?.refresh_seconds||5)*1000;if(state.activeView==='runtime'||state.activeView==='jobs')return Math.max(2000,configured);if(state.activeView==='logs'&&state.settingsSnapshot?.settings?.log_follow)return Math.max(2000,configured);if(state.activeView==='mounts')return Math.max(3000,configured);return Math.max(5000,configured);}
function scheduleRefresh() {
  stopTimer(); if (document.visibilityState !== 'visible') return;
  state.timer=setTimeout(async()=>{try{if(state.activeView==='mounts')await loadMounts();else if(state.activeView==='jobs')await loadJobs();else if(state.activeView==='runtime')await loadRuntime();else if(state.activeView==='logs'&&state.settingsSnapshot?.settings?.log_follow)await loadLogs();else if(state.activeView==='home')await loadHome();}catch(_){}finally{scheduleRefresh();}},refreshDelay());
}
async function setView(view) {
  const names=['home','mounts','jobs','runtime','logs','settings']; if(!names.includes(view))view='home';state.activeView=view;stopTimer();
  let activeTab=null;for(const name of names)byId(`view-${name}`).hidden=name!==view;for(const tab of document.querySelectorAll('.tab')){const active=tab.dataset.view===view;tab.classList.toggle('active',active);if(active){tab.setAttribute('aria-current','page');activeTab=tab;}else tab.removeAttribute('aria-current');}if(activeTab)activeTab.scrollIntoView({block:'nearest',inline:'center',behavior:document.body.classList.contains('reduce-motion')?'auto':'smooth'});text('pageTitle',view.charAt(0).toUpperCase()+view.slice(1));
  try{if(view==='mounts')await loadMounts();else if(view==='jobs')await loadJobs();else if(view==='runtime')await loadRuntime();else if(view==='logs')await loadLogs();else if(view==='settings')await loadSettings();else await loadHome();}catch(error){compatibility.hidden=false;compatibility.className='notice error';compatibility.textContent=`Backend unavailable or incompatible: ${String(error?.message||error)}`;}scheduleRefresh();
}
async function bootstrap(){compatibility.hidden=false;compatibility.className='notice';compatibility.textContent='Checking backend compatibility…';badge.textContent='Connecting…';try{await ensureConnected();state.settingsSnapshot=result(await query('ui.settings'));applySettingsPresentation();await setView(state.settingsSnapshot.settings?.default_view||'home');}catch(error){compatibility.hidden=false;compatibility.className='notice error';compatibility.textContent=`Backend unavailable or incompatible: ${String(error?.message||error)}`;badge.textContent='Unavailable';}}
for(const tab of document.querySelectorAll('.tab'))tab.addEventListener('click',()=>void setView(tab.dataset.view));
byId('retryButton')?.addEventListener('click',()=>void loadHome());byId('refreshMountsButton')?.addEventListener('click',()=>void loadMounts());byId('refreshOperationsButton')?.addEventListener('click',()=>void loadOperations());byId('refreshJobsButton')?.addEventListener('click',()=>void loadJobs());byId('refreshRuntimeButton')?.addEventListener('click',()=>void loadRuntime());byId('refreshLogsButton')?.addEventListener('click',()=>void loadLogs());byId('refreshSettingsButton')?.addEventListener('click',()=>void loadSettings());
byId('reconcileButton')?.addEventListener('click',()=>void reconcileAll());byId('addMountButton')?.addEventListener('click',()=>openEditor());byId('rollbackButton')?.addEventListener('click',()=>void openRollback());byId('closeEditorButton')?.addEventListener('click',closeEditor);byId('closeRollbackButton')?.addEventListener('click',closeRollback);byId('previewMountButton')?.addEventListener('click',()=>void previewEditor(false));byId('deleteMountButton')?.addEventListener('click',()=>void previewEditor(true));byId('applyMountButton')?.addEventListener('click',()=>void applyEditor());byId('applyRollbackButton')?.addEventListener('click',()=>void applyRollback());
byId('addJobButton')?.addEventListener('click',()=>openJob());byId('closeJobButton')?.addEventListener('click',closeJob);byId('previewJobButton')?.addEventListener('click',()=>void previewJob(false));byId('deleteJobButton')?.addEventListener('click',()=>void previewJob(true));byId('applyJobButton')?.addEventListener('click',()=>void applyJob());byId('jobForm')?.addEventListener('input',invalidateJobPreview);byId('jobForm')?.addEventListener('change',invalidateJobPreview);
byId('runDoctorButton')?.addEventListener('click',()=>void runDoctorUI());byId('bundleButton')?.addEventListener('click',()=>void buildBundle());byId('refreshRemotesButton')?.addEventListener('click',()=>void loadRemotes());byId('previewSettingsButton')?.addEventListener('click',()=>void previewSettings());byId('applySettingsButton')?.addEventListener('click',()=>void applySettings());byId('settingsForm')?.addEventListener('input',()=>{state.settingsPreview=null;byId('applySettingsButton').disabled=true;});byId('logFollow')?.addEventListener('change',()=>{if(state.settingsSnapshot?.settings){state.settingsSnapshot.settings.log_follow=byId('logFollow').checked;scheduleRefresh();}});
for(const id of ['logSeverityFilter','logSourceFilter','logSearch'])byId(id)?.addEventListener(id==='logSearch'?'input':'change',()=>renderLogs());
byId('mountForm')?.addEventListener('submit',(event)=>{event.preventDefault();void previewEditor(false);});
byId('mountForm')?.addEventListener('input',(event)=>{if(event.target?.id==='field-name')state.editorNameTouched=true;if(event.target?.id==='field-mountpoint')state.editorMountpointTouched=true;if(event.target?.id==='field-remote'){state.editorManualRemote=true;state.editorRemoteReachable=null;const parsed=parseRemoteEndpoint(event.target.value);state.editorRemotePath=parsed.path;byId('mountRemoteEndpoint').textContent=event.target.value||'—';renderProviderReadiness();}invalidatePreview();});
byId('mountForm')?.addEventListener('change',(event)=>{if(event.target?.id==='field-vfs_profile')updateProfilePresentation();if(event.target?.id==='field-network_mode')updateNetworkPolicyHelp();invalidatePreview();});
byId('mountRemoteSelect')?.addEventListener('change',()=>{state.editorRemotePath='';state.editorRemoteReachable=null;byId('mountRemotePath').value='/';state.editorManualRemote=false;byId('manualRemoteWrap').hidden=true;updateRemoteEndpoint();});
byId('browseMountRemoteButton')?.addEventListener('click',()=>void browseMountRemote());
byId('testMountRemoteButton')?.addEventListener('click',()=>void testMountRemote());
byId('manualRemoteButton')?.addEventListener('click',()=>setManualRemote(!state.editorManualRemote));
byId('mountRemoteParentButton')?.addEventListener('click',()=>{const remote=byId('mountRemoteSelect').value;const parent=state.editorRemotePath.split('/').filter(Boolean).slice(0,-1).join('/');void browseMountRemote(remote,parent);});
byId('useMountRemoteFolderButton')?.addEventListener('click',chooseCurrentRemoteFolder);
for(const node of document.querySelectorAll('[data-mountpoint-template]'))node.addEventListener('click',()=>applyMountpointSuggestion(node.dataset.mountpointTemplate));
byId('keepEditingButton')?.addEventListener('click',()=>{byId('discardBackdrop').hidden=true;byId('closeEditorButton').focus();});
byId('discardChangesButton')?.addEventListener('click',forceCloseEditor);
byId('editorBackdrop')?.addEventListener('click',(event)=>{if(event.target===byId('editorBackdrop'))closeEditor();});byId('discardBackdrop')?.addEventListener('click',(event)=>{if(event.target===byId('discardBackdrop')){byId('discardBackdrop').hidden=true;byId('closeEditorButton').focus();}});byId('jobBackdrop')?.addEventListener('click',(event)=>{if(event.target===byId('jobBackdrop'))closeJob();});byId('rollbackBackdrop')?.addEventListener('click',(event)=>{if(event.target===byId('rollbackBackdrop'))closeRollback();});document.addEventListener('keydown',(event)=>{if(!byId('discardBackdrop').hidden&&trapModalTab(event,byId('discardBackdrop')))return;if(!byId('editorBackdrop').hidden&&trapModalTab(event,byId('editorBackdrop')))return;if(!byId('jobBackdrop').hidden&&trapModalTab(event,byId('jobBackdrop')))return;if(!byId('rollbackBackdrop').hidden&&trapModalTab(event,byId('rollbackBackdrop')))return;if(event.key!=='Escape')return;if(!byId('discardBackdrop').hidden){byId('discardBackdrop').hidden=true;byId('closeEditorButton').focus();return;}if(!byId('jobBackdrop').hidden){closeJob();return;}if(!byId('editorBackdrop').hidden){closeEditor();return;}if(!byId('rollbackBackdrop').hidden)closeRollback();});document.addEventListener('visibilitychange',()=>{if(document.visibilityState==='visible')void setView(state.activeView);else stopTimer();});void bootstrap();
