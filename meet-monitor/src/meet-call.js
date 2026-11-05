(function (root, factory) {
  const api = factory();
  root.MMMeetCall = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const leaveSelectors = [
    'button[aria-label*="Leave call" i]',
    '[role="button"][aria-label*="Leave call" i]'
  ];

  const defaultWait = milliseconds =>
    new Promise(resolve => setTimeout(resolve, milliseconds));

  function firstMatch(document, selectors) {
    for (const selector of selectors) {
      const element = document.querySelector(selector);
      if (element) return element;
    }
    return null;
  }

  function namedButton(document, names) {
    const wanted = names.map(name => name.toLowerCase());
    for (const element of document.querySelectorAll('button, [role="button"]')) {
      const text = String(element.innerText || element.textContent || '').trim().toLowerCase();
      const label = String(element.getAttribute?.('aria-label') || '').trim().toLowerCase();
      if (wanted.includes(text) || wanted.includes(label)) return element;
    }
    return null;
  }

  function isInCall(document) {
    return Boolean(firstMatch(document, leaveSelectors));
  }

  function leave(document) {
    const button = firstMatch(document, leaveSelectors);
    if (!button) return false;
    button.click();
    return true;
  }

  async function join(document, options = {}) {
    const wait = options.wait || defaultWait;
    const attempts = options.attempts || 60;
    for (let attempt = 0; attempt < attempts; attempt++) {
      if (isInCall(document)) return true;
      const button = namedButton(document, ['Rejoin', 'Join now']);
      if (button && !button.disabled) button.click();
      await wait(500);
    }
    return isInCall(document);
  }

  return { isInCall, join, leave };
});
