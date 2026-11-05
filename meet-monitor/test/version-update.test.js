const test = require('node:test');
const assert = require('node:assert/strict');

const update = require('../src/version-update.js');

test('detects only a newer numeric Chrome extension version', () => {
  assert.equal(update.isNewer('1.8.0', '1.9.0'), true);
  assert.equal(update.isNewer('1.9.0', '1.9.0'), false);
  assert.equal(update.isNewer('2.0.0', '1.9.9'), false);
  assert.equal(update.isNewer('1.9', '1.9.0.1'), true);
  assert.equal(update.isNewer('1.9.0', 'invalid'), false);
});

test('uses the master branch manifest only as a version signal', () => {
  assert.equal(
    update.manifestURL,
    'https://raw.githubusercontent.com/YatinAdityaT/ASG-bot/master/meet-monitor/manifest.json'
  );
});
