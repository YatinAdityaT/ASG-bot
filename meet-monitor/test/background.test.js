const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

test('background protects credentials and retains failed events for alarm retry', async () => {
  const listeners = {};
  const syncData = {
    webhook: '',
    botAPIURL: 'https://bot.example.com/v1/meet-events',
    consentBanner: true
  };
  const localData = { botAPIToken: 'private-token', botAPIOutbox: [] };
  const accessLevels = [];
  const fetchStatuses = [500, 202];
  let now = 1_000_000;

  const storageArea = data => ({
    async get(defaults) { return { ...defaults, ...data }; },
    async set(values) { Object.assign(data, values); }
  });
  const sandbox = {
    URL,
    Date: { now: () => now },
    Promise,
    console: { warn() {} },
    importScripts() {
      sandbox.MMDelivery = require('../src/delivery.js');
    },
    fetch: async () => {
      const status = fetchStatuses.shift();
      return { ok: status >= 200 && status < 300, status };
    },
    chrome: {
      storage: {
        sync: storageArea(syncData),
        local: {
          ...storageArea(localData),
          async setAccessLevel(options) { accessLevels.push(options); }
        }
      },
      runtime: {
        onInstalled: { addListener(fn) { listeners.installed = fn; } },
        onMessage: { addListener(fn) { listeners.message = fn; } },
        onStartup: { addListener(fn) { listeners.startup = fn; } }
      },
      alarms: {
        create() {},
        onAlarm: { addListener(fn) { listeners.alarm = fn; } }
      },
      notifications: { create: async () => {} }
    }
  };

  const source = fs.readFileSync(
    path.join(__dirname, '..', 'src', 'background.js'),
    'utf8'
  );
  vm.runInNewContext(source, sandbox, { filename: 'background.js' });
  await waitFor(() => accessLevels.length === 1);

  const rec = {
    type: 'join',
    id: 'participant-1',
    meeting: 'abc-defg-hij',
    ts: '2026-10-27T08:00:00Z',
    name: 'Alex'
  };
  await new Promise(resolve => {
    assert.equal(listeners.message({ kind: 'event', rec }, null, resolve), true);
  });

  assert.equal(accessLevels.length, 1);
  assert.equal(accessLevels[0].accessLevel, 'TRUSTED_CONTEXTS');
  assert.equal(localData.botAPIOutbox.length, 1);
  assert.equal(localData.botAPIOutbox[0].attempts, 1);

  now += 3_000;
  localData.botAPIOutbox[0].nextAttemptAt = 0;
  listeners.alarm({ name: 'bot-api-outbox' });
  await waitFor(() => localData.botAPIOutbox.length === 0);
  assert.deepEqual(fetchStatuses, []);
});

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 5));
  }
  throw new Error('condition was not reached');
}

