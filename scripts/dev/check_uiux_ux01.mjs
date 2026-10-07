#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';

const root = process.cwd();
const css = fs.readFileSync(path.join(root, 'module/webroot/style.css'), 'utf8');
const html = fs.readFileSync(path.join(root, 'module/webroot/index.html'), 'utf8');
const roadmapPath = path.join(root, 'docs/campaign/UIUX_REVAMP_ROADMAP.md');
const implPath = path.join(root, 'docs/implementation/UIUX-UX01.md');
const mode = process.argv[2] || 'all';

function fail(message) {
  console.error(`UIUX-UX01 ${mode}: FAIL: ${message}`);
  process.exit(1);
}
function requireText(text, needle, message = `missing ${needle}`) {
  if (!text.includes(needle)) fail(message);
}
function requireRegex(text, pattern, message) {
  if (!pattern.test(text)) fail(message);
}

function tokens() {
  for (const token of [
    '--ui-bg:', '--ui-surface-1:', '--ui-line:', '--ui-text:', '--ui-muted:', '--ui-accent:',
    '--ui-space-1:', '--ui-space-4:', '--ui-radius-control:', '--ui-radius-card:',
    '--ui-touch-min: 44px;', '--ui-content-max:', '--ui-safe-top:', '--ui-safe-bottom:'
  ]) requireText(css, token);
  requireText(css, 'min-height: var(--ui-touch-min);', 'controls do not consume the canonical minimum touch target');
  requireText(css, 'border-radius: var(--ui-radius-card);', 'card radius token is not consumed');
  requireText(css, 'width: min(var(--ui-content-max), 100%);', 'content-width token is not consumed');
}

function shell() {
  requireText(html, 'viewport-fit=cover', 'viewport-fit=cover must remain enabled');
  requireText(css, 'min-height: 100dvh;', 'dynamic viewport height is missing');
  requireText(css, 'overflow-x: hidden;', 'root horizontal overflow containment is missing');
  for (const inset of ['safe-area-inset-top', 'safe-area-inset-right', 'safe-area-inset-bottom', 'safe-area-inset-left']) {
    requireText(css, inset, `missing ${inset}`);
  }
  requireRegex(css, /\.topbar\s*\{[\s\S]*?padding-top:\s*calc\([^}]*--ui-safe-top/s, 'topbar does not consume safe top inset');
  requireRegex(css, /main\s*\{[\s\S]*?--ui-safe-left[\s\S]*?--ui-safe-right[\s\S]*?--ui-safe-bottom/s, 'main does not consume horizontal/bottom safe insets');
  requireText(css, 'overflow-wrap: anywhere;', 'long-value wrapping contract is missing');
  requireText(css, 'word-break: break-word;', 'long-value break fallback is missing');
}

function responsive() {
  requireText(css, '@media (max-width: 759px)', 'mobile shell must include the observed 691px portrait viewport');
  requireRegex(css, /@media \(max-width: 759px\)[\s\S]*?\.topbar\s*\{[\s\S]*?position:\s*static/s, 'mobile topbar must leave sticky desktop geometry');
  requireRegex(css, /@media \(max-width: 759px\)[\s\S]*?\.tabs\s*\{[\s\S]*?overflow-x:\s*auto/s, 'legacy tabs must remain safely scrollable until UX02 replaces them');
  requireRegex(css, /@media \(max-width: 759px\)[\s\S]*?\.form-grid\s*\{\s*grid-template-columns:\s*1fr/s, 'forms do not collapse to one column');
  requireRegex(css, /@media \(max-width: 759px\)[\s\S]*?\.runtime-grid,\s*\.mount-meta\s*\{\s*grid-template-columns:\s*1fr/s, 'technical metadata does not collapse to one column');
  requireRegex(css, /@media \(max-width: 759px\)[\s\S]*?\.detail-row\s*\{\s*display:\s*grid;\s*grid-template-columns:\s*minmax\(0,1fr\)/s, 'detail rows do not get a narrow single-column fallback');
}

function roadmap() {
  if (!fs.existsSync(roadmapPath) || !fs.existsSync(implPath)) fail('campaign/implementation evidence documents are missing');
  const roadmapText = fs.readFileSync(roadmapPath, 'utf8');
  for (const id of ['UX01','UX02','MG1','UX03','UX04','UX05','UX06','MG2','UX07','UX08','UX09','MG3','UX10','UX11','UX12','MG4','FG1','FS1']) {
    requireText(roadmapText, `| ${id} |`, `roadmap is missing ${id}`);
  }
  requireRegex(roadmapText, /\| UX01 \|[^\n]*\| ACTIVE \|/, 'UX01 must be the active campaign position');
}

const checks = { tokens, shell, responsive, roadmap };
if (mode === 'all') Object.values(checks).forEach(fn => fn());
else if (checks[mode]) checks[mode]();
else fail(`unknown check mode ${mode}`);
console.log(`UIUX-UX01 ${mode}: PASS`);
