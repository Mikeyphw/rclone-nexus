import fs from 'node:fs';

const app = fs.readFileSync('module/webroot/app.js', 'utf8');
const html = fs.readFileSync('module/webroot/index.html', 'utf8');
const bridge = fs.readFileSync('module/webroot/bridge.js', 'utf8');

for (const view of ['home','mounts','jobs','runtime','logs','settings']) {
  if (!html.includes(`data-view="${view}"`) || !html.includes(`id="view-${view}"`)) throw new Error(`missing final navigation view ${view}`);
}
for (const op of ['jobs.snapshot','jobs.preview','jobs.apply','job.run','namespace.inspect','namespace.preview','namespace.apply','namespace.rollback.preview','namespace.rollback','policy.status','cache.status','cache.clear.preview','cache.clear','cache.forget.preview','cache.forget','rc.metrics','diagnostics.logs','doctor.report','doctor.bundle','doctor.bundle.read','provider.remotes','provider.browse','ui.settings','ui.settings.preview','ui.settings.apply']) {
  if (!app.includes(`'${op}'`) && !app.includes(`\`${op}`)) throw new Error(`WebUI does not reference typed operation ${op}`);
}
for (const forbidden of ['localStorage', 'sessionStorage', 'indexedDB', 'innerHTML', 'eval(', 'new Function', '--rc-user', '--rc-pass']) {
  if (app.includes(forbidden) || html.includes(forbidden)) throw new Error(`forbidden WebUI primitive/content: ${forbidden}`);
}
if (app.includes('rclone.conf')) throw new Error('browser logic must not reference rclone.conf');
if (!app.includes('preview_proof') || !app.includes("jobs.apply") || !app.includes("ui.settings.apply")) throw new Error('job/settings mutations are not preview-proof bound');
if (!app.includes('document.visibilityState') || !app.includes('log_follow')) throw new Error('bounded visibility-aware follow/polling missing');
if (!app.includes('doctor.bundle.read') || !app.includes('Blob')) throw new Error('support bundle save flow missing');
if (!app.includes('provider.browse') || !app.includes('Use for mount')) throw new Error('credential-free remote browser integration missing');

for (const token of ['reduced_motion','log_max_bytes','log_backups','webui_idle_seconds','default_mount_view','user.user_id','useRemoteForMount']) {
  if (!app.includes(token) && !html.includes(token)) throw new Error(`missing WEB-X03 settings/runtime contract ${token}`);
}

if (!bridge.includes('EMBEDDED_BINARY')) throw new Error('fixed embedded bridge contract missing');
console.log('WEB-X03 JavaScript contract PASS');
