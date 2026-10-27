const test = require('node:test');
const assert = require('node:assert/strict');

const { requestFor } = require('../src/delivery.js');

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

