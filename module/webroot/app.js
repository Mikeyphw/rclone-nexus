import { capabilities, cancel, preview, query, reconcile, run, selectTransport, startOperation, transportName, validateCapabilities } from './bridge.js';
import { DEFAULT_MOUNT, candidate, consequenceLabel, isDestructivePreview, mountRows, operationSummary, previewIsUsable, removeMount, replaceMount } from './model.js';

const byId = (id) => document.getElementById(id);
const compatibility = byId('compatibility');
const badge = byId('transportBadge');
const state = {
  caps: null, snapshot: null, activeView: 'home', timer: null, editorOriginal: '', editorDelete: false,
  preview: null, previewCandidates: null, rollbackPreview: null, refreshing: false,
};

function text(id, value) { const node = byId(id); if (node) node.textContent = String(value ?? '—'); }
function clear(node) { while (node?.firstChild) node.removeChild(node.firstChild); }
function backendError(envelope) {
  const error = new Error(envelope?.response?.error?.message || envelope?.response?.error?.code || 'Backend request failed');
  error.code = envelope?.response?.error?.code || 'backend_error';
  error.detail = envelope?.response?.error?.detail || '';
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
function formatTime(value) { if (!value) return '—'; try { return new Date(Number(value)).toLocaleString(); } catch (_) { return '—'; } }
function label(textValue, className = 'state-pill') { const node = document.createElement('span'); node.className = className; node.textContent = String(textValue); return node; }
function paragraph(value, className = '') { const node = document.createElement('p'); if (className) node.className = className; node.textContent = String(value ?? ''); return node; }
function button(title, handler, className = '') { const node = document.createElement('button'); node.type = 'button'; node.textContent = title; if (className) node.className = className; node.addEventListener('click', handler); return node; }

function requiredOperations(caps) {
  const names = new Set(caps.operations.map((item) => item?.name));
  const required = [
    'provider.status','platform.status','config.snapshot','config.preview','config.apply','config.rollback.preview','config.rollback',
    'mount.status','mount.health','mount.start.preview','mount.start','mount.stop','mount.restart','mount.reconcile',
    'operation.list','operation.cancel',
  ];
  for (const name of required) if (!names.has(name)) throw new Error(`Backend is missing required operation: ${name}`);
}

async function ensureConnected() {
  if (state.caps) return state.caps;
  const transport = await selectTransport();
  badge.textContent = transport === 'embedded' ? 'Embedded manager bridge' : 'Standalone loopback';
  const caps = validateCapabilities(await capabilities());
  requiredOperations(caps); state.caps = caps;
  compatibility.className = 'notice ok';
  compatibility.textContent = `Compatible backend connected through ${transportName()}. Runtime truth remains backend-owned.`;
  return caps;
}

async function loadHome() {
  const caps = await ensureConnected();
  const [providerEnvelope, mountsEnvelope, platformEnvelope, operationsEnvelope] = await Promise.all([
    query('provider.status'), query('mount.status'), query('platform.status'), query('operation.list', { limit: 30 }),
  ]);
  const provider = result(providerEnvelope), mounts = result(mountsEnvelope), platform = result(platformEnvelope), operations = result(operationsEnvelope);
  text('backendValue', caps.server?.version || caps.server?.name || 'Connected');
  text('backendDetail', `Schema ${caps.schema_version} · protocol ${caps.protocol.min}-${caps.protocol.max}`);
  text('providerValue', provider?.ready === true ? 'Ready' : (provider?.state || 'Observed'));
  text('providerDetail', provider?.reason || provider?.rclone_version || 'Credential-free provider status');
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
  if (value === 'STOPPED' || value === 'RETRYING' || value === 'DEGRADED' || value === 'REMOTE_OFFLINE') return 'state-pill warn';
  return 'state-pill bad';
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
    actions.append(
      button('Start', () => void lifecycleAction('start', row.config.name)),
      button('Stop', () => void lifecycleAction('stop', row.config.name)),
      button('Restart', () => void lifecycleAction('restart', row.config.name)),
      button('Edit', () => openEditor(row.config), 'primary'),
    );
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
  } catch (error) { showNotice(notice, `${name}: ${String(error?.message || error)}`, 'error'); }
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

function invalidatePreview() {
  state.preview = null; state.previewCandidates = null; byId('previewPanel').hidden = true; byId('applyMountButton').disabled = true; hideNotice(byId('editorError'));
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
}

function openEditor(config = null) {
  if (!state.snapshot) return;
  const existing = Boolean(config?.name); state.editorOriginal = existing ? config.name : ''; state.editorDelete = false;
  fillForm(config || DEFAULT_MOUNT, existing); invalidatePreview();
  text('editorTitle', existing ? `Edit ${config.name}` : 'Add mount'); byId('deleteMountButton').hidden = !existing; byId('editorBackdrop').hidden = false;
  byId('field-remote').focus();
}
function closeEditor() { byId('editorBackdrop').hidden = true; state.editorOriginal = ''; state.editorDelete = false; invalidatePreview(); }

function candidateRegistry(deleteMode = false) {
  if (!state.snapshot) throw new Error('Configuration snapshot is unavailable');
  if (deleteMode) return removeMount(state.snapshot.mounts, state.editorOriginal);
  return replaceMount(state.snapshot.mounts, state.editorOriginal, formValue());
}

function renderPreview(value) {
  const root = byId('previewChanges'); clear(root);
  text('previewMeta', `Revision ${value.current_revision} · proof expires ${formatTime(value.preview_expires_unix_ms)}${isDestructivePreview(value) ? ' · destructive change' : ''}`);
  for (const change of value.changes || []) {
    const card = document.createElement('div'); card.className = 'change-card'; const title = document.createElement('strong'); title.textContent = `${change.name}: ${change.kind}`; card.append(title);
    if (change.reasons?.length) card.append(paragraph(`Changed: ${change.reasons.join(', ')}`, 'muted'));
    if (change.consequences?.length) card.append(paragraph(`Consequences: ${change.consequences.map(consequenceLabel).join(', ')}`, 'muted'));
    if (change.restart) card.append(paragraph('Lifecycle: restart/start/stop required', 'muted'));
    root.append(card);
  }
  if (!(value.changes || []).length) root.append(paragraph('No configuration changes.', 'muted'));
  byId('previewPanel').hidden = false;
}

async function previewEditor(deleteMode = false) {
  const errorNode = byId('editorError'); hideNotice(errorNode); state.editorDelete = deleteMode;
  try {
    const candidates = candidateRegistry(deleteMode); const value = result(await preview('config.preview', { mounts: candidates }));
    state.preview = value; state.previewCandidates = candidates; renderPreview(value);
    byId('applyMountButton').disabled = !previewIsUsable(value, state.snapshot) || !(value.changes || []).length;
  } catch (error) { invalidatePreview(); showNotice(errorNode, `${error?.code ? `${error.code}: ` : ''}${String(error?.message || error)}`, 'error'); }
}

async function applyEditor() {
  const errorNode = byId('editorError');
  if (!state.preview || !state.previewCandidates || !previewIsUsable(state.preview, state.snapshot)) { showNotice(errorNode, 'Preview is stale or missing. Preview again before applying.', 'error'); return; }
  byId('applyMountButton').disabled = true;
  try {
    const handle = startOperation('config.apply', 'run', {
      expected_revision: state.preview.current_revision, candidate_digest: state.preview.candidate_digest,
      preview_proof: state.preview.preview_proof, mounts: state.previewCandidates,
    });
    const envelope = await handle.completion; result(envelope); closeEditor(); await loadMounts(); showNotice(byId('mountsNotice'), 'Configuration applied from the fresh backend preview.', 'ok');
  } catch (error) {
    invalidatePreview();
    const prefix = ['stale_revision','preview_required','preview_expired','preview_mismatch'].includes(error?.code) ? 'Configuration changed or preview expired. Preview again. ' : '';
    showNotice(errorNode, `${prefix}${String(error?.message || error)}`, 'error');
  }
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

function stopTimer() { if (state.timer) clearTimeout(state.timer); state.timer = null; }
function scheduleRefresh() {
  stopTimer();
  if (document.visibilityState !== 'visible') return;
  const delay = state.activeView === 'operations' ? 1800 : state.activeView === 'mounts' ? 4000 : 10000;
  state.timer = setTimeout(async () => { try { if (state.activeView === 'operations') await loadOperations(); else if (state.activeView === 'mounts') await loadMounts(); else await loadHome(); } catch (_) {} finally { scheduleRefresh(); } }, delay);
}

async function setView(view) {
  state.activeView = view; stopTimer();
  for (const name of ['home','mounts','operations']) byId(`view-${name}`).hidden = name !== view;
  for (const tab of document.querySelectorAll('.tab')) tab.classList.toggle('active', tab.dataset.view === view);
  text('pageTitle', view === 'home' ? 'Home' : view === 'mounts' ? 'Mounts' : 'Operations');
  try { if (view === 'mounts') await loadMounts(); else if (view === 'operations') await loadOperations(); else await loadHome(); }
  catch (error) { compatibility.className = 'notice error'; compatibility.textContent = `Backend unavailable or incompatible: ${String(error?.message || error)}`; }
  scheduleRefresh();
}

async function bootstrap() {
  compatibility.className = 'notice'; compatibility.textContent = 'Checking backend compatibility…'; badge.textContent = 'Connecting…';
  try { await ensureConnected(); await setView('home'); }
  catch (error) { compatibility.className = 'notice error'; compatibility.textContent = `Backend unavailable or incompatible: ${String(error?.message || error)}`; badge.textContent = 'Unavailable'; }
}

for (const tab of document.querySelectorAll('.tab')) tab.addEventListener('click', () => void setView(tab.dataset.view));
byId('retryButton')?.addEventListener('click', () => void loadHome());
byId('refreshMountsButton')?.addEventListener('click', () => void loadMounts());
byId('refreshOperationsButton')?.addEventListener('click', () => void loadOperations());
byId('reconcileButton')?.addEventListener('click', () => void reconcileAll());
byId('addMountButton')?.addEventListener('click', () => openEditor());
byId('rollbackButton')?.addEventListener('click', () => void openRollback());
byId('closeEditorButton')?.addEventListener('click', closeEditor);
byId('closeRollbackButton')?.addEventListener('click', closeRollback);
byId('previewMountButton')?.addEventListener('click', () => void previewEditor(false));
byId('deleteMountButton')?.addEventListener('click', () => void previewEditor(true));
byId('applyMountButton')?.addEventListener('click', () => void applyEditor());
byId('applyRollbackButton')?.addEventListener('click', () => void applyRollback());
byId('mountForm')?.addEventListener('submit', (event) => { event.preventDefault(); void previewEditor(false); });
byId('mountForm')?.addEventListener('input', invalidatePreview);
byId('mountForm')?.addEventListener('change', invalidatePreview);
byId('editorBackdrop')?.addEventListener('click', (event) => { if (event.target === byId('editorBackdrop')) closeEditor(); });
byId('rollbackBackdrop')?.addEventListener('click', (event) => { if (event.target === byId('rollbackBackdrop')) closeRollback(); });
document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') void setView(state.activeView); else stopTimer(); });
void bootstrap();
