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

  assert.equal(harness.eventRequests().length, 0);
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

  assert.equal(harness.eventRequests().length, 0);
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

  assert.equal(harness.eventRequests().length, 1);
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

test('first observation elects one source and sends sequence one baseline', async () => {
  const harness = await createHarness([202], null, {
    sessionData: { monitorSession: null }
  });

  const response = await harness.sendObservation(7, roster('Alex'));
  await waitFor(() => harness.stateRequests().length === 1);

  assert.equal(response.elected, true);
  assert.equal(harness.sessionData.monitorSession.sessionId, 'session-1');
  assert.equal(harness.sessionData.monitorSession.sourceTabId, 7);
  assert.equal(harness.stateBodies()[0].sequence, 1);
  assert.equal(harness.stateBodies()[0].participants[0].name, 'Alex');
});

test('unchanged observations do not send until the heartbeat', async () => {
  const harness = await createHarness([202, 202], null, {
    sessionData: { monitorSession: null }
  });

  await harness.sendObservation(7, roster('Alex'));
  await harness.sendObservation(7, roster('Alex'));
  assert.equal(harness.stateRequests().length, 1);

  harness.listeners.alarm({ name: 'meet-state-heartbeat' });
  await waitFor(() => harness.stateRequests().length === 2);
  assert.equal(harness.stateBodies()[1].sequence, 2);
});

test('confirmed roster changes send immediately while a fresh second tab is ignored', async () => {
  const harness = await createHarness([202, 202], null, {
    sessionData: { monitorSession: null }
  });

  await harness.sendObservation(7, roster('Alex'));
  const rejected = await harness.sendObservation(8, roster('Ignored'));
  await harness.sendObservation(7, roster('Taylor'));

  assert.equal(rejected.elected, false);
  assert.equal(harness.stateRequests().length, 2);
  assert.equal(harness.stateBodies()[1].participants[0].name, 'Taylor');
});

test('source takeover requires more than 45 seconds and starts a new session', async () => {
  const harness = await createHarness([202, 202], null, {
    sessionData: { monitorSession: null }
  });

  await harness.sendObservation(7, roster('Alex'));
  harness.advance(45_000);
  assert.equal((await harness.sendObservation(8, roster('No'))).elected, false);
  harness.advance(1);
  assert.equal((await harness.sendObservation(8, roster('Taylor'))).elected, true);

  assert.equal(harness.sessionData.monitorSession.sessionId, 'session-2');
  assert.equal(harness.stateBodies().at(-1).sequence, 1);
});

test('transient state retry preserves body and sequence', async () => {
  const harness = await createHarness([500, 202], null, {
    sessionData: { monitorSession: null }
  });

  await harness.sendObservation(7, roster('Alex'));
  const firstBody = harness.stateRequests()[0].options.body;
  harness.sessionData.pendingMeetSnapshot.nextAttemptAt = 0;
  harness.listeners.alarm({ name: 'meet-state-retry' });
  await waitFor(() => harness.stateRequests().length === 2);

  assert.equal(harness.stateRequests()[1].options.body, firstBody);
  assert.equal(harness.stateBodies()[1].sequence, 1);
  assert.equal(harness.sessionData.pendingMeetSnapshot, undefined);
});

test('new semantic state supersedes an unsent retry with a higher sequence', async () => {
  const harness = await createHarness([500, 202], null, {
    sessionData: { monitorSession: null }
  });

  await harness.sendObservation(7, roster('Alex'));
  await harness.sendObservation(7, roster('Taylor'));

  assert.deepEqual(harness.stateBodies().map(body => body.sequence), [1, 2]);
  assert.equal(harness.stateBodies()[1].participants[0].name, 'Taylor');
});

test('sending off still sends disabled snapshots and blocks chat and webhook', async () => {
  const harness = await createHarness([202], null, {
    sessionData: { monitorSession: null },
    syncData: { sendingEnabled: false, webhook: 'https://webhook.example.com/events' }
  });

  await harness.sendObservation(7, roster('Alex'));
  const event = await harness.sendEvent({
    ...meetEvent('chat-1'),
    text: 'private text'
  }, 7);

  assert.equal(harness.stateBodies()[0].forwardingEnabled, false);
  assert.equal(event.queued, false);
  assert.equal(harness.eventRequests().length, 0);
  assert.equal(harness.webhookRequests().length, 0);
});

test('only elected chat records enter the event API', async () => {
  const harness = await createHarness([202, 202], null, {
    sessionData: { monitorSession: null }
  });
  harness.syncData.webhook = '';
  await harness.sendObservation(7, roster('Alex'));

  await harness.sendEvent({ ...meetEvent('join-1'), type: 'join' }, 7);
  await harness.sendEvent(meetEvent('wrong-tab'), 8);
  await harness.sendEvent(meetEvent('chat-1'), 7);

  assert.equal(harness.eventRequests().length, 1);
  assert.equal(JSON.parse(harness.eventRequests()[0].options.body).id, 'chat-1');
});

test('stale sources produce no heartbeat traffic', async () => {
  const harness = await createHarness([202], null, {
    sessionData: { monitorSession: null }
  });
  await harness.sendObservation(7, roster('Alex'));

  harness.advance(45_001);
  harness.listeners.alarm({ name: 'meet-state-heartbeat' });
  await settle();

  assert.equal(harness.stateRequests().length, 1);
});

test('state client errors are permanent and use safe categories', async () => {
  const cases = new Map([
    [400, 'invalid_snapshot'],
    [401, 'unauthorized'],
    [403, 'origin_forbidden'],
    [409, 'sequence_conflict']
  ]);

  for (const [status, category] of cases) {
    const harness = await createHarness([status], null, {
      sessionData: { monitorSession: null }
    });
    await harness.sendObservation(7, roster('Alex'));

    assert.equal(harness.sessionData.pendingMeetSnapshot, undefined);
    assert.equal(
      harness.sessionData.monitorSession.lastServerError.category,
      category
    );
  }
});

test('off to on queues an immediate enabled snapshot', async () => {
  const harness = await createHarness([202, 202], null, {
    sessionData: { monitorSession: null },
    syncData: { sendingEnabled: false }
  });
  await harness.sendObservation(7, roster('Alex'));

  harness.setSendingEnabled(true);
  await waitFor(() => harness.stateRequests().length === 2);

  assert.equal(harness.stateBodies()[1].forwardingEnabled, true);
  assert.equal(harness.stateBodies()[1].sequence, 2);
});

test('service worker initialization restores and retries matching pending state', async () => {
  const body = snapshotBody('restored-session', 4, roster('Alex'));
  const harness = await createHarness([202], null, {
    sessionData: {
      monitorSession: {
        sessionId: 'restored-session',
        sourceTabId: 1,
        sequence: 4,
        meeting: 'jpy-ymgz-tdo',
        lastObservedAt: 1_000_000,
        roster: roster('Alex'),
        self: {},
        lastStateKey: '',
        acceptedSequence: 0,
        lastServerResult: null,
        lastServerSuccessAt: null,
        lastServerError: null
      },
      pendingMeetSnapshot: {
        sessionId: 'restored-session',
        sequence: 4,
        stateKey: require('../src/snapshot.js').stateKey(body),
        body,
        generation: 'generation-1',
        attempts: 1,
        nextAttemptAt: 0
      }
    }
  });

  await waitFor(() => harness.stateRequests().length === 1);
  assert.equal(harness.stateBodies()[0].sequence, 4);
  assert.equal(harness.sessionData.monitorSession.acceptedSequence, 4);
});

test('clearing configuration removes pending state', async () => {
  const harness = await createHarness([500], null, {
    sessionData: { monitorSession: null }
  });
  await harness.sendObservation(7, roster('Alex'));
  assert.ok(harness.sessionData.pendingMeetSnapshot);

  await harness.updateConfig({ url: '', token: '', generation: '' });

  assert.equal(harness.sessionData.pendingMeetSnapshot, undefined);
  assert.equal(harness.localData.botAPIOutbox.length, 0);
});

test('initialization permanently removes persisted non-chat outbox entries', async () => {
  const harness = await createHarness([], null, {
    localData: {
      botAPIOutbox: [{
        ...outboxEntry('old-participant'),
        rec: { ...meetEvent('old-participant'), type: 'join' }
      }]
    }
  });

  await waitFor(() => harness.localData.botAPIOutbox.length === 0);
  assert.equal(harness.eventRequests().length, 0);
});

test('monitor model persists confirmed history and bounds it to 1000 entries', async () => {
  const existing = Array.from({ length: 1000 }, (_, index) => ({
    ts: `2026-11-03T12:00:${String(index % 60).padStart(2, '0')}Z`,
    type: 'join',
    id: `old-${index}`,
    name: `Person ${index}`,
    meeting: 'jpy-ymgz-tdo',
    sequence: index,
    deliveryState: 'accepted'
  }));
  const harness = await createHarness([202], null, {
    sessionData: { monitorSession: null },
    localData: { monitorHistory: existing }
  });

  await harness.sendObservation(7, roster('Alex'), {
    changes: [{ ...meetEvent('new-participant'), type: 'join' }],
    rawRoster: roster('Raw Alex')
  });
  const model = await harness.sendViewMessage({ kind: 'getMonitorModel' });

  assert.equal(harness.localData.monitorHistory.length, 1000);
  assert.equal(harness.localData.monitorHistory[0].id, 'old-1');
  assert.equal(harness.localData.monitorHistory.at(-1).id, 'new-participant');
  assert.equal(harness.localData.monitorHistory.at(-1).deliveryState, 'accepted');
  assert.equal(model.history.length, 1000);
  assert.equal(model.extensionVersion, '2.0.0');
  assert.equal(model.roster.participant.name, 'Alex');
});

test('debug observations are opt-in, session-only, bounded, and absent from requests', async () => {
  const harness = await createHarness([202], null, {
    sessionData: {
      monitorSession: null,
      monitorDebugHistory: Array.from({ length: 250 }, (_, index) => ({
        observedAt: index,
        rawRoster: { [`old-${index}`]: { name: `Raw ${index}` } }
      }))
    }
  });

  await harness.sendViewMessage({ kind: 'setDebugMode', enabled: true });
  await harness.sendObservation(7, roster('Alex'), {
    rawRoster: { secret: { name: 'raw-only-secret' } }
  });
  const model = await harness.sendViewMessage({ kind: 'getMonitorModel' });

  assert.equal(harness.sessionData.monitorDebugHistory.length, 250);
  assert.equal(
    harness.sessionData.monitorDebugHistory.at(-1).rawRoster.secret.name,
    'raw-only-secret'
  );
  assert.equal(model.debugEnabled, true);
  assert.equal(model.debugHistory.length, 250);
  assert.equal(
    harness.fetchRequests.some(request =>
      String(request.options.body).includes('raw-only-secret')
    ),
    false
  );
});

test('monitor controls require a trusted extension view and clear history', async () => {
  const harness = await createHarness([], null, {
    localData: { monitorHistory: [meetEvent('stored')] }
  });

  const rejected = await harness.sendMessage(
    { kind: 'clearMonitorHistory' },
    { tab: { id: 7 }, url: 'https://meet.google.com/jpy-ymgz-tdo' }
  );
  assert.equal(rejected.cleared, false);
  assert.equal(harness.localData.monitorHistory.length, 1);

  const forwarding = await harness.sendViewMessage({
    kind: 'setForwarding',
    enabled: false
  });
  const cleared = await harness.sendViewMessage({ kind: 'clearMonitorHistory' });

  assert.equal(forwarding.saved, true);
  assert.equal(harness.syncData.sendingEnabled, false);
  assert.equal(cleared.cleared, true);
  assert.deepEqual(harness.localData.monitorHistory, []);
  assert.ok(harness.broadcasts.some(message =>
    message.kind === 'monitorModelChanged'
  ));
});

test('popup contains a labeled sending control with ON/OFF status', () => {
  const html = fs.readFileSync(
    path.join(__dirname, '..', 'src', 'popup.html'),
    'utf8'
  );

  assert.match(html, /<label id="sending-control">/);
  assert.match(html, /<input type="checkbox" id="sending"> Sending/);
  assert.match(html, /<strong id="sending-state">ON<\/strong>/);
  assert.match(html, /id="open-monitor"/);
});

test('popup loads and persists the sending toggle', async () => {
  const harness = createPopupHarness(false);

  await waitFor(() => harness.elements['#sending-state'].textContent === 'OFF');
  assert.equal(harness.elements['#sending'].checked, false);

  harness.elements['#sending'].checked = true;
  await harness.elements['#sending'].onchange();

  assert.deepEqual(JSON.parse(JSON.stringify(harness.runtimeMessages.at(-1))), {
    kind: 'setForwarding',
    enabled: true
  });
  assert.equal(harness.elements['#sending-state'].textContent, 'ON');
});

test('popup reads the monitor model and opens the side panel for the active window', async () => {
  const harness = createPopupHarness(true, {
    id: 7,
    windowId: 42,
    url: 'https://meet.google.com/jpy-ymgz-tdo'
  });
  await waitFor(() => harness.elements['#meeting'].textContent === 'jpy-ymgz-tdo');

  await harness.elements['#open-monitor'].onclick();

  assert.deepEqual(
    JSON.parse(JSON.stringify(harness.sidePanelOpens)),
    [{ windowId: 42 }]
  );
  assert.ok(harness.runtimeMessages.some(message =>
    message.kind === 'getMonitorModel'
  ));
});

async function createHarness(fetchStatuses, fetchOverride, initial = {}) {
  const listeners = {};
  let now = 1_000_000;
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
  const sessionData = {
    monitorSession: {
      sessionId: 'restored-session',
      sourceTabId: 1,
      sequence: 0,
      meeting: 'jpy-ymgz-tdo',
      lastObservedAt: now,
      roster: {},
      self: {},
      lastStateKey: '',
      acceptedSequence: 0,
      lastServerResult: null,
      lastServerSuccessAt: null,
      lastServerError: null
    }
  };
  Object.assign(syncData, initial.syncData);
  Object.assign(localData, initial.localData);
  Object.assign(sessionData, initial.sessionData);
  const accessLevels = [];
  const fetchRequests = [];
  const notificationCalls = [];
  const alarmCreates = [];
  const broadcasts = [];
  let uuid = 0;

  const clone = value => JSON.parse(JSON.stringify(value));
  const storageArea = (data, beforeGet) => ({
    async get(defaults) {
      if (beforeGet) await beforeGet(defaults);
      return clone({ ...defaults, ...data });
    },
    async set(values) { Object.assign(data, clone(values)); },
    async remove(keys) {
      for (const key of Array.isArray(keys) ? keys : [keys]) delete data[key];
    }
  });
  const sandbox = {
    URL,
    AbortController,
    Date: class extends Date {
      static now() {
        return now;
      }
    },
    Promise,
    setTimeout(fn, delay) {
      if (delay === 15_000) return 1;
      now += delay;
      Promise.resolve().then(fn);
      return 1;
    },
    clearTimeout() {},
    console: { warn() {} },
    crypto: {
      randomUUID() {
        uuid += 1;
        return `session-${uuid}`;
      }
    },
    importScripts(...scripts) {
      if (scripts.includes('delivery.js')) {
        sandbox.MMDelivery = require('../src/delivery.js');
      }
      if (scripts.includes('snapshot.js')) {
        sandbox.MMSnapshot = require('../src/snapshot.js');
      }
    },
    async fetch(url, options) {
      fetchRequests.push({ url, options });
      if (fetchOverride) return fetchOverride(url, options);
      const status = fetchStatuses.shift();
      return {
        ok: status >= 200 && status < 300,
        status,
        async json() {
          return {
            acceptedSequence: JSON.parse(options.body).sequence,
            serverTime: '2026-11-03T12:00:00Z',
            monitorOnline: true,
            whatsAppConnected: true,
            queuedChanges: 0
          };
        }
      };
    },
    chrome: {
      storage: {
        sync: storageArea(syncData, initial.beforeSyncGet),
        session: storageArea(sessionData, initial.beforeSessionGet),
        local: {
          ...storageArea(localData, initial.beforeLocalGet),
          async setAccessLevel(options) { accessLevels.push(options); }
        },
        onChanged: { addListener(fn) { listeners.storageChanged = fn; } }
      },
      runtime: {
        id: 'extension-id',
        getURL: value => `chrome-extension://extension-id/${value}`,
        getManifest: () => ({ version: '2.0.0' }),
        async sendMessage(message) { broadcasts.push(clone(message)); },
        onInstalled: { addListener(fn) { listeners.installed = fn; } },
        onMessage: { addListener(fn) { listeners.message = fn; } },
        onStartup: { addListener(fn) { listeners.startup = fn; } }
      },
      alarms: {
        create(name, options) { alarmCreates.push({ name, options }); },
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
    sessionData,
    notificationCalls,
    broadcasts,
    syncData,
    alarmCreates,
    advance(milliseconds) { now += milliseconds; },
    stateRequests: () => fetchRequests.filter(request =>
      request.url === 'https://bot.example.com/v1/meet-state'
    ),
    stateBodies() {
      return this.stateRequests().map(request => JSON.parse(request.options.body));
    },
    eventRequests: () => fetchRequests.filter(request =>
      request.url === 'https://bot.example.com/v1/meet-events'
    ),
    webhookRequests: () => fetchRequests.filter(request =>
      request.url === 'https://webhook.example.com/events'
    ),
    setSendingEnabled(enabled) {
      const oldValue = syncData.sendingEnabled;
      syncData.sendingEnabled = enabled;
      listeners.storageChanged({
        sendingEnabled: { oldValue, newValue: enabled }
      }, 'sync');
    },
    sendEvent(rec, tabId = 1) {
      return new Promise(resolve => {
        assert.equal(listeners.message(
          { kind: 'event', rec },
          { tab: { id: tabId } },
          resolve
        ), true);
      });
    },
    sendObservation(tabId, observedRoster, details = {}) {
      return new Promise(resolve => {
        assert.equal(listeners.message({
          kind: 'monitorObservation',
          meeting: 'jpy-ymgz-tdo',
          observedAt: now,
          roster: observedRoster,
          rawRoster: details.rawRoster || observedRoster,
          self: {},
          changes: details.changes || []
        }, { tab: { id: tabId } }, resolve), true);
      });
    },
    sendMessage(message, sender) {
      return new Promise(resolve => {
        const keepAlive = listeners.message(message, sender, resolve);
        if (keepAlive !== true) resolve(undefined);
      });
    },
    sendViewMessage(message) {
      return this.sendMessage(message, {
        id: 'extension-id',
        url: 'chrome-extension://extension-id/src/side-panel.html'
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

function createPopupHarness(sendingEnabled, activeTab = null) {
  const elements = Object.fromEntries(
    [
      '#meeting',
      '#count',
      '#roster',
      '#self',
      '#feed',
      '#csv',
      '#opts',
      '#open-monitor',
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
  const runtimeMessages = [];
  const sidePanelOpens = [];
  const model = {
    meeting: 'jpy-ymgz-tdo',
    forwardingEnabled: sendingEnabled,
    roster: {},
    history: [],
    content: { online: true }
  };
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
      runtime: {
        openOptionsPage() {},
        async sendMessage(message) {
          runtimeMessages.push(message);
          if (message.kind === 'getMonitorModel') return model;
          return { saved: true };
        },
        onMessage: { addListener() {} }
      },
      sidePanel: {
        async open(options) {
          sidePanelOpens.push(options);
        }
      },
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
          return activeTab ? [activeTab] : [];
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
  return { elements, runtimeMessages, sidePanelOpens, syncWrites };
}

function meetEvent(id) {
  return {
    type: 'chat_message',
    id,
    meeting: 'jpy-ymgz-tdo',
    ts: `2026-10-27T08:00:0${id === 'good' ? '2' : '1'}Z`,
    name: 'Alex',
    text: 'Hello'
  };
}

function roster(name) {
  return {
    participant: {
      name,
      cam: false,
      mic: 'muted',
      self: false,
      host: false
    }
  };
}

function snapshotBody(sessionId, sequence, participants) {
  return require('../src/snapshot.js').build({
    meeting: 'jpy-ymgz-tdo',
    sessionId,
    sequence,
    capturedAt: '2026-11-03T12:00:00.000Z',
    extensionVersion: '2.0.0',
    forwardingEnabled: true,
    roster: participants
  });
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
