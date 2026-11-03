(function (root, factory) {
  const api = factory();
  root.MMMeetChat = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const composerSelectors = [
    'textarea[aria-label*="Send a message" i]',
    '[contenteditable="true"][aria-label*="Send a message" i]'
  ];
  const openChatSelectors = [
    'button[aria-label*="Chat with everyone" i]',
    '[role="button"][aria-label*="Chat with everyone" i]',
    'button[aria-label*="Show chat" i]'
  ];
  const sendSelectors = [
    'button[aria-label*="Send a message" i]',
    'button[aria-label="Send"]'
  ];

  const firstMatch = (document, selectors) => {
    for (const selector of selectors) {
      const element = document.querySelector(selector);
      if (element) return element;
    }
    return null;
  };

  const defaultWait = milliseconds =>
    new Promise(resolve => setTimeout(resolve, milliseconds));

  async function postMessage(document, text, options = {}) {
    const message = String(text || '').trim();
    if (!message) return false;

    let composer = firstMatch(document, composerSelectors);
    if (!composer) {
      const openChat = firstMatch(document, openChatSelectors);
      if (!openChat) return false;
      openChat.click();
      const wait = options.wait || defaultWait;
      for (let attempt = 0; attempt < 30 && !composer; attempt++) {
        await wait(100);
        composer = firstMatch(document, composerSelectors);
      }
    }
    if (!composer) return false;

    composer.focus();
    if ('value' in composer) {
      const prototype = Object.getPrototypeOf(composer);
      const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set;
      if (setter) setter.call(composer, message);
      else composer.value = message;
    } else {
      composer.textContent = message;
    }

    const EventConstructor = document.defaultView?.Event || Event;
    composer.dispatchEvent(new EventConstructor('input', { bubbles: true }));
    composer.dispatchEvent(new EventConstructor('change', { bubbles: true }));

    const send = firstMatch(document, sendSelectors);
    if (!send) return false;
    send.click();
    return true;
  }

  return { postMessage };
});
