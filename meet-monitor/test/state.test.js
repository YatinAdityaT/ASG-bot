const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const { ParticipantTracker, leftParticipantName } = require('../src/state.js');

test('temporarily missing participants do not emit leave or duplicate join', () => {
  const tracker = new ParticipantTracker(30_000);
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  assert.deepEqual(tracker.update(new Map([['alex', alex]]), 0).map(e => e.type), ['join']);
  assert.deepEqual(tracker.update(new Map(), 10_000), []);
  assert.deepEqual(tracker.update(new Map([['alex', alex]]), 20_000), []);
});

test('DOM absence alone never claims that a participant left', () => {
  const tracker = new ParticipantTracker(30_000);
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  tracker.update(new Map([['alex', alex]]), 0);
  assert.deepEqual(tracker.update(new Map(), 29_999), []);
  assert.deepEqual(tracker.update(new Map(), 60_000), []);
});

test('an explicit Meet leave signal removes the participant once', () => {
  const tracker = new ParticipantTracker();
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  tracker.update(new Map([['alex', alex]]), 0);
  assert.equal(tracker.leave('alex').type, 'leave');
  assert.equal(tracker.leave('alex'), null);
  assert.deepEqual(tracker.update(new Map([['alex', alex]])), []);
  tracker.update(new Map());
  assert.deepEqual(tracker.update(new Map([['alex', alex]])).map(e => e.type), ['join']);
});

test('a different observed name neither renames nor replaces the established name', () => {
  const tracker = new ParticipantTracker();
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  tracker.update(new Map([['alex', alex]]), 0);

  assert.deepEqual(
    tracker.update(new Map([['alex', { ...alex, name: 'Wrong name' }]]), 1),
    []
  );
  assert.equal(tracker.roster.get('alex').name, 'Alex');
});

test('a transient camera change is canceled before it can be confirmed', () => {
  const tracker = new ParticipantTracker(2000);
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  tracker.update(new Map([['alex', alex]]), 0);
  assert.deepEqual(
    tracker.update(new Map([['alex', { ...alex, cam: true }]]), 100),
    []
  );
  assert.equal(tracker.nextDeadline(), 2100);

  assert.deepEqual(tracker.update(new Map([['alex', alex]]), 2099), []);
  assert.equal(tracker.nextDeadline(), null);
  assert.equal(tracker.roster.get('alex').cam, false);
});

test('simultaneous camera and microphone changes confirm once after 2000 ms', () => {
  const tracker = new ParticipantTracker(2000);
  const alex = { name: 'Alex', cam: false, mic: 'muted' };
  const changed = { ...alex, cam: true, mic: 'unmuted' };

  tracker.update(new Map([['alex', alex]]), 0);
  assert.deepEqual(tracker.update(new Map([['alex', changed]]), 100), []);
  assert.equal(tracker.nextDeadline(), 2100);
  assert.deepEqual(tracker.update(new Map([['alex', changed]]), 2099), []);

  assert.deepEqual(
    tracker.update(new Map([['alex', changed]]), 2100).map(e => e.type),
    ['cam_on', 'mic_unmuted']
  );
  assert.deepEqual(tracker.update(new Map([['alex', changed]]), 3000), []);
  assert.equal(tracker.nextDeadline(), null);
});

test('a participant does not join until a usable name appears', () => {
  const tracker = new ParticipantTracker();
  const unnamed = { name: '  ', cam: false, mic: 'muted' };
  const alex = { ...unnamed, name: 'Alex' };

  assert.deepEqual(tracker.update(new Map([['alex', unnamed]]), 0), []);
  assert.equal(tracker.roster.has('alex'), false);

  assert.deepEqual(
    tracker.update(new Map([['alex', alex]]), 1).map(e => e.type),
    ['join']
  );
  assert.equal(tracker.roster.get('alex').name, 'Alex');
});

test('explicit leave clears pending media candidates', () => {
  const tracker = new ParticipantTracker(2000);
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  tracker.update(new Map([['alex', alex]]), 0);
  tracker.update(new Map([['alex', { ...alex, cam: true }]]), 100);
  assert.equal(tracker.nextDeadline(), 2100);

  assert.equal(tracker.leave('alex').type, 'leave');
  assert.equal(tracker.nextDeadline(), null);
});

test('a missing tile interrupts pending media confirmation without removing the participant', () => {
  const tracker = new ParticipantTracker(2000);
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  tracker.update(new Map([['alex', alex]]), 0);
  tracker.update(new Map([['alex', { ...alex, cam: true }]]), 100);
  assert.equal(tracker.nextDeadline(), 2100);

  assert.deepEqual(tracker.update(new Map(), 1000), []);
  assert.equal(tracker.nextDeadline(), null);
  assert.equal(tracker.roster.get('alex').cam, false);
});

test('content scans use timestamps and replace stale confirmation timers', () => {
  const calls = { constructor: [], updates: 0, observedAt: [], cleared: [] };
  const timers = [];
  let observerCallback;

  class FakeTracker {
    constructor(stabilityMs) {
      calls.constructor.push(stabilityMs);
      this.roster = new Map();
    }
    update(_now, observedAt) {
      calls.updates += 1;
      calls.observedAt.push(observedAt);
      return [];
    }
    nextDeadline() {
      return 12_000;
    }
    leave() {
      return null;
    }
  }

  const window = {
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
  };
  const document = {
    body: { appendChild() {} },
    querySelectorAll: () => [],
    createElement: () => ({ style: {} })
  };
  const context = {
    window,
    document,
    Date: { now: () => 10_000 },
    MutationObserver: class {
      constructor(callback) {
        observerCallback = callback;
      }
      observe() {}
    },
    setTimeout(callback, delay) {
      const timer = { callback, delay };
      timers.push(timer);
      return timer;
    },
    clearTimeout(timer) {
      calls.cleared.push(timer);
    },
    setInterval() {},
    chrome: {
      runtime: {
        sendMessage: () => Promise.resolve(),
        onMessage: { addListener() {} }
      },
      storage: {
        sync: { get: () => Promise.resolve({ consentBanner: false }) }
      }
    }
  };

  const source = fs.readFileSync(path.join(__dirname, '../src/content.js'), 'utf8');
  vm.runInNewContext(source, context);

  assert.deepEqual(calls.constructor, [2000]);
  assert.deepEqual(calls.observedAt, [10_000]);
  assert.equal(timers[0].delay, 2000);

  const staleConfirmation = timers[0];
  observerCallback();
  assert.equal(timers[1].delay, 200);
  timers[1].callback();
  assert.equal(calls.updates, 2);
  assert.equal(calls.cleared.includes(staleConfirmation), true);

  staleConfirmation.callback();
  assert.equal(calls.updates, 2);
});

test('Meet leave announcements expose the participant name', () => {
  assert.equal(leftParticipantName('Alex left the call'), 'Alex');
  assert.equal(leftParticipantName('Alex joined the call'), null);
});
