const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const ROOT = path.join(__dirname, '..');

test('toolbar action opens the side panel instead of a popup', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(ROOT, 'manifest.json'), 'utf8'));
  assert.equal(manifest.action.default_popup, undefined);
  assert.ok(manifest.permissions.includes('sidePanel'));
  assert.ok(manifest.permissions.includes('tabs'));
});

test('options page exposes comma-separated monitored Meet links', () => {
  const html = fs.readFileSync(path.join(ROOT, 'src', 'options.html'), 'utf8');
  assert.match(html, /id="monitoredMeetLinks"/);
  assert.match(html, /comma-separated/i);
});
