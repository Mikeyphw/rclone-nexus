import fs from 'node:fs';

const bridgeSource = fs.readFileSync('module/webroot/bridge.js', 'utf8');
const app = fs.readFileSync('module/webroot/app.js', 'utf8');
const html = fs.readFileSync('module/webroot/index.html', 'utf8');
const css = fs.readFileSync('module/webroot/style.css', 'utf8');

for (const forbidden of ['innerHTML','outerHTML','insertAdjacentHTML','eval(','new Function','localStorage','sessionStorage','indexedDB']) {
  if (app.includes(forbidden) || bridgeSource.includes(forbidden)) throw new Error(`forbidden browser primitive: ${forbidden}`);
}
for (const secret of ['--rc-user','--rc-pass','refresh_token','client_secret']) {
  if (app.includes(secret) || html.includes(secret)) throw new Error(`secret-bearing browser content: ${secret}`);
}
for (const token of [
  'preview_proof','expected_revision','candidate_digest',
  "namespace.preview","namespace.apply","namespace.rollback.preview","namespace.rollback",
  "cache.clear.preview","cache.clear","cache.forget.preview","cache.forget",
]) {
  if (!app.includes(token)) throw new Error(`mutation-preview contract missing: ${token}`);
}
if (!app.includes("event.key!=='Escape'")) throw new Error('modal Escape-key semantics missing');
if (!app.includes("setAttribute('aria-current','page')")) throw new Error('active navigation accessibility state missing');

for (const token of [
  'name="viewport"','aria-label="Primary navigation"','aria-live="polite"','aria-modal="true"',
]) if (!html.includes(token)) throw new Error(`accessibility contract missing: ${token}`);
for (const view of ['home','mounts','jobs','runtime','logs','settings']) {
  if (!html.includes(`id="view-${view}"`)) throw new Error(`missing responsive view ${view}`);
}
for (const token of ['@media (max-width: 680px)', '.tabs { overflow-x: auto;', '.form-grid { grid-template-columns: 1fr;', 'width: min(820px,100%)', '@media (prefers-reduced-motion: reduce)']) {
  if (!css.includes(token)) throw new Error(`phone/tablet responsive contract missing: ${token}`);
}

// A broken embedded manager bridge must not strand the UI when the authenticated
// standalone transport is available. The fallback must be capability-proven, not
// fabricated.
globalThis.ksu = {
  exec(_command, _options, callbackName) {
    globalThis[callbackName](7, '', 'embedded unavailable');
  },
};
globalThis.fetch = async (path) => {
  if (path !== '/api/v1/transport') throw new Error(`unexpected fallback path ${path}`);
  return { ok: true, async json() { return { schema_version: 1, transport: 'standalone', typed_backend: true }; } };
};
const fallback = await import(new URL(`../../module/webroot/bridge.js?g1fallback=${Date.now()}`, import.meta.url));
if (await fallback.selectTransport() !== 'standalone') throw new Error('embedded failure did not fall back to standalone');
if (fallback.transportName() !== 'standalone') throw new Error('fallback transport state is not standalone');

// If neither authority is proven, selection must fail rather than inventing a
// successful transport.
globalThis.ksu = {
  exec(_command, _options, callbackName) {
    globalThis[callbackName](9, '', 'bridge dead');
  },
};
globalThis.fetch = async () => ({ ok: false, status: 503, async json() { return {}; } });
const unavailable = await import(new URL(`../../module/webroot/bridge.js?g1fail=${Date.now()}`, import.meta.url));
let failed = false;
try { await unavailable.selectTransport(); } catch (error) {
  failed = String(error?.message || error).includes('standalone fallback unavailable');
}
if (!failed || unavailable.transportName() !== null) throw new Error('dual transport failure did not fail closed');

console.log('WEB-G1 JavaScript/security contract PASS');
