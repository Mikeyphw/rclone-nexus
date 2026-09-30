import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

const files = ['module/webroot/bridge.js', 'module/webroot/app.js'];
for (const file of files) {
  const result = spawnSync(process.execPath, ['--check', file], { encoding: 'utf8' });
  if (result.status !== 0) {
    process.stderr.write(result.stderr || result.stdout || `syntax failed: ${file}\n`);
    process.exit(result.status || 1);
  }
}

const bridge = await import(new URL(`../../module/webroot/bridge.js?test=${Date.now()}`, import.meta.url));
const goodCaps = {
  schema_version: 1,
  protocol: { min: 1, max: 1 },
  operations: [{ name: 'provider.status', class: 'query' }],
};
bridge.validateCapabilities(goodCaps);
for (const bad of [
  { ...goodCaps, schema_version: 2 },
  { ...goodCaps, protocol: { min: 2, max: 3 } },
  { ...goodCaps, operations: null },
]) {
  let failed = false;
  try { bridge.validateCapabilities(bad); } catch (_) { failed = true; }
  if (!failed) throw new Error('compatibility mismatch was accepted');
}

const commands = [];
globalThis.ksu = {
  exec(command, _options, callbackName) {
    commands.push(command);
    const callback = globalThis[callbackName];
    if (command.includes('--capabilities')) {
      callback(0, JSON.stringify(goodCaps), '');
      return;
    }
    const match = command.match(/--request-base64 ([A-Za-z0-9_-]+)$/);
    if (!match) { callback(2, '', 'unexpected command'); return; }
    const normalized = match[1].replaceAll('-', '+').replaceAll('_', '/');
    const padded = normalized + '='.repeat((4 - normalized.length % 4) % 4);
    const request = JSON.parse(Buffer.from(padded, 'base64').toString('utf8'));
    callback(0, JSON.stringify({ schema_version: 1, response: { schema_version: 1, kind: 'response', request_id: request.request_id, protocol: 1, ok: true, result: { ready: true } } }), '');
  },
};
if (await bridge.selectTransport() !== 'embedded') throw new Error('real embedded capability was not preferred');
bridge.validateCapabilities(await bridge.capabilities());
const envelope = await bridge.query('provider.status', {});
if (envelope?.response?.ok !== true) throw new Error('embedded typed query failed');
if (commands.some((command) => !command.startsWith("'/data/adb/modules/rclone_nexus/system/bin/racctl' webui bridge "))) throw new Error('embedded bridge used a non-fixed command');
if (commands.some((command) => /\b(sh|bash)\s+-c\b/.test(command))) throw new Error('embedded bridge exposed shell -c');

const source = readFileSync('module/webroot/bridge.js', 'utf8');
if (source.includes('innerHTML') || source.includes('eval(') || source.includes('new Function')) throw new Error('unsafe dynamic browser primitive found');
console.log('WEB-X01 JS contract PASS');
