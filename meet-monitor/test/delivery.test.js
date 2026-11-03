const test = require('node:test');
const assert = require('node:assert/strict');

const {
  commandAckRequestFor,
  commandRequestFor,
  eventKey,
  eventRequestFor,
  forwardingKey,
  isAllowedBotAPIURL,
  requestFor,
  retryDelay,
  stateRequestFor
} = require('../src/delivery.js');

test('configured bot API request includes bearer authentication and exact event JSON', () => {
  const event = { type: 'join', id: 'participant-1', name: 'Alex' };
  const request = requestFor(
    event,
    'https://bot.example.com/v1/meet-events',
    'private-token'
  );

  assert.equal(request.url, 'https://bot.example.com/v1/meet-events');
  assert.deepEqual(request.options, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer private-token'
    },
    body: JSON.stringify(event)
  });
});

test('bot API request is disabled without both URL and token', () => {
  const event = { type: 'join' };
  assert.equal(requestFor(event, '', 'token'), null);
  assert.equal(requestFor(event, 'https://bot.example.com/v1/meet-events', ''), null);
  assert.equal(requestFor(event, '  ', '  '), null);
});

test('bot API URL requires HTTPS except for loopback development', () => {
  assert.equal(isAllowedBotAPIURL('https://bot.example.com/v1/meet-events'), true);
  assert.equal(isAllowedBotAPIURL('http://127.0.0.1:8080/v1/meet-events'), true);
  assert.equal(isAllowedBotAPIURL('http://localhost:8080/v1/meet-events'), true);
  assert.equal(isAllowedBotAPIURL('http://bot.example.com/v1/meet-events'), false);
  assert.equal(isAllowedBotAPIURL('https://bot.example.com/v1/messages'), false);
});

test('event keys use stable Meet identity fields', () => {
  const base = {
    type: 'join',
    id: 'participant-1',
    meeting: 'abc-defg-hij',
    ts: '2026-10-27T08:00:00Z'
  };
  assert.equal(
    eventKey({ ...base, name: 'Alex', cam: false }),
    eventKey({ ...base, name: 'Alex Renamed', cam: true })
  );
});

test('chat event keys ignore timestamp', () => {
  const event = {
    type: 'chat_message',
    id: 'participant-1',
    meeting: 'jpy-ymgz-tdo',
    ts: '2026-10-27T08:00:00Z'
  };

  assert.equal(
    eventKey(event),
    eventKey({ ...event, ts: '2026-10-27T08:00:01Z' })
  );
});

test('non-chat event keys retain timestamp', () => {
  const event = {
    type: 'join',
    id: 'participant-1',
    meeting: 'jpy-ymgz-tdo',
    ts: '2026-10-27T08:00:00Z'
  };

  assert.notEqual(
    eventKey(event),
    eventKey({ ...event, ts: '2026-10-27T08:00:01Z' })
  );
});

test('forwarding keys ignore timestamp but include event content', () => {
  const event = {
    type: 'chat_message',
    id: 'participant-1',
    meeting: 'jpy-ymgz-tdo',
    name: 'Alex',
    text: 'Hello',
    ts: '2026-10-27T08:00:00Z'
  };

  assert.equal(
    forwardingKey(event),
    forwardingKey({ ...event, ts: '2026-10-27T08:00:01Z' })
  );
  assert.notEqual(
    forwardingKey(event),
    forwardingKey({ ...event, text: 'Different message' })
  );
});

test('retry delays back off and cap at one hour', () => {
  assert.deepEqual(
    [0, 1, 2, 3, 4, 20].map(retryDelay),
    [30000, 60000, 120000, 600000, 3600000, 3600000]
  );
});

test('requestFor is an alias for eventRequestFor', () => {
  assert.equal(requestFor, eventRequestFor);
});

test('state request targets same-origin /v1/meet-state with snapshot body and bearer', () => {
  const snapshot = {
    protocolVersion: 1,
    meeting: 'abc-defg-hij',
    sessionId: 'sess-1',
    sequence: 3,
    capturedAt: '2026-11-03T12:00:00Z',
    extensionVersion: '2.0.0',
    forwardingEnabled: true,
    participants: [{ id: 'p1', name: 'Alex', cam: true, mic: 'unmuted', self: false, host: false }]
  };
  const request = stateRequestFor(
    snapshot,
    'https://bot.example.com/v1/meet-events',
    'private-token'
  );

  assert.equal(request.url, 'https://bot.example.com/v1/meet-state');
  assert.deepEqual(request.options, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer private-token'
    },
    body: JSON.stringify(snapshot)
  });
});

test('Meet command requests poll and acknowledge on the configured bot origin', () => {
  const poll = commandRequestFor(
    'https://bot.example.com/v1/meet-events',
    'private-token'
  );
  assert.deepEqual(poll, {
    url: 'https://bot.example.com/v1/meet-command',
    options: {
      method: 'GET',
      headers: { 'Authorization': 'Bearer private-token' }
    }
  });

  const ack = commandAckRequestFor(
    'message-1',
    'https://bot.example.com/v1/meet-events',
    'private-token'
  );
  assert.deepEqual(ack, {
    url: 'https://bot.example.com/v1/meet-command/ack',
    options: {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer private-token'
      },
      body: JSON.stringify({ id: 'message-1' })
    }
  });
});

test('state request derives meet-state on HTTPS and permitted loopback hosts', () => {
  const snapshot = { meeting: 'm', participants: [] };
  const token = 'tok';
  const cases = [
    ['https://bot.example.com/v1/meet-events', 'https://bot.example.com/v1/meet-state'],
    ['http://127.0.0.1:8080/v1/meet-events', 'http://127.0.0.1:8080/v1/meet-state'],
    ['http://localhost:9090/v1/meet-events', 'http://localhost:9090/v1/meet-state'],
    ['http://[::1]:8080/v1/meet-events', 'http://[::1]:8080/v1/meet-state']
  ];
  for (const [apiURL, expectedURL] of cases) {
    const request = stateRequestFor(snapshot, apiURL, token);
    assert.equal(request.url, expectedURL);
  }
});

test('state request rejects unsafe URL, missing URL, and missing token', () => {
  const snapshot = { meeting: 'm', participants: [] };
  assert.equal(stateRequestFor(snapshot, '', 'token'), null);
  assert.equal(stateRequestFor(snapshot, 'https://bot.example.com/v1/meet-events', ''), null);
  assert.equal(stateRequestFor(snapshot, 'http://bot.example.com/v1/meet-events', 'token'), null);
  assert.equal(stateRequestFor(snapshot, 'https://bot.example.com/v1/messages', 'token'), null);
});

test('state request does not mutate snapshot or configured API URL string', () => {
  const snapshot = {
    protocolVersion: 1,
    meeting: 'meet-1',
    sessionId: 'sess-1',
    sequence: 1,
    capturedAt: '2026-11-03T10:00:00Z',
    extensionVersion: '1.0.0',
    forwardingEnabled: false,
    participants: [{ id: 'p1', name: 'Alex', cam: true, mic: 'unmuted', self: false, host: false }]
  };
  const snapshotCopy = JSON.parse(JSON.stringify(snapshot));
  const apiURL = 'https://bot.example.com/v1/meet-events';
  stateRequestFor(snapshot, apiURL, 'token');
  assert.deepEqual(snapshot, snapshotCopy);
  assert.equal(apiURL, 'https://bot.example.com/v1/meet-events');
});
