const test = require('node:test');
const assert = require('node:assert/strict');

const {
  eventKey,
  forwardingKey,
  isAllowedBotAPIURL,
  requestFor,
  retryDelay
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
