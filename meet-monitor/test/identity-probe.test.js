const test = require('node:test');
const assert = require('node:assert/strict');

const probe = require('../src/identity-probe.js');

test('identity probe reports Meet IDs and locally visible email candidates', () => {
  const child = node('', {
    'data-email': 'alex@example.com',
    'data-participant-id': 'stable-looking-123'
  });
  const tile = node('Alex', {
    'data-requested-participant-id': 'spaces/room/devices/42'
  }, [child]);
  const page = node('Contact monitor@example.com', {
    'aria-label': 'Google Meet'
  }, [tile]);
  const root = {
    documentElement: page,
    querySelectorAll(selector) {
      return selector === '[data-requested-participant-id]' ? [tile] : [];
    }
  };
  const selectors = {
    tileSel: '[data-requested-participant-id]',
    idAttr: 'data-requested-participant-id',
    pickName: () => 'Alex'
  };

  const result = probe.inspect(root, selectors);

  assert.deepEqual(result.emailCandidates, [
    'alex@example.com',
    'monitor@example.com'
  ]);
  assert.deepEqual(result.participants, [{
    id: 'spaces/room/devices/42',
    name: 'Alex',
    emailCandidates: ['alex@example.com'],
    identityAttributes: [{
      name: 'data-email',
      value: 'alex@example.com'
    }, {
      name: 'data-participant-id',
      value: 'stable-looking-123'
    }, {
      name: 'data-requested-participant-id',
      value: 'spaces/room/devices/42'
    }]
  }]);
});

function node(text, attributes = {}, descendants = []) {
  return {
    innerText: text,
    textContent: text,
    attributes: Object.entries(attributes).map(([name, value]) => ({ name, value })),
    getAttribute(name) {
      return attributes[name] || null;
    },
    querySelectorAll() {
      return descendants;
    }
  };
}
