import { capabilities, cancel, preview, query, reconcile, run, selectTransport, startOperation, transportName, validateCapabilities } from './bridge.js';
import { DEFAULT_MOUNT, candidate, consequenceLabel, isDestructivePreview, mountRows, operationSummary, previewIsUsable, removeMount, replaceMount } from './model.js';

const byId = (id) => document.getElementById(id);
const compatibility = byId('compatibility');
const badge = byId('transportBadge');
const state = {
  caps: null, snapshot: null, activeView: 'home', timer: null, editorOriginal: '', editorDelete: false,
  preview: null, previewCandidates: null, rollbackPreview: null, refreshing: false,
  jobSnapshot: null, jobOriginal: '', jobPreview: null, jobCandidates: null, settingsSnapshot: null, settingsPreview: null, logCursor: 0, remote: '', remotePath: '',
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
    'operation.list','operation.cancel','namespace.inspect','namespace.preview','namespace.apply','namespace.rollback.preview','namespace.rollback',
    'policy.status','cache.status','cache.clear.preview','cache.clear','cache.forget.preview','cache.forget','rc.metrics',
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

async function namespaceAction(name,mode){const notice=byId('runtimeNotice');try{const previewName=mode==='apply'?'namespace.preview':'namespace.rollback.preview';const pre=result(await preview(previewName,{name}));if(pre.qualified===false){showNotice(notice,`${name}: ${pre.reason||'namespace visibility is not qualified'}`,'error');return;}result(await run(mode==='apply'?'namespace.apply':'namespace.rollback',{name}));showNotice(notice,`${name}: namespace ${mode} completed.`,'ok');await loadRuntime();}catch(error){showNotice(notice,`${name}: ${String(error?.message||error)}`,'error');}}
async function cacheAction(name,action){const notice=byId('runtimeNotice');try{const pre=result(await preview(`cache.${action}.preview`,{name}));const count=pre.delete_files||0;if(!globalThis.confirm(`Delete ${count} owned cache file(s) for ${name}?`))return;result(await run(`cache.${action}`,{name}));showNotice(notice,`${name}: cache ${action} completed.`,'ok');await loadRuntime();}catch(error){showNotice(notice,`${name}: ${String(error?.message||error)}`,'error');}}
function renderRuntime(snapshot,health,policy,cache,inspections,metrics){const root=byId('runtimeCards');clear(root);const hmap=new Map((health||[]).map((v)=>[v.name,v]));const pmap=new Map((policy||[]).map((v)=>[v.name,v]));const cmap=new Map((cache||[]).map((v)=>[v.name,v]));for(const cfg of snapshot.mounts||[]){const h=hmap.get(cfg.name)||{},pd=pmap.get(cfg.name)||{},cs=cmap.get(cfg.name)||{},ns=inspections.get(cfg.name)||{},rcm=metrics.get(cfg.name)||{};const card=document.createElement('article');card.className='runtime-card';const head=document.createElement('div');head.className='mount-head';const hh=document.createElement('h3');hh.textContent=cfg.name;head.append(hh,label(h.state||'UNKNOWN',healthClass(h.state)));card.append(head);const vis=document.createElement('div');vis.className='visibility-matrix';for(const cls of ['service','root','shell','termux','app']){const visible=(ns.achieved_classes||[]).includes(cls);vis.append(label(`${cls}: ${visible?'visible':'not proven'}`,visible?'state-pill good':'state-pill warn'));}for(const user of ns.users||[]){const detail=`user ${user.user_id}${user.primary?' primary':''}: ${user.qualified?'qualified':'not qualified'} · ${user.visible_namespaces||0}/${user.app_namespaces||0} visible`;vis.append(label(detail,user.qualified?'state-pill good':'state-pill warn'));}card.append(vis);const grid=document.createElement('div');grid.className='runtime-grid';metric(grid,'Readiness',h.readiness?.ready?'ready':h.readiness?.waiting_reason||'waiting');metric(grid,'Policy',pd.decision?.allowed===false?pd.decision?.reason||'blocked':'allowed');metric(grid,'Retry',`${h.restart_attempts||0}/${h.restart_budget||0}`);metric(grid,'Cache',`${bytes(cs.bytes)} / ${bytes(cs.max_bytes)}`);metric(grid,'Open files',rcm.open_files??'—');metric(grid,'Rate',rcm.available?`${bytes(rcm.speed_bytes_per_sec)}/s`:'unavailable');card.append(grid);const actions=document.createElement('div');actions.className='mount-actions';actions.append(button('Make app-visible',()=>void namespaceAction(cfg.name,'apply')),button('Release app visibility',()=>void namespaceAction(cfg.name,'rollback')),button('Clear cache',()=>void cacheAction(cfg.name,'clear')),button('Forget cache',()=>void cacheAction(cfg.name,'forget'),'danger'));card.append(actions);root.append(card);}}
async function loadRuntime(){const snap=result(await query('config.snapshot'));const [he,po,ca]=await Promise.all([query('mount.health'),query('policy.status'),query('cache.status')]);const inspections=new Map(),metrics=new Map();await Promise.all((snap.mounts||[]).map(async(m)=>{try{inspections.set(m.name,result(await query('namespace.inspect',{name:m.name})));}catch(_){inspections.set(m.name,{});}try{metrics.set(m.name,result(await query('rc.metrics',{name:m.name})));}catch(_){metrics.set(m.name,{});}}));renderRuntime(snap,result(he).health||[],result(po).policies||[],result(ca).caches||[],inspections,metrics);await loadOperations();}

function renderLogs(records){const root=byId('logCards');clear(root);if(!records.length){root.append(paragraph('No matching diagnostic records.','notice'));return;}for(const rec of records){const card=document.createElement('article');card.className=`log-card severity-${rec.severity||'INFO'}`;const head=document.createElement('div');head.className='log-head';head.append(label(rec.severity||'INFO'),paragraph(`${formatTime(rec.time_unix_ms)} · ${rec.source||rec.category||'nexus'}`,'muted'));card.append(head,paragraph(rec.message||`${rec.category||''} ${rec.name||''} ${rec.state||''} ${rec.code||''}`.trim(),'code'));root.append(card);}}
async function loadLogs(){const limit=Number(state.settingsSnapshot?.settings?.log_limit||100);const value=result(await query('diagnostics.logs',{limit}));renderLogs(value.records||[]);if(value.records?.length)state.logCursor=Math.max(...value.records.map((r)=>Number(r.time_unix_ms||0)));byId('logFollow').checked=Boolean(state.settingsSnapshot?.settings?.log_follow);}

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
async function useRemoteForMount(endpoint){if(!state.snapshot){state.snapshot=result(await query('config.snapshot'));}openEditor();byId('field-remote').value=endpoint;invalidatePreview();}
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
  for(const name of names)byId(`view-${name}`).hidden=name!==view;for(const tab of document.querySelectorAll('.tab'))tab.classList.toggle('active',tab.dataset.view===view);text('pageTitle',view.charAt(0).toUpperCase()+view.slice(1));
  try{if(view==='mounts')await loadMounts();else if(view==='jobs')await loadJobs();else if(view==='runtime')await loadRuntime();else if(view==='logs')await loadLogs();else if(view==='settings')await loadSettings();else await loadHome();}catch(error){compatibility.className='notice error';compatibility.textContent=`Backend unavailable or incompatible: ${String(error?.message||error)}`;}scheduleRefresh();
}
async function bootstrap(){compatibility.className='notice';compatibility.textContent='Checking backend compatibility…';badge.textContent='Connecting…';try{await ensureConnected();state.settingsSnapshot=result(await query('ui.settings'));applySettingsPresentation();await setView(state.settingsSnapshot.settings?.default_view||'home');}catch(error){compatibility.className='notice error';compatibility.textContent=`Backend unavailable or incompatible: ${String(error?.message||error)}`;badge.textContent='Unavailable';}}
for(const tab of document.querySelectorAll('.tab'))tab.addEventListener('click',()=>void setView(tab.dataset.view));
byId('retryButton')?.addEventListener('click',()=>void loadHome());byId('refreshMountsButton')?.addEventListener('click',()=>void loadMounts());byId('refreshOperationsButton')?.addEventListener('click',()=>void loadOperations());byId('refreshJobsButton')?.addEventListener('click',()=>void loadJobs());byId('refreshRuntimeButton')?.addEventListener('click',()=>void loadRuntime());byId('refreshLogsButton')?.addEventListener('click',()=>void loadLogs());byId('refreshSettingsButton')?.addEventListener('click',()=>void loadSettings());
byId('reconcileButton')?.addEventListener('click',()=>void reconcileAll());byId('addMountButton')?.addEventListener('click',()=>openEditor());byId('rollbackButton')?.addEventListener('click',()=>void openRollback());byId('closeEditorButton')?.addEventListener('click',closeEditor);byId('closeRollbackButton')?.addEventListener('click',closeRollback);byId('previewMountButton')?.addEventListener('click',()=>void previewEditor(false));byId('deleteMountButton')?.addEventListener('click',()=>void previewEditor(true));byId('applyMountButton')?.addEventListener('click',()=>void applyEditor());byId('applyRollbackButton')?.addEventListener('click',()=>void applyRollback());
byId('addJobButton')?.addEventListener('click',()=>openJob());byId('closeJobButton')?.addEventListener('click',closeJob);byId('previewJobButton')?.addEventListener('click',()=>void previewJob(false));byId('deleteJobButton')?.addEventListener('click',()=>void previewJob(true));byId('applyJobButton')?.addEventListener('click',()=>void applyJob());byId('jobForm')?.addEventListener('input',invalidateJobPreview);byId('jobForm')?.addEventListener('change',invalidateJobPreview);
byId('runDoctorButton')?.addEventListener('click',()=>void runDoctorUI());byId('bundleButton')?.addEventListener('click',()=>void buildBundle());byId('refreshRemotesButton')?.addEventListener('click',()=>void loadRemotes());byId('previewSettingsButton')?.addEventListener('click',()=>void previewSettings());byId('applySettingsButton')?.addEventListener('click',()=>void applySettings());byId('settingsForm')?.addEventListener('input',()=>{state.settingsPreview=null;byId('applySettingsButton').disabled=true;});byId('logFollow')?.addEventListener('change',()=>{if(state.settingsSnapshot?.settings){state.settingsSnapshot.settings.log_follow=byId('logFollow').checked;scheduleRefresh();}});
byId('mountForm')?.addEventListener('submit',(event)=>{event.preventDefault();void previewEditor(false);});byId('mountForm')?.addEventListener('input',invalidatePreview);byId('mountForm')?.addEventListener('change',invalidatePreview);byId('editorBackdrop')?.addEventListener('click',(event)=>{if(event.target===byId('editorBackdrop'))closeEditor();});byId('jobBackdrop')?.addEventListener('click',(event)=>{if(event.target===byId('jobBackdrop'))closeJob();});byId('rollbackBackdrop')?.addEventListener('click',(event)=>{if(event.target===byId('rollbackBackdrop'))closeRollback();});document.addEventListener('visibilitychange',()=>{if(document.visibilityState==='visible')void setView(state.activeView);else stopTimer();});void bootstrap();
