import { capabilities, query, selectTransport, transportName, validateCapabilities } from './bridge.js';

const byId = (id) => document.getElementById(id);
const compatibility = byId('compatibility');
const badge = byId('transportBadge');

function text(id, value) { const node = byId(id); if (node) node.textContent = String(value ?? '—'); }
function result(envelope) {
  if (!envelope?.response) throw new Error('Backend envelope is missing a response');
  if (envelope.response.ok !== true) throw new Error(envelope.response.error?.message || envelope.response.error?.code || 'Backend request failed');
  return envelope.response.result ?? {};
}
function countMounts(value) {
  if (Array.isArray(value)) return value.length;
  if (Array.isArray(value?.mounts)) return value.mounts.length;
  if (value && typeof value === 'object') return Object.keys(value).filter((key) => key !== 'schema_version').length;
  return 0;
}

async function load() {
  compatibility.className = 'notice';
  compatibility.textContent = 'Checking backend compatibility…';
  badge.textContent = 'Connecting…';
  try {
    const transport = await selectTransport();
    badge.textContent = transport === 'embedded' ? 'Embedded manager bridge' : 'Standalone loopback';
    const caps = await capabilities();
    validateCapabilities(caps);
    const operations = new Set(caps.operations.map((item) => item?.name));
    for (const required of ['provider.status', 'mount.status', 'platform.status']) {
      if (!operations.has(required)) throw new Error(`Backend is missing required operation: ${required}`);
    }
    const [providerEnvelope, mountsEnvelope, platformEnvelope] = await Promise.all([
      query('provider.status'), query('mount.status'), query('platform.status'),
    ]);
    const provider = result(providerEnvelope);
    const mounts = result(mountsEnvelope);
    const platform = result(platformEnvelope);

    text('backendValue', caps.server?.version || caps.server?.name || 'Connected');
    text('backendDetail', `Schema ${caps.schema_version} · protocol ${caps.protocol.min}-${caps.protocol.max}`);
    text('providerValue', provider?.ready === true ? 'Ready' : (provider?.state || 'Observed'));
    text('providerDetail', provider?.reason || provider?.rclone_version || 'Credential-free provider status');
    text('mountValue', countMounts(mounts));
    text('mountDetail', 'Managed mount definitions visible to the backend');
    const manager = platform?.root_manager || platform?.manager || {};
    text('rootValue', manager?.name || manager?.kind || 'Detected');
    text('rootDetail', manager?.compatible === false ? 'Compatibility not qualified' : 'Capability-qualified runtime');

    compatibility.className = 'notice ok';
    compatibility.textContent = `Compatible backend connected through ${transportName()}. This foundation build is read-only.`;
  } catch (error) {
    compatibility.className = 'notice error';
    compatibility.textContent = `Backend unavailable or incompatible: ${String(error?.message || error)}`;
    badge.textContent = 'Unavailable';
  }
}

byId('retryButton')?.addEventListener('click', () => { void load(); });
void load();
