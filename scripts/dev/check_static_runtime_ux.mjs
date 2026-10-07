import fs from 'node:fs';

const app = fs.readFileSync('module/webroot/app.js', 'utf8');
const model = fs.readFileSync('module/webroot/model.js', 'utf8');
const html = fs.readFileSync('module/webroot/index.html', 'utf8');
const css = fs.readFileSync('module/webroot/style.css', 'utf8');
const combined = `${app}\n${model}\n${html}\n${css}`;

for (const token of [
  "query('runtime.status')",
  "byId('runtimeEngineManager')",
  'Bundled Runtime',
  'runtime-status-hero',
  'Replace it by building and flashing a new module ZIP.',
]) {
  if (!app.includes(token) && !combined.includes(token)) {
    throw new Error(`static runtime UX token missing: ${token}`);
  }
}

for (const token of [
  'runtime.manager',
  'runtime.activate',
  'runtime.rollback',
  'runtime.recover',
  'runtime.update.',
  'runtime.source.',
  'runtime.candidates',
  'runtime.activation.status',
  'runtimeManagerAction',
  'runtimeCandidateAction',
  'runtimeSourceChannelOptions',
  'runtimeUpdatePolicyInput',
  'Runtime Manager',
  'runtime-manager',
]) {
  if (combined.includes(token)) throw new Error(`retired mutable-runtime UX token present: ${token}`);
}

for (const token of [
  'id="view-runtime"',
  'id="runtimeEngineManager"',
]) {
  if (!html.includes(token)) throw new Error(`runtime status DOM contract missing: ${token}`);
}
if (!css.includes('.runtime-status-hero')) throw new Error('runtime status CSS contract missing');

for (const forbidden of ['innerHTML', 'outerHTML', 'insertAdjacentHTML', 'eval(', 'new Function', 'localStorage', 'sessionStorage', 'indexedDB']) {
  if (app.includes(forbidden)) throw new Error(`unsafe browser primitive present: ${forbidden}`);
}

console.log('STATIC-RUNTIME WebUI contract PASS');
