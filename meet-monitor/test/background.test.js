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

test('alarm flush preserves queued entries without fetching while sending is disabled', async () => {
  const harness = await createHarness([]);
  harness.syncData.sendingEnabled = false;
  harness.localData.botAPIOutbox = [outboxEntry('alarm')];

  harness.listeners.alarm({ name: 'bot-api-outbox' });
  await settle();

  assert.equal(harness.fetchRequests.length, 0);
  assert.equal(harness.localData.botAPIOutbox.length, 1);
});

test('startup flush preserves queued entries without fetching while sending is disabled', async () => {
  const harness = await createHarness([]);
  harness.syncData.sendingEnabled = false;
  harness.localData.botAPIOutbox = [outboxEntry('startup')];

  harness.listeners.startup();
  await settle();

  assert.equal(harness.fetchRequests.length, 0);
  assert.equal(harness.localData.botAPIOutbox.length, 1);
});

test('configuration flush preserves queued entries without fetching while sending is disabled', async () => {
  const harness = await createHarness([]);
  harness.syncData.sendingEnabled = false;
  harness.localData.botAPIOutbox = [outboxEntry('configuration')];

  await harness.updateConfig({
    url: 'https://bot.example.com/v1/meet-events',
    token: 'private-token',
    generation: 'generation-2'
  });
  await settle();

  assert.equal(harness.fetchRequests.length, 0);
  assert.equal(harness.localData.botAPIOutbox.length, 1);
});

test('initial flush preserves queued entries without fetching while sending is disabled', async () => {
  const harness = await createHarness([], null, {
    syncData: { sendingEnabled: false },
    localData: { botAPIOutbox: [outboxEntry('initialization')] }
  });
  await settle();

  assert.equal(harness.fetchRequests.length, 0);
  assert.equal(harness.localData.botAPIOutbox.length, 1);
});

test('flush permanently removes persisted entries from disallowed meetings', async () => {
  const harness = await createHarness([]);
  harness.localData.botAPIOutbox = [{
    ...outboxEntry('disallowed'),
    rec: {
      ...meetEvent('disallowed'),
      meeting: 'abc-defg-hij'
    }
  }];

  harness.listeners.alarm({ name: 'bot-api-outbox' });
  await waitFor(() => harness.localData.botAPIOutbox.length === 0);

  assert.equal(harness.fetchRequests.length, 0);
});

test('re-enabling sending flushes the preserved outbox', async () => {
  const harness = await createHarness([202]);
  harness.syncData.sendingEnabled = false;
  harness.localData.botAPIOutbox = [outboxEntry('reenabled')];

  harness.setSendingEnabled(true);
  await waitFor(() => harness.localData.botAPIOutbox.length === 0);

  assert.equal(harness.fetchRequests.length, 1);
});

test('equivalent events from separate contexts forward once within five seconds', async () => {
  const harness = await createHarness([], async () => ({ ok: true, status: 202 }));
  harness.syncData.webhook = 'https://webhook.example.com/events';
  const first = {
    ...meetEvent('participant-1'),
    type: 'chat_message',
    text: 'Hello'
  };

  await harness.sendEvent(first);
  harness.advance(1_000);
  const response = await harness.sendEvent({
    ...first,
    ts: '2026-10-27T08:00:02Z'
  });

  assert.equal(response.queued, false);
  assert.equal(
    harness.fetchRequests.filter(request =>
      request.url === 'https://bot.example.com/v1/meet-events'
    ).length,
    1
  );
  assert.equal(
    harness.fetchRequests.filter(request =>
      request.url === 'https://webhook.example.com/events'
    ).length,
    1
  );
});

test('a stalled webhook does not block bot handling or event acknowledgement', async () => {
  const harness = await createHarness([], async url => {
    if (url === 'https://webhook.example.com/events') {
      return new Promise(() => {});
    }
    return { ok: true, status: 202 };
  });
  harness.syncData.webhook = 'https://webhook.example.com/events';

  const response = await Promise.race([
    harness.sendEvent(meetEvent('stalled-webhook')),
    new Promise(resolve => setTimeout(() => resolve('timeout'), 50))
  ]);

  assert.notEqual(response, 'timeout');
  assert.equal(response.queued, true);
  assert.equal(harness.localData.botAPIOutbox.length, 0);
});

test('disabling during webhook lookup prevents the webhook fetch', async () => {
  const lookupStarted = deferred();
  const releaseLookup = deferred();
  const harness = await createHarness([], async () => ({ ok: true, status: 202 }), {
    async beforeSyncGet(defaults) {
      if (Object.hasOwn(defaults, 'webhook')) {
        lookupStarted.resolve();
        await releaseLookup.promise;
      }
    }
  });
  harness.syncData.webhook = 'https://webhook.example.com/events';

  const responsePromise = harness.sendEvent(meetEvent('webhook-race'));
  await lookupStarted.promise;
  harness.setSendingEnabled(false);
  releaseLookup.resolve();
  await responsePromise;
  await settle();

  assert.equal(
    harness.fetchRequests.filter(request =>
      request.url === 'https://webhook.example.com/events'
    ).length,
    0
  );
});

test('disabling during serialized enqueue prevents outbox insertion', async () => {
  const operationStarted = deferred();
  const releaseOperation = deferred();
  const harness = await createHarness([], null, {
    async beforeLocalGet(defaults) {
      if (Object.hasOwn(defaults, 'botAPIConfig')) {
        operationStarted.resolve();
        await releaseOperation.promise;
      }
    }
  });

  const responsePromise = harness.sendEvent(meetEvent('enqueue-race'));
  await operationStarted.promise;
  harness.setSendingEnabled(false);
  releaseOperation.resolve();
  const response = await responsePromise;

  assert.equal(response.queued, false);
  assert.equal(harness.localData.botAPIOutbox.length, 0);
});

test('events from another meeting reach neither outbound path', async () => {
  const harness = await createHarness([]);
  harness.syncData.webhook = 'https://webhook.example.com/events';

  const response = await harness.sendEvent({
    ...meetEvent('participant-1'),
    meeting: 'abc-defg-hij'
  });

  assert.equal(response.queued, false);
  assert.equal(harness.fetchRequests.length, 0);
  assert.equal(harness.localData.botAPIOutbox.length, 0);
});

test('disabled sending reaches neither outbound path', async () => {
  const harness = await createHarness([]);
  harness.syncData.webhook = 'https://webhook.example.com/events';
  harness.syncData.sendingEnabled = false;

  const response = await harness.sendEvent(meetEvent('participant-1'));

  assert.equal(response.queued, false);
  assert.equal(harness.fetchRequests.length, 0);
  assert.equal(harness.localData.botAPIOutbox.length, 0);
});

test('sending defaults to enabled when the extension is installed', async () => {
  const harness = await createHarness([]);
  delete harness.syncData.sendingEnabled;

  await harness.listeners.installed();

  assert.equal(harness.syncData.sendingEnabled, true);
});

test('chat events create no browser notifications', async () => {
  const harness = await createHarness([202]);

  await harness.sendEvent({
    ...meetEvent('participant-1'),
    type: 'chat_message',
    text: 'Hello'
  });

  assert.equal(harness.notificationCalls.length, 0);
});

test('popup contains a labeled sending control with ON/OFF status', () => {
  const html = fs.readFileSync(
    path.join(__dirname, '..', 'src', 'popup.html'),
    'utf8'
  );

  assert.match(html, /<label id="sending-control">/);
  assert.match(html, /<input type="checkbox" id="sending"> Sending/);
  assert.match(html, /<strong id="sending-state">ON<\/strong>/);
});

test('popup loads and persists the sending toggle', async () => {
  const harness = createPopupHarness(false);

  await waitFor(() => harness.elements['#sending-state'].textContent === 'OFF');
  assert.equal(harness.elements['#sending'].checked, false);

  harness.elements['#sending'].checked = true;
  await harness.elements['#sending'].onchange();

  assert.equal(harness.syncWrites.length, 1);
  assert.equal(harness.syncWrites[0].sendingEnabled, true);
  assert.equal(harness.elements['#sending-state'].textContent, 'ON');
});

async function createHarness(fetchStatuses, fetchOverride, initial = {}) {
  const listeners = {};
  const syncData = {
    webhook: '',
    botAPIURL: 'https://bot.example.com/v1/meet-events',
    consentBanner: true,
    sendingEnabled: true
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
  Object.assign(syncData, initial.syncData);
  Object.assign(localData, initial.localData);
  const accessLevels = [];
  const fetchRequests = [];
  const notificationCalls = [];
  let now = 1_000_000;

  const clone = value => JSON.parse(JSON.stringify(value));
  const storageArea = (data, beforeGet) => ({
    async get(defaults) {
      if (beforeGet) await beforeGet(defaults);
      return clone({ ...defaults, ...data });
    },
    async set(values) { Object.assign(data, clone(values)); }
  });
  const sandbox = {
    URL,
    AbortController,
    Date: { now: () => now },
    Promise,
    setTimeout(fn, delay) {
      if (delay === 15_000) return 1;
      now += delay;
      Promise.resolve().then(fn);
      return 1;
    },
    clearTimeout() {},
    console: { warn() {} },
    importScripts() {
      sandbox.MMDelivery = require('../src/delivery.js');
    },
    async fetch(url, options) {
      fetchRequests.push({ url, options });
      if (fetchOverride) return fetchOverride(url, options);
      const status = fetchStatuses.shift();
      return { ok: status >= 200 && status < 300, status };
    },
    chrome: {
      storage: {
        sync: storageArea(syncData, initial.beforeSyncGet),
        local: {
          ...storageArea(localData, initial.beforeLocalGet),
          async setAccessLevel(options) { accessLevels.push(options); }
        },
        onChanged: { addListener(fn) { listeners.storageChanged = fn; } }
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
      notifications: {
        async create(options) {
          notificationCalls.push(options);
        }
      }
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
    fetchRequests,
    fetchStatuses,
    listeners,
    localData,
    notificationCalls,
    syncData,
    advance(milliseconds) { now += milliseconds; },
    setSendingEnabled(enabled) {
      const oldValue = syncData.sendingEnabled;
      syncData.sendingEnabled = enabled;
      listeners.storageChanged({
        sendingEnabled: { oldValue, newValue: enabled }
      }, 'sync');
    },
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

function outboxEntry(id) {
  return {
    key: id,
    rec: meetEvent(id),
    generation: 'generation-1',
    attempts: 0,
    nextAttemptAt: 0
  };
}

function createPopupHarness(sendingEnabled) {
  const elements = Object.fromEntries(
    [
      '#meeting',
      '#count',
      '#roster',
      '#self',
      '#feed',
      '#csv',
      '#opts',
      '#sending',
      '#sending-state'
    ].map(selector => [selector, {
      checked: false,
      innerHTML: '',
      textContent: '',
      onclick: null,
      onchange: null
    }])
  );
  const syncWrites = [];
  const sandbox = {
    Blob,
    Date,
    MMCSV: { row: values => values.join(',') },
    Promise,
    URL: {
      createObjectURL: () => 'blob:test',
      revokeObjectURL() {}
    },
    chrome: {
      runtime: { openOptionsPage() {} },
      storage: {
        sync: {
          async get(defaults) {
            return { ...defaults, sendingEnabled };
          },
          async set(values) {
            syncWrites.push(values);
          }
        }
      },
      tabs: {
        async query() {
          return [];
        },
        async sendMessage() {
          return null;
        }
      }
    },
    document: {
      querySelector(selector) {
        return elements[selector];
      },
      createElement() {
        return { click() {} };
      }
    },
    setInterval() {},
    setTimeout(fn) {
      fn();
    }
  };
  const source = fs.readFileSync(
    path.join(__dirname, '..', 'src', 'popup.js'),
    'utf8'
  );
  vm.runInNewContext(source, sandbox, { filename: 'popup.js' });
  return { elements, syncWrites };
}

function meetEvent(id) {
  return {
    type: 'join',
    id,
    meeting: 'jpy-ymgz-tdo',
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

async function settle() {
  for (let turn = 0; turn < 10; turn++) await Promise.resolve();
}

function deferred() {
  let resolve;
  const promise = new Promise(done => {
    resolve = done;
  });
  return { promise, resolve };
}
