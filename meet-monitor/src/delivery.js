(function (root, factory) {
  const api = factory();
  root.MMDelivery = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const retryDelays = [5_000, 30_000, 120_000, 600_000, 3_600_000];

  const isAllowedBotAPIURL = raw => {
    try {
      const url = new URL(String(raw || '').trim());
      if (url.pathname !== '/v1/meet-events' || url.search || url.hash || url.username || url.password) {
        return false;
      }
      if (url.protocol === 'https:') return true;
      return url.protocol === 'http:' &&
        ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname);
    } catch {
      return false;
    }
  };

  const eventKey = event => JSON.stringify([
    event?.type || '',
    event?.id || '',
    event?.meeting || '',
    event?.ts || ''
  ]);

  const retryDelay = attempts =>
    retryDelays[Math.min(Math.max(Number(attempts) || 0, 0), retryDelays.length - 1)];

  const requestFor = (event, apiURL, token) => {
    const url = String(apiURL || '').trim();
    const bearer = String(token || '').trim();
    if (!isAllowedBotAPIURL(url) || !bearer) return null;

    return {
      url,
      options: {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${bearer}`
        },
        body: JSON.stringify(event)
      }
    };
  };

  return { eventKey, isAllowedBotAPIURL, requestFor, retryDelay };
});
