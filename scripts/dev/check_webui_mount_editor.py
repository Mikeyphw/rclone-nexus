#!/usr/bin/env python3
from pathlib import Path

html = Path('module/webroot/index.html').read_text(encoding='utf-8')
app = Path('module/webroot/app.js').read_text(encoding='utf-8')
model = Path('module/webroot/model.js').read_text(encoding='utf-8')
css = Path('module/webroot/style.css').read_text(encoding='utf-8')
validation = Path('internal/mounts/validation.go').read_text(encoding='utf-8')
protocol = Path('internal/protocol/types.go').read_text(encoding='utf-8')
engine = Path('internal/control/engine.go').read_text(encoding='utf-8')

html_tokens = [
    'mountRemoteSelect', 'mountRemoteBrowser', 'testMountRemoteButton', 'profileCards',
    'profileRecommendation', 'editorIssueSummary', 'previewCountdown', 'operationProgress',
    'discardBackdrop', 'Review mount', 'Make visible to Android apps', 'Start automatically',
    'data-mountpoint-template', 'networkPolicyHelp', 'providerReadiness', 'previewExpiryHelp', 'reviewSafety',
]
app_tokens = [
    "query('config.validate'", "query('provider.remotes'", "query('provider.browse'", "query('vfs.profiles'",
    'renderEditorIssues', 'validateEditorNow', 'monitorOperation', 'preview_expires_unix_ms',
    'editorDirty', 'operation.status', 'trapModalTab', 'applyMountpointSuggestion', 'showMountOutcome',
    'renderProviderReadiness', 'editorSession', 'editorProfileTouched', 'editorBackendValidated', "query('provider.status'",
    "label:'Retry start'", "label:'Edit mount'", "label:'View logs'", 'profile-managed',
]
model_tokens = ['localMountIssues', 'parseRemoteEndpoint', 'suggestMountDefaults', 'profilePresentation']
css_tokens = ['profile-grid', 'issue-summary', 'sticky-actions', 'remote-folder-grid', 'progress-steps', 'field-invalid', 'mountpoint-suggestions', 'profile-managed']

for token in html_tokens:
    if token not in html:
        raise SystemExit(f'guided mount editor HTML missing {token}')
for token in app_tokens:
    if token not in app:
        raise SystemExit(f'guided mount editor logic missing {token}')
for token in model_tokens:
    if token not in model:
        raise SystemExit(f'guided mount editor model missing {token}')
for token in css_tokens:
    if token not in css:
        raise SystemExit(f'guided mount editor styling missing {token}')
if '<form id="mountForm" novalidate' in html:
    raise SystemExit('mount editor must retain native form validation')
for forbidden in ['innerHTML', 'insertAdjacentHTML', 'localStorage', 'sessionStorage', 'indexedDB', 'eval(', 'new Function']:
    if forbidden in app or forbidden in html:
        raise SystemExit(f'forbidden browser primitive in mount editor: {forbidden}')

for source, tokens in [
    (validation, ['Mount        string `json:"mount,omitempty"`', 'items[j].Mount = configs[i].Name']),
    (protocol, ['Mount        string `json:"mount,omitempty"`', 'issue.Mount = redact.BoundedString']),
    (engine, ['Mount: item.Mount', 'Mount: cfg.Name']),
]:
    for token in tokens:
        if token not in source:
            raise SystemExit(f'structured mount attribution missing {token}')
if "if(!state.editorDirty)state.editorInitial=serializeEditor()" not in app:
    raise SystemExit('async editor helper completion can overwrite dirty baseline')
if "node.disabled=!custom" not in app or "field-${field}`).value=value" not in app:
    raise SystemExit('profile-managed raw VFS controls are not synchronized/disabled')
if "Outside Nexus/provider state" not in app or "No mount overlap" not in app:
    raise SystemExit('destination backend validation chips are incomplete')
print('WebUI guided mount editor static contract PASS')
