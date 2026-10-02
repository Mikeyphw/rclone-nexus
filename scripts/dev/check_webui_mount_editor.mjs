import { readFileSync } from 'node:fs';
import { localMountIssues, parseRemoteEndpoint, profilePresentation, suggestMountDefaults } from '../../module/webroot/model.js';

const html = readFileSync('module/webroot/index.html', 'utf8');
const app = readFileSync('module/webroot/app.js', 'utf8');
const css = readFileSync('module/webroot/style.css', 'utf8');

const parsed = parseRemoteEndpoint('drive:Media/Movies');
if (parsed.remote !== 'drive' || parsed.path !== 'Media/Movies') throw new Error('remote endpoint parsing failed');
const suggested = suggestMountDefaults('drive:Media/Movies');
if (suggested.name !== 'drive-movies' || !suggested.mountpoint.endsWith('/Movies')) throw new Error(`mount defaults failed: ${JSON.stringify(suggested)}`);

const issues = localMountIssues({
  name: 'bad name', remote: '', mountpoint: 'relative', vfs_profile: 'custom', vfs_cache_mode: 'full',
  vfs_cache_max_size: '2GBB', vfs_cache_max_age: '1h30m', dir_cache_time: '30m', poll_interval: '15s',
  log_level: 'INFO', network_mode: 'any', min_battery: 120, cache_high_water: 90, cache_low_water: 95,
});
for (const code of ['invalid_mount_name','remote_required','mountpoint_absolute_required','invalid_cache_size','invalid_min_battery','invalid_cache_watermarks']) {
  if (!issues.some((item) => item.code === code)) throw new Error(`missing local issue ${code}`);
}
if (issues.some((item) => item.field === 'vfs_cache_max_age')) throw new Error('compound Go duration 1h30m should be accepted locally');

const conflict = localMountIssues({
  name:'two', remote:'drive:', mountpoint:'/storage/emulated/0/Rclone/Movies/Child', vfs_profile:'custom', vfs_cache_mode:'full',
  log_level:'INFO', network_mode:'any', cache_high_water:90, cache_low_water:75,
}, [{name:'one', mountpoint:'/storage/emulated/0/Rclone/Movies'}], '');
if (!conflict.some((item) => item.code === 'mountpoint_overlap' && item.related_mount === 'one')) throw new Error('local mountpoint conflict feedback missing');

const profile = profilePresentation('balanced', [{name:'balanced',description:'Balanced',options:{vfs_cache_mode:'full',vfs_cache_max_size:'2GiB'}}], {profile:'balanced'});
if (!profile.recommended || profile.options.vfs_cache_max_size !== '2GiB') throw new Error('profile presentation failed');

for (const token of [
  'mountRemoteSelect','mountRemoteBrowser','testMountRemoteButton','profileCards','profileRecommendation','editorIssueSummary',
  'previewCountdown','operationProgress','discardBackdrop','Review mount','Make visible to Android apps','Start automatically','data-mountpoint-template','networkPolicyHelp','providerReadiness','previewExpiryHelp','reviewSafety',
]) if (!html.includes(token)) throw new Error(`guided mount editor HTML missing ${token}`);

for (const token of [
  "query('config.validate'", "query('provider.remotes'", "query('provider.browse'", "query('vfs.profiles'", 'renderEditorIssues',
  'validateEditorNow', 'monitorOperation', 'preview_expires_unix_ms', 'editorDirty', 'operation.status', 'trapModalTab', 'applyMountpointSuggestion',
  'renderProviderReadiness','editorSession','editorProfileTouched','editorBackendValidated',"query('provider.status'","label:'Retry start'","label:'Edit mount'","label:'View logs'",'profile-managed',
]) if (!app.includes(token)) throw new Error(`guided mount editor logic missing ${token}`);

for (const token of ['profile-grid','issue-summary','sticky-actions','remote-folder-grid','progress-steps','field-invalid','profile-managed']) {
  if (!css.includes(token)) throw new Error(`guided mount editor styling missing ${token}`);
}
if (!app.includes('if(!state.editorDirty)state.editorInitial=serializeEditor()')) throw new Error('async dirty baseline guard missing');
if (!app.includes('node.disabled=!custom') || !app.includes('field-${field}`).value=value')) throw new Error('profile-managed raw VFS synchronization missing');
if (!app.includes('Outside Nexus/provider state') || !app.includes('No mount overlap')) throw new Error('destination backend validation chips missing');

for (const forbidden of ['innerHTML','insertAdjacentHTML','localStorage','sessionStorage','indexedDB','eval(','new Function']) {
  if (app.includes(forbidden) || html.includes(forbidden)) throw new Error(`forbidden browser primitive in mount editor: ${forbidden}`);
}

console.log('WebUI guided mount editor contract PASS');
