const test = require('node:test');
const assert = require('node:assert/strict');

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

test('Meet leave announcements expose the participant name', () => {
  assert.equal(leftParticipantName('Alex left the call'), 'Alex');
  assert.equal(leftParticipantName('Alex joined the call'), null);
});

