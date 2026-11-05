const test = require('node:test');
const assert = require('node:assert/strict');

const call = require('../src/meet-call.js');

test('leaves an active call', () => {
  let clicks = 0;
  const document = fakeDocument({
    leave: { click() { clicks += 1; } }
  });

  assert.equal(call.isInCall(document), true);
  assert.equal(call.leave(document), true);
  assert.equal(clicks, 1);
});

test('rejoins and waits until the call is active', async () => {
  let joined = false;
  let clicks = 0;
  const rejoin = {
    innerText: 'Rejoin',
    disabled: false,
    getAttribute() { return ''; },
    click() {
      clicks += 1;
      joined = true;
    }
  };
  const document = fakeDocument({
    inCall: () => joined,
    buttons: [rejoin]
  });

  assert.equal(await call.join(document, {
    attempts: 2,
    wait: async () => {}
  }), true);
  assert.equal(clicks, 1);
});

function fakeDocument(options = {}) {
  return {
    querySelector(selector) {
      if (selector.includes('Leave call')) {
        if (typeof options.inCall === 'function' && options.inCall()) return {};
        return options.leave || null;
      }
      return null;
    },
    querySelectorAll() {
      return options.buttons || [];
    }
  };
}
