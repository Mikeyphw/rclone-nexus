import { spawnSync } from 'node:child_process';

const scripts = [
  'scripts/dev/check_webui_js.mjs',
  'scripts/dev/check_webui_x02.mjs',
  'scripts/dev/check_webui_x03.mjs',
  'scripts/dev/check_webui_g1.mjs',
  'scripts/dev/check_static_runtime_ux.mjs',
  'scripts/dev/check_webui_shortcut.mjs',
];

for (const script of scripts) {
  const result = spawnSync(process.execPath, [script], {
    cwd: process.cwd(),
    encoding: 'utf8',
  });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  if (result.status !== 0) process.exit(result.status || 1);
}
console.log('GRAND-G1 WebUI contract PASS');
