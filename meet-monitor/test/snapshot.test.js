const test = require('node:test');
const assert = require('node:assert/strict');

const { canonicalParticipants, build, stateKey } = require('../src/snapshot.js');

test('canonicalParticipants returns empty array for null or empty roster', () => {
  assert.deepEqual(canonicalParticipants(null), []);
  assert.deepEqual(canonicalParticipants(undefined), []);
  assert.deepEqual(canonicalParticipants({}), []);
});

test('canonicalParticipants normalizes names, cam, mic, self, and host', () => {
  const roster = {
    p1: { name: '  Alex  ', cam: true, mic: 'unmuted', self: true, host: false },
    p2: { name: 'Bob', cam: false, mic: 'muted', self: false, host: true },
    p3: { name: undefined, cam: 1, mic: 'other', self: 0, host: undefined }
  };
  assert.deepEqual(canonicalParticipants(roster), [
    { id: 'p1', name: 'Alex', cam: true, mic: 'unmuted', self: true, host: false },
    { id: 'p2', name: 'Bob', cam: false, mic: 'muted', self: false, host: true },
    { id: 'p3', name: '', cam: true, mic: 'muted', self: false, host: false }
  ]);
});

test('canonicalParticipants sorts by id independent of insertion order', () => {
  const roster = {
    zebra: { name: 'Z' },
    alpha: { name: 'A' },
    mid: { name: 'M' }
  };
  assert.deepEqual(
    canonicalParticipants(roster).map(p => p.id),
    ['alpha', 'mid', 'zebra']
  );
  const reordered = { mid: roster.mid, zebra: roster.zebra, alpha: roster.alpha };
  assert.deepEqual(
    canonicalParticipants(reordered).map(p => p.id),
    ['alpha', 'mid', 'zebra']
  );
});

test('build returns exact snapshot shape with no extra fields', () => {
  const input = {
    meeting: 'abc-defg-hij',
    sessionId: 'sess-1',
    sequence: 42,
    capturedAt: '2026-11-03T12:00:00Z',
    extensionVersion: '2.0.0',
    forwardingEnabled: true,
    roster: {
      b: { name: 'B', cam: false, mic: 'muted' },
      a: { name: 'A', cam: true, mic: 'unmuted', self: true }
    }
  };
  const snapshot = build(input);
  assert.deepEqual(snapshot, {
    protocolVersion: 1,
    meeting: 'abc-defg-hij',
    sessionId: 'sess-1',
    sequence: 42,
    capturedAt: '2026-11-03T12:00:00Z',
    extensionVersion: '2.0.0',
    forwardingEnabled: true,
    participants: [
      { id: 'a', name: 'A', cam: true, mic: 'unmuted', self: true, host: false },
      { id: 'b', name: 'B', cam: false, mic: 'muted', self: false, host: false }
    ]
  });
  assert.deepEqual(Object.keys(snapshot).sort(), [
    'capturedAt',
    'extensionVersion',
    'forwardingEnabled',
    'meeting',
    'participants',
    'protocolVersion',
    'sequence',
    'sessionId'
  ]);
});

test('build coerces missing input fields to defaults', () => {
  assert.deepEqual(build({}), {
    protocolVersion: 1,
    meeting: '',
    sessionId: '',
    sequence: 0,
    capturedAt: '',
    extensionVersion: '',
    forwardingEnabled: false,
    participants: []
  });
});

test('stateKey is equal across participant order, sequence, and capturedAt changes', () => {
  const base = build({
    meeting: 'meet-1',
    sessionId: 'sess-1',
    sequence: 1,
    capturedAt: '2026-11-03T10:00:00Z',
    extensionVersion: '1.0.0',
    forwardingEnabled: true,
    roster: { p1: { name: 'Alex', cam: true, mic: 'unmuted' } }
  });
  const reordered = build({
    meeting: 'meet-1',
    sessionId: 'sess-1',
    sequence: 99,
    capturedAt: '2026-11-03T11:00:00Z',
    extensionVersion: '1.0.0',
    forwardingEnabled: true,
    roster: { p1: { name: 'Alex', cam: true, mic: 'unmuted' } }
  });
  const shuffledParticipants = {
    ...base,
    participants: [...base.participants].reverse(),
    sequence: 500,
    capturedAt: '2026-12-01T00:00:00Z',
    protocolVersion: 99
  };
  assert.equal(stateKey(base), stateKey(reordered));
  assert.equal(stateKey(base), stateKey(shuffledParticipants));
});

test('stateKey differs for roster, media, name, meeting, session, extension version, forwarding', () => {
  const base = build({
    meeting: 'meet-1',
    sessionId: 'sess-1',
    extensionVersion: '1.0.0',
    forwardingEnabled: true,
    roster: { p1: { name: 'Alex', cam: true, mic: 'unmuted' } }
  });
  const keyBase = stateKey(base);

  const nameChange = build({
    ...base,
    roster: { p1: { name: 'Alex Renamed', cam: true, mic: 'unmuted' } }
  });
  assert.notEqual(keyBase, stateKey(nameChange));

  const camChange = build({
    ...base,
    roster: { p1: { name: 'Alex', cam: false, mic: 'unmuted' } }
  });
  assert.notEqual(keyBase, stateKey(camChange));

  const micChange = build({
    ...base,
    roster: { p1: { name: 'Alex', cam: true, mic: 'muted' } }
  });
  assert.notEqual(keyBase, stateKey(micChange));

  const rosterAdd = build({
    ...base,
    roster: {
      p1: { name: 'Alex', cam: true, mic: 'unmuted' },
      p2: { name: 'Bob', cam: false, mic: 'muted' }
    }
  });
  assert.notEqual(keyBase, stateKey(rosterAdd));

  assert.notEqual(keyBase, stateKey({ ...base, meeting: 'other-meet' }));
  assert.notEqual(keyBase, stateKey({ ...base, sessionId: 'other-sess' }));
  assert.notEqual(keyBase, stateKey({ ...base, extensionVersion: '2.0.0' }));
  assert.notEqual(keyBase, stateKey({ ...base, forwardingEnabled: false }));
});

test('build and stateKey do not mutate input roster or participant objects', () => {
  const roster = {
    p2: { name: ' B ', cam: false, mic: 'muted', self: false, host: true },
    p1: { name: ' A ', cam: true, mic: 'unmuted', self: true, host: false }
  };
  const rosterCopy = JSON.parse(JSON.stringify(roster));
  build({ roster });
  assert.deepEqual(roster, rosterCopy);

  const snapshot = build({
    meeting: 'm',
    sessionId: 's',
    extensionVersion: '1',
    forwardingEnabled: false,
    roster: { p1: { name: 'X', cam: true, mic: 'unmuted' } }
  });
  const participantsCopy = JSON.parse(JSON.stringify(snapshot.participants));
  stateKey(snapshot);
  assert.deepEqual(snapshot.participants, participantsCopy);
});
