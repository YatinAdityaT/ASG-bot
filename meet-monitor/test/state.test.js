const test = require('node:test');
const assert = require('node:assert/strict');

const { ParticipantTracker } = require('../src/state.js');

test('temporarily missing participants do not emit leave or duplicate join', () => {
  const tracker = new ParticipantTracker(30_000);
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  assert.deepEqual(tracker.update(new Map([['alex', alex]]), 0).map(e => e.type), ['join']);
  assert.deepEqual(tracker.update(new Map(), 10_000), []);
  assert.deepEqual(tracker.update(new Map([['alex', alex]]), 20_000), []);
});

test('participants missing beyond the grace period emit one leave', () => {
  const tracker = new ParticipantTracker(30_000);
  const alex = { name: 'Alex', cam: false, mic: 'muted' };

  tracker.update(new Map([['alex', alex]]), 0);
  assert.deepEqual(tracker.update(new Map(), 29_999), []);
  assert.deepEqual(tracker.update(new Map(), 30_000).map(e => e.type), ['leave']);
  assert.deepEqual(tracker.update(new Map(), 60_000), []);
});

