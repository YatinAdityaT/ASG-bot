const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

test('background protects credentials and retains transient failures for alarm retry', async () => {
  const harness = await createHarness([500, 202]);
  const rec = meetEvent('participant-1');

  await harness.sendEvent(rec);

  assert.equal(harness.accessLevels.length, 1);
  assert.equal(harness.accessLevels[0].accessLevel, 'TRUSTED_CONTEXTS');
  assert.equal(harness.localData.botAPIOutbox.length, 1);
  assert.equal(harness.localData.botAPIOutbox[0].attempts, 1);

  harness.advance(6_000);
  harness.localData.botAPIOutbox[0].nextAttemptAt = 0;
  harness.listeners.alarm({ name: 'bot-api-outbox' });
  await waitFor(() => harness.localData.botAPIOutbox.length === 0);
  assert.deepEqual(harness.fetchStatuses, []);
});

test('permanent client errors do not block later events', async () => {
  const fetched = [];
  const harness = await createHarness([], async (_url, options) => {
    const id = JSON.parse(options.body).id;
    fetched.push(id);
    return { ok: id !== 'bad', status: id === 'bad' ? 400 : 202 };
  });

  await harness.sendEvent(meetEvent('bad'));
  harness.advance(3_000);
  await harness.sendEvent(meetEvent('good'));
  await waitFor(() => harness.localData.botAPIOutbox.length === 0);

  assert.deepEqual(fetched, ['bad', 'good']);
});

test('trusted options update API URL and token atomically', async () => {
  const harness = await createHarness([]);
  const config = {
    url: 'https://new.example.com/v1/meet-events',
    token: 'new-private-token',
    generation: 'generation-2'
  };

  const response = await harness.updateConfig(config);

  assert.equal(harness.localData.botAPIConfig.url, config.url);
  assert.equal(harness.localData.botAPIConfig.token, config.token);
  assert.equal(harness.localData.botAPIConfig.generation, config.generation);
  assert.equal(response.saved, true);
});

test('changing API endpoint clears records bound to the previous generation', async () => {
  const harness = await createHarness([]);
  harness.localData.botAPIOutbox = [{
    key: 'old',
    rec: meetEvent('old'),
    generation: 'generation-1',
    attempts: 0,
    nextAttemptAt: 0
  }];

  await harness.updateConfig({
    url: 'https://different.example.com/v1/meet-events',
    token: 'new-private-token',
    generation: 'generation-2'
  });

  assert.equal(harness.localData.botAPIOutbox.length, 0);
});

test('batch progress is persisted after each accepted request', async () => {
  let fetchCount = 0;
  let secondStarted = false;
  const harness = await createHarness([], async () => {
    fetchCount += 1;
    if (fetchCount === 1) return { ok: true, status: 202 };
    secondStarted = true;
    return new Promise(() => {});
  });
  harness.localData.botAPIOutbox = ['one', 'two'].map((id, index) => ({
    key: id,
    rec: meetEvent(id),
    generation: 'generation-1',
    attempts: 0,
    nextAttemptAt: index
  }));

  harness.listeners.alarm({ name: 'bot-api-outbox' });
  await waitFor(() => secondStarted);

  assert.equal(harness.localData.botAPIOutbox.length, 1);
  assert.equal(harness.localData.botAPIOutbox[0].key, 'two');
});

async function createHarness(fetchStatuses, fetchOverride) {
  const listeners = {};
  const syncData = {
    webhook: '',
    botAPIURL: 'https://bot.example.com/v1/meet-events',
    consentBanner: true
  };
  const localData = {
    botAPIToken: 'private-token',
    botAPIConfig: {
      url: 'https://bot.example.com/v1/meet-events',
      token: 'private-token',
      generation: 'generation-1'
    },
    botAPIOutbox: []
  };
  const accessLevels = [];
  let now = 1_000_000;

  const clone = value => JSON.parse(JSON.stringify(value));
  const storageArea = data => ({
    async get(defaults) { return clone({ ...defaults, ...data }); },
    async set(values) { Object.assign(data, clone(values)); }
  });
  const sandbox = {
    URL,
    AbortController,
    Date: { now: () => now },
    Promise,
    setTimeout(fn, delay) {
      now += delay;
      Promise.resolve().then(fn);
      return 1;
    },
    clearTimeout() {},
    console: { warn() {} },
    importScripts() {
      sandbox.MMDelivery = require('../src/delivery.js');
    },
    fetch: fetchOverride || (async () => {
      const status = fetchStatuses.shift();
      return { ok: status >= 200 && status < 300, status };
    }),
    chrome: {
      storage: {
        sync: storageArea(syncData),
        local: {
          ...storageArea(localData),
          async setAccessLevel(options) { accessLevels.push(options); }
        }
      },
      runtime: {
        id: 'extension-id',
        getURL: value => `chrome-extension://extension-id/${value}`,
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

  return {
    accessLevels,
    fetchStatuses,
    listeners,
    localData,
    advance(milliseconds) { now += milliseconds; },
    sendEvent(rec) {
      return new Promise(resolve => {
        assert.equal(listeners.message({ kind: 'event', rec }, { tab: {} }, resolve), true);
      });
    },
    updateConfig(config) {
      return new Promise(resolve => {
        const sender = {
          id: 'extension-id',
          tab: { id: 99 },
          url: 'chrome-extension://extension-id/src/options.html'
        };
        assert.equal(
          listeners.message({ kind: 'setBotAPIConfig', config }, sender, resolve),
          true
        );
      });
    }
  };
}

function meetEvent(id) {
  return {
    type: 'join',
    id,
    meeting: 'abc-defg-hij',
    ts: `2026-10-27T08:00:0${id === 'good' ? '2' : '1'}Z`,
    name: 'Alex'
  };
}

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 5));
  }
  throw new Error('condition was not reached');
}
