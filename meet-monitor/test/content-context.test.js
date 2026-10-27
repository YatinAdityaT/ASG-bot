const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const { ParticipantTracker } = require('../src/state.js');

test('synchronous context invalidation stops the observer, timers, and later sends', () => {
  const harness = createHarness({
    sendMessage(call) {
      if (call === 2) throw new Error('Extension context invalidated.');
      return Promise.resolve();
    }
  });

  harness.scheduleMutation();
  assert.doesNotThrow(() => harness.runInterval());

  assert.equal(harness.sendCalls(), 2);
  assert.equal(harness.disconnectCalls(), 1);
  assert.deepEqual(harness.activeTimerKinds(), []);
  assert.equal(harness.timersClearedExactlyOnce(), true);

  harness.runAllCallbacks();
  assert.equal(harness.sendCalls(), 2);
  assert.equal(harness.disconnectCalls(), 1);
  assert.equal(harness.timersClearedExactlyOnce(), true);
});

test('rejected context invalidation stops stale content-script work', async () => {
  const harness = createHarness({
    sendMessage(call) {
      return call === 2
        ? Promise.reject(new Error('Extension context invalidated.'))
        : Promise.resolve();
    }
  });

  harness.scheduleMutation();
  harness.runInterval();
  await settle();

  assert.equal(harness.sendCalls(), 2);
  assert.equal(harness.disconnectCalls(), 1);
  assert.deepEqual(harness.activeTimerKinds(), []);
  assert.equal(harness.timersClearedExactlyOnce(), true);

  harness.runAllCallbacks();
  await settle();
  assert.equal(harness.sendCalls(), 2);
  assert.equal(harness.disconnectCalls(), 1);
});

test('consent lookup invalidation is handled and cleans up stale work', () => {
  const harness = createHarness({ controlledStorage: true });

  assert.doesNotThrow(() => harness.rejectStorage(
    new Error('Extension context invalidated.')
  ));
  assert.equal(harness.disconnectCalls(), 1);
  assert.deepEqual(harness.activeTimerKinds(), []);
});

test('unrelated synchronous send errors remain visible', () => {
  const harness = createHarness({
    sendMessage(call) {
      if (call === 2) throw new TypeError('programming error');
      return Promise.resolve();
    }
  });

  assert.throws(() => harness.runInterval(), {
    name: 'TypeError',
    message: 'programming error'
  });
  assert.equal(harness.disconnectCalls(), 0);
  assert.notDeepEqual(harness.activeTimerKinds(), []);
});

test('unknown toolbar values preserve the self tile media state', () => {
  const harness = createBehaviorHarness({
    selfState: { mic: 'unknown', cam: 'unknown' },
    tileState: { mic: 'muted', cam: true }
  });

  const join = harness.events().find(event => event.type === 'join');
  assert.equal(join.mic, 'muted');
  assert.equal(join.cam, true);
});

test('a disappearing toolbar does not create false self media transitions', () => {
  const harness = createBehaviorHarness({
    selfState: { mic: 'on', cam: 'on' },
    tileState: { mic: 'unmuted', cam: true }
  });

  harness.setSelfState({ mic: 'unknown', cam: 'unknown' });
  harness.runIntervalAt(10_100);
  harness.runIntervalAt(12_100);

  assert.deepEqual(harness.events().map(event => event.type), ['join']);
});

test('the active confirmation callback emits one confirmed event exactly once', () => {
  const harness = createBehaviorHarness({
    selfState: { mic: 'off', cam: 'off' },
    tileState: { mic: 'muted', cam: false }
  });

  harness.setSelfState({ mic: 'off', cam: 'on' });
  harness.runIntervalAt(10_100);
  const callback = harness.activeConfirmationCallback();
  harness.runCallbackAt(callback, 12_100);
  harness.runCallbackAt(callback, 12_100);

  assert.equal(
    harness.events().filter(event => event.type === 'cam_on').length,
    1
  );
});

test('an incrementally rendered chat row emits once after text appears', () => {
  const harness = createBehaviorHarness();
  harness.setChat({ id: 'message-1', text: '' });
  harness.runIntervalAt(10_100);
  assert.equal(harness.events().some(event => event.type === 'chat_message'), false);

  harness.setChat({ id: 'message-1', text: 'Hello' });
  harness.runIntervalAt(10_200);
  harness.runIntervalAt(10_300);

  const chats = harness.events().filter(event => event.type === 'chat_message');
  assert.equal(chats.length, 1);
  assert.equal(chats[0].text, 'Hello');
});

function createHarness(options = {}) {
  const source = fs.readFileSync(
    path.join(__dirname, '..', 'src', 'content.js'),
    'utf8'
  );
  const timers = [];
  let observer;
  let sends = 0;
  let storageReject;

  class FakeTracker {
    constructor() {
      this.roster = new Map();
    }
    update() {
      return [{
        type: 'join',
        id: 'participant-1',
        cur: { name: 'Alex', cam: false, mic: 'muted' }
      }];
    }
    nextDeadline() {
      return 11_000;
    }
    leave() {
      return null;
    }
  }

  const newTimer = (kind, callback, delay) => {
    const timer = { kind, callback, delay, active: true, clearCount: 0 };
    timers.push(timer);
    return timer;
  };
  const storageResult = options.controlledStorage
    ? {
        then() {
          return {
            catch(handler) {
              storageReject = handler;
            }
          };
        }
      }
    : Promise.resolve({ consentBanner: false });
  const context = {
    window: {
      MMSelectors: {
        meetingCode: () => 'meeting',
        tileSel: '.tile',
        idAttr: 'data-id',
        announcementSel: '.announcement',
        chatGroupSel: '.chat',
        selfState: () => ({ mic: 'off', cam: 'off' })
      },
      MMParticipantTracker: FakeTracker,
      MMLeftParticipantName: () => null
    },
    document: {
      body: { appendChild() {} },
      querySelectorAll: () => [],
      createElement: () => ({ style: {} })
    },
    Date: class extends Date {
      static now() {
        return 10_000;
      }
    },
    MutationObserver: class {
      constructor(callback) {
        this.callback = callback;
        this.disconnectCount = 0;
        observer = this;
      }
      observe() {}
      disconnect() {
        this.disconnectCount += 1;
      }
    },
    setTimeout: (callback, delay) => newTimer('timeout', callback, delay),
    clearTimeout(timer) {
      timer.active = false;
      timer.clearCount += 1;
    },
    setInterval: (callback, delay) => newTimer('interval', callback, delay),
    clearInterval(timer) {
      timer.active = false;
      timer.clearCount += 1;
    },
    chrome: {
      runtime: {
        sendMessage(message) {
          sends += 1;
          return options.sendMessage
            ? options.sendMessage(sends, message)
            : Promise.resolve();
        },
        onMessage: { addListener() {} }
      },
      storage: {
        sync: { get: () => storageResult }
      }
    }
  };

  vm.runInNewContext(source, context, { filename: 'content.js' });

  return {
    activeTimerKinds: () => timers.filter(timer => timer.active).map(timer => timer.kind),
    disconnectCalls: () => observer.disconnectCount,
    rejectStorage(error) {
      assert.equal(typeof storageReject, 'function', 'storage rejection handler registered');
      storageReject(error);
    },
    runAllCallbacks() {
      for (const timer of timers.slice()) timer.callback();
    },
    runInterval() {
      timers.find(timer => timer.kind === 'interval').callback();
    },
    scheduleMutation() {
      observer.callback();
    },
    sendCalls: () => sends,
    timersClearedExactlyOnce: () =>
      timers.length > 0 && timers.every(timer => timer.clearCount === 1)
  };
}

function createBehaviorHarness(options = {}) {
  const source = fs.readFileSync(
    path.join(__dirname, '..', 'src', 'content.js'),
    'utf8'
  );
  let now = 10_000;
  let selfState = options.selfState || { mic: 'off', cam: 'off' };
  const tileState = options.tileState || { mic: 'muted', cam: false };
  let chat = null;
  const sent = [];
  const timers = [];
  const intervalCallbacks = [];
  const tile = {
    getAttribute: () => 'self-id'
  };
  const chatGroup = {
    querySelector(selector) {
      if (selector === '.sender') return { innerText: 'Alex' };
      return null;
    },
    querySelectorAll(selector) {
      if (selector !== '.row' || !chat) return [];
      return [{
        getAttribute: () => chat.id,
        querySelector: selector =>
          selector === '.text' ? { innerText: chat.text } : null
      }];
    }
  };
  const selectors = {
    meetingCode: () => 'meeting',
    tileSel: '.tile',
    idAttr: 'data-id',
    announcementSel: '.announcement',
    chatGroupSel: '.chat',
    chatSenderSel: '.sender',
    chatMsgRowSel: '.row',
    chatTextSel: '.text',
    pickName: () => 'Self',
    camOf: () => tileState.cam,
    micOf: () => tileState.mic,
    detectSelf: () => true,
    detectHost: () => false,
    selfState: () => selfState
  };
  const context = {
    window: {
      MMSelectors: selectors,
      MMParticipantTracker: ParticipantTracker,
      MMLeftParticipantName: () => null
    },
    document: {
      body: { appendChild() {} },
      querySelectorAll(selector) {
        if (selector === '.tile') return [tile];
        if (selector === '.chat') return chat ? [chatGroup] : [];
        return [];
      },
      createElement: () => ({ style: {} })
    },
    Date: class extends Date {
      static now() {
        return now;
      }
    },
    MutationObserver: class {
      observe() {}
      disconnect() {}
    },
    setTimeout(callback, delay) {
      const timer = { callback, delay, active: true };
      timers.push(timer);
      return timer;
    },
    clearTimeout(timer) {
      timer.active = false;
    },
    setInterval(callback) {
      intervalCallbacks.push(callback);
      return { callback, active: true };
    },
    clearInterval(timer) {
      timer.active = false;
    },
    chrome: {
      runtime: {
        sendMessage(message) {
          sent.push(message);
          return Promise.resolve();
        },
        onMessage: { addListener() {} }
      },
      storage: {
        sync: { get: () => Promise.resolve({ consentBanner: false }) }
      }
    }
  };

  vm.runInNewContext(source, context, { filename: 'content.js' });

  return {
    activeConfirmationCallback() {
      const timer = timers.findLast(candidate => candidate.active);
      assert.ok(timer, 'active confirmation timer exists');
      return timer.callback;
    },
    events: () => sent
      .filter(message => message.kind === 'event')
      .map(message => message.rec),
    runActiveConfirmationAt(timestamp) {
      this.runCallbackAt(this.activeConfirmationCallback(), timestamp);
    },
    runCallbackAt(callback, timestamp) {
      now = timestamp;
      callback();
    },
    runIntervalAt(timestamp) {
      now = timestamp;
      intervalCallbacks[0]();
    },
    setChat(value) {
      chat = value;
    },
    setSelfState(value) {
      selfState = value;
    }
  };
}

async function settle() {
  for (let turn = 0; turn < 5; turn++) await Promise.resolve();
}
