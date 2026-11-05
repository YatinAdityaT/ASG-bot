const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const ROOT = path.join(__dirname, '..');

test('toolbar action restores the Meet Monitor popup', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(ROOT, 'manifest.json'), 'utf8'));
  assert.equal(manifest.action.default_popup, 'src/popup.html');
  assert.ok(manifest.permissions.includes('sidePanel'));
  assert.ok(manifest.permissions.includes('tabs'));
});

test('options page exposes comma-separated monitored Meet codes', () => {
  const html = fs.readFileSync(path.join(ROOT, 'src', 'options.html'), 'utf8');
  assert.match(html, /id="monitoredMeetLinks"/);
  assert.match(html, /comma-separated Google Meet codes/i);
  assert.match(html, /placeholder="jpy-ymgz-tdo"/);
});

test('side panel controls per-tab monitoring, reload, and identity probing', () => {
  const source = fs.readFileSync(path.join(ROOT, 'src', 'side-panel.js'), 'utf8');
  assert.match(source, /kind:\s*'setTabMonitoring'/);
  assert.match(source, /chrome\.runtime\.reload\(\)/);
  assert.match(source, /kind:\s*'getIdentityProbe'/);
});
