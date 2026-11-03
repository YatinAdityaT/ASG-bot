const test = require('node:test');
const assert = require('node:assert/strict');

const { postMessage } = require('../src/meet-chat.js');

test('posts text through an already-open Meet chat composer', async () => {
  const events = [];
  const composer = {
    value: '',
    focus() {},
    dispatchEvent(event) { events.push(event.type); }
  };
  let clicks = 0;
  const send = { click() { clicks += 1; } };
  const document = fakeDocument({
    'textarea[aria-label*="Send a message" i]': composer,
    'button[aria-label*="Send a message" i]': send
  });

  assert.equal(await postMessage(document, 'Hello Meet'), true);
  assert.equal(composer.value, 'Hello Meet');
  assert.deepEqual(events, ['input', 'change']);
  assert.equal(clicks, 1);
});

test('opens Meet chat before finding the composer', async () => {
  let open = false;
  const composer = {
    value: '',
    focus() {},
    dispatchEvent() {}
  };
  const document = fakeDocument({
    'button[aria-label*="Chat with everyone" i]': {
      click() { open = true; }
    },
    'textarea[aria-label*="Send a message" i]': () => open ? composer : null,
    'button[aria-label*="Send a message" i]': () => open ? { click() {} } : null
  });

  assert.equal(await postMessage(document, 'Opened chat', {
    wait: async () => {}
  }), true);
  assert.equal(composer.value, 'Opened chat');
});

function fakeDocument(elements) {
  return {
    querySelector(selector) {
      const value = elements[selector];
      return typeof value === 'function' ? value() : value || null;
    }
  };
}
