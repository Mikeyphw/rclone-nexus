const SCHEMA_VERSION = 1;
const PROTOCOL_VERSION = 1;
const MAX_TEXT = 1024 * 1024;
const EMBEDDED_BINARY = '/data/adb/modules/rclone_nexus/system/bin/racctl';
let requestCounter = 0;
let selected = null;

function bounded(value, limit = 4096) {
  const text = String(value ?? '');
  return text.length <= limit ? text : `${text.slice(0, limit)}…`;
}

function requestId() {
  requestCounter += 1;
  return `webui-${Date.now()}-${requestCounter}`;
}

function requestEnvelope(name, operationClass, args = {}) {
  return {
    schema_version: SCHEMA_VERSION,
    request_id: requestId(),
    client: { name: 'rclone-nexus-webui', version: '0.1.0-dev', protocol: { min: PROTOCOL_VERSION, max: PROTOCOL_VERSION } },
    operation: { name, class: operationClass, args },
  };
}

function base64url(value) {
  const bytes = new TextEncoder().encode(JSON.stringify(value));
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '');
}

function csrf() {
  const part = document.cookie.split(';').map((item) => item.trim()).find((item) => item.startsWith('rnexus_csrf='));
  return part ? decodeURIComponent(part.slice('rnexus_csrf='.length)) : '';
}

function embeddedAvailable() {
  return Boolean(globalThis.ksu && typeof globalThis.ksu.exec === 'function');
}

async function embeddedExec(encoded, timeoutMs = 10000) {
  if (!embeddedAvailable()) throw new Error('Embedded manager bridge is unavailable');
  if (!/^[A-Za-z0-9_-]+$/.test(encoded)) throw new Error('Embedded request encoding is invalid');
  const callback = `rnexus_${Date.now()}_${Math.floor(Math.random() * 1000000)}`;
  const command = `'${EMBEDDED_BINARY}' webui bridge --request-base64 ${encoded}`;
  return new Promise((resolve, reject) => {
    let settled = false;
    const cleanup = () => { try { delete globalThis[callback]; } catch (_) { globalThis[callback] = undefined; } };
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      cleanup();
      reject(new Error('Embedded manager callback timed out'));
    }, timeoutMs);
    globalThis[callback] = (errno, stdout, stderr) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      cleanup();
      const out = bounded(stdout, MAX_TEXT);
      if (Number(errno) !== 0) {
        reject(new Error(bounded(stderr || `Embedded backend exited with status ${errno}`)));
        return;
      }
      try { resolve(JSON.parse(out)); } catch (_) { reject(new Error('Embedded backend returned invalid JSON')); }
    };
    try {
      globalThis.ksu.exec(command, JSON.stringify({}), callback);
    } catch (error) {
      clearTimeout(timer);
      cleanup();
      reject(error);
    }
  });
}

async function embeddedCapabilities(timeoutMs = 10000) {
  if (!embeddedAvailable()) throw new Error('Embedded manager bridge is unavailable');
  const callback = `rnexus_caps_${Date.now()}_${Math.floor(Math.random() * 1000000)}`;
  const command = `'${EMBEDDED_BINARY}' webui bridge --capabilities`;
  return new Promise((resolve, reject) => {
    let settled = false;
    const cleanup = () => { try { delete globalThis[callback]; } catch (_) { globalThis[callback] = undefined; } };
    const timer = setTimeout(() => { if (!settled) { settled = true; cleanup(); reject(new Error('Embedded capability callback timed out')); } }, timeoutMs);
    globalThis[callback] = (errno, stdout, stderr) => {
      if (settled) return;
      settled = true; clearTimeout(timer); cleanup();
      if (Number(errno) !== 0) { reject(new Error(bounded(stderr || `Embedded backend exited with status ${errno}`))); return; }
      try { resolve(JSON.parse(bounded(stdout, MAX_TEXT))); } catch (_) { reject(new Error('Embedded capability response is invalid JSON')); }
    };
    try { globalThis.ksu.exec(command, JSON.stringify({}), callback); }
    catch (error) { clearTimeout(timer); cleanup(); reject(error); }
  });
}

async function standaloneProbe() {
  const response = await fetch('/api/v1/transport', { method: 'GET', credentials: 'same-origin', cache: 'no-store' });
  if (!response.ok) throw new Error(`Standalone transport probe failed (${response.status})`);
  const value = await response.json();
  if (value?.schema_version !== SCHEMA_VERSION || value?.transport !== 'standalone' || value?.typed_backend !== true) {
    throw new Error('Standalone transport capability mismatch');
  }
  return value;
}

export async function selectTransport() {
  if (embeddedAvailable()) {
    selected = 'embedded';
    const caps = await embeddedCapabilities();
    if (caps?.schema_version !== SCHEMA_VERSION || !Array.isArray(caps?.operations)) throw new Error('Embedded bridge contract mismatch');
    return selected;
  }
  await standaloneProbe();
  selected = 'standalone';
  return selected;
}

export function transportName() { return selected; }

export function validateCapabilities(value) {
  if (!value || value.schema_version !== SCHEMA_VERSION) throw new Error('Unsupported backend capability schema');
  const minimum = Number(value.protocol?.min ?? 0);
  const maximum = Number(value.protocol?.max ?? 0);
  if (!(minimum <= PROTOCOL_VERSION && maximum >= PROTOCOL_VERSION)) {
    throw new Error(`Backend protocol ${minimum}-${maximum} is incompatible with WebUI protocol ${PROTOCOL_VERSION}`);
  }
  if (!Array.isArray(value.operations)) throw new Error('Backend operation registry is missing');
  return value;
}

export async function capabilities() {
  if (selected === 'standalone') {
    const response = await fetch('/api/v1/capabilities', { credentials: 'same-origin', cache: 'no-store' });
    if (!response.ok) throw new Error(`Capabilities request failed (${response.status})`);
    return response.json();
  }
  if (selected === 'embedded') {
    return embeddedCapabilities();
  }
  throw new Error('Transport is not selected');
}

export async function query(name, args = {}) {
  if (!/^[a-z][a-z0-9_.-]{1,127}$/.test(name)) throw new Error('Invalid typed operation name');
  if (selected === 'standalone') {
    const token = csrf();
    if (!token) throw new Error('Standalone CSRF token is unavailable');
    const response = await fetch(`/api/v1/query/${encodeURIComponent(name)}`, {
      method: 'POST', credentials: 'same-origin', cache: 'no-store',
      headers: { 'Content-Type': 'application/json', 'X-Rclone-Nexus-CSRF': token },
      body: JSON.stringify(args),
    });
    if (!response.ok) throw new Error(`Typed query failed (${response.status})`);
    return response.json();
  }
  if (selected === 'embedded') return embeddedExec(base64url(requestEnvelope(name, 'query', args)));
  throw new Error('Transport is not selected');
}
