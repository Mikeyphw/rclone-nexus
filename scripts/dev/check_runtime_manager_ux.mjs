import fs from 'node:fs';
import { runtimeManagerAction, runtimeCandidateAction, runtimeIssueCanRetry } from '../../module/webroot/model.js';

const app = fs.readFileSync('module/webroot/app.js','utf8');
const css = fs.readFileSync('module/webroot/style.css','utf8');
const html = fs.readFileSync('module/webroot/index.html','utf8');

const manager = { actions: {
  rollback:{enabled:false,operation:'runtime.rollback',class:'run',reason:'previous runtime is not qualified'},
  update_retry:{enabled:false,operation:'runtime.update.check',class:'run',reason:'last update result is not retryable'},
}};
if (runtimeManagerAction(manager,'rollback').enabled) throw new Error('rollback helper ignored backend disabled state');
if (!runtimeManagerAction(manager,'rollback').reason.includes('not qualified')) throw new Error('rollback helper lost backend reason');
if (runtimeManagerAction({},'rollback').enabled) throw new Error('missing action failed open');
const candidate = { actions: { activate:{enabled:false,operation:'runtime.activate',class:'run',reason:'candidate is not qualified'} } };
if (runtimeCandidateAction(candidate,'activate').enabled) throw new Error('candidate activation failed open');
if (runtimeIssueCanRetry({retryable:false,recovery_actions:['runtime.update.check']})) throw new Error('terminal issue exposed retry action');
if (!runtimeIssueCanRetry({retryable:true,recovery_actions:['runtime.update.check']})) throw new Error('retryable issue lost recovery action');

for (const token of [
  "query('runtime.manager')",
  "run('runtime.test'",
  "run('runtime.source.resolve'",
  "run('runtime.source.import-resolution'",
  "run('runtime.source.import-local'",
  "run('runtime.source.register'",
  "run('runtime.update.policy.apply'",
  "preview('migration.preview'",
  "run('migration.apply'",
  "preview('migration.finalize.preview'",
  "run('migration.finalize'",
  "run('migration.rollback'",
  'runtimeManagerAction(state.runtimeManager',
  'runtimeCandidateAction(candidate',
  'Nothing is selected implicitly',
]) if (!app.includes(token)) throw new Error(`Runtime Manager production UX token missing: ${token}`);

for (const token of ['Runtime Manager','Runtime sources & import','Available runtimes','NewFuture migration','Errors & recovery','Active provenance']) {
  if (!app.includes(token)) throw new Error(`Runtime Manager surface missing: ${token}`);
}
for (const token of ['--safe-bottom','env(safe-area-inset-bottom','@media (max-width:680px)','.runtime-form','.migration-choices']) {
  if (!css.includes(token)) throw new Error(`Runtime Manager mobile/safe-area contract missing: ${token}`);
}
if (!html.includes('id="view-runtime"')) throw new Error('Runtime page is missing');
for (const forbidden of ['innerHTML','outerHTML','insertAdjacentHTML','eval(','new Function','localStorage','sessionStorage','indexedDB']) {
  if (app.includes(forbidden)) throw new Error(`forbidden browser authority primitive: ${forbidden}`);
}
console.log('UX-X01 Runtime Manager JavaScript/state contract PASS');
