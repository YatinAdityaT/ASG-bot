const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

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

async function settle() {
  for (let turn = 0; turn < 5; turn++) await Promise.resolve();
}
