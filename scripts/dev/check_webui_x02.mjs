import { readFileSync } from 'node:fs';
import { candidate, isDestructivePreview, operationSummary, previewIsUsable, removeMount, replaceMount } from '../../module/webroot/model.js';

const existing = {
  name: 'drive', enabled: true, remote: 'drive:', mountpoint: '/storage/emulated/0/Rclone/Drive',
  vfs_profile: 'custom', vfs_cache_mode: 'full', allow_other: true, network_mode: 'offline-allowed',
  cache_high_water: 90, cache_low_water: 75, has_args_file: true,
};
const edited = replaceMount([existing], 'drive', { ...existing, mountpoint: '/storage/emulated/0/Rclone/New', min_battery: '25' });
if (edited.length !== 1 || edited[0].mountpoint !== '/storage/emulated/0/Rclone/New') throw new Error('mount replacement failed');
if (edited[0].vfs_profile !== 'custom') throw new Error('custom profile was not preserved');
if ('has_args_file' in edited[0]) throw new Error('private args-file metadata leaked into candidate mutation');
if (edited[0].min_battery !== 25) throw new Error('numeric policy normalization failed');
if (removeMount(edited, 'drive').length !== 0) throw new Error('mount delete candidate failed');

const snapshot = { revision: 7 };
const fresh = { current_revision: 7, candidate_digest: 'abc', preview_proof: 'proof', changes: [] };
if (!previewIsUsable(fresh, snapshot)) throw new Error('fresh backend preview rejected');
if (previewIsUsable({ ...fresh, current_revision: 6 }, snapshot)) throw new Error('stale revision accepted');
if (previewIsUsable({ ...fresh, preview_proof: '' }, snapshot)) throw new Error('missing proof accepted');
if (!isDestructivePreview({ changes: [{ kind: 'delete', reasons: ['deleted'] }] })) throw new Error('delete not classified as destructive');

const summary = operationSummary({ request_id:'r1', operation:'mount.start', state:'RUNNING', cancellable:true, updated_unix_ms:42, events:[{event:'progress',message:'starting'}] });
if (summary.requestId !== 'r1' || summary.message !== 'starting' || !summary.cancellable) throw new Error('operation journal model failed');

const app = readFileSync('module/webroot/app.js','utf8');
const bridge = readFileSync('module/webroot/bridge.js','utf8');
for (const bad of ['localStorage','sessionStorage','indexedDB','innerHTML','outerHTML','insertAdjacentHTML','eval(','new Function']) {
  if (app.includes(bad) || bridge.includes(bad)) throw new Error(`browser code uses forbidden primitive: ${bad}`);
}
for (const required of [
  "query('config.snapshot')", "preview('config.preview'", "startOperation('config.apply'", 'preview_proof',
  "query('operation.list'", "cancel('operation.cancel'", "startOperation('mount.reconcile'", "run('config.rollback'",
  "document.addEventListener('visibilitychange'",
]) if (!app.includes(required)) throw new Error(`WEB-X02 app contract missing: ${required}`);
for (const required of ['startOperation', 'X-Rclone-Nexus-Request-ID', "operationClass, args", "CLASS_PATH"]) {
  if (!bridge.includes(required)) throw new Error(`WEB-X02 bridge contract missing: ${required}`);
}
console.log('WEB-X02 JS/model contract PASS');
