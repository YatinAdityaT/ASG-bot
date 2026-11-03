(function (root, factory) {
  const api = factory();
  root.MMDelivery = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const retryDelays = [30_000, 60_000, 120_000, 600_000, 3_600_000];

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
    event?.type === 'chat_message' ? '' : event?.ts || ''
  ]);

  const forwardingKey = event => JSON.stringify([
    event?.type || '',
    event?.id || '',
    event?.meeting || '',
    event?.name || '',
    event?.cam ?? null,
    event?.mic ?? null,
    event?.text ?? null
  ]);

  const retryDelay = attempts =>
    retryDelays[Math.min(Math.max(Number(attempts) || 0, 0), retryDelays.length - 1)];

  const eventRequestFor = (event, apiURL, token) => {
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

  const requestFor = eventRequestFor;

  const stateRequestFor = (snapshot, apiURL, token) => {
    const url = String(apiURL || '').trim();
    const bearer = String(token || '').trim();
    if (!isAllowedBotAPIURL(url) || !bearer) return null;

    const derivedURL = new URL(url);
    derivedURL.pathname = '/v1/meet-state';

    return {
      url: derivedURL.toString(),
      options: {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${bearer}`
        },
        body: JSON.stringify(snapshot)
      }
    };
  };

  const commandRequestFor = (apiURL, token) => {
    const url = String(apiURL || '').trim();
    const bearer = String(token || '').trim();
    if (!isAllowedBotAPIURL(url) || !bearer) return null;

    const derivedURL = new URL(url);
    derivedURL.pathname = '/v1/meet-command';
    return {
      url: derivedURL.toString(),
      options: {
        method: 'GET',
        headers: { 'Authorization': `Bearer ${bearer}` }
      }
    };
  };

  const commandAckRequestFor = (id, apiURL, token) => {
    const commandId = String(id || '').trim();
    const request = commandRequestFor(apiURL, token);
    if (!commandId || !request) return null;

    const derivedURL = new URL(request.url);
    derivedURL.pathname = '/v1/meet-command/ack';
    return {
      url: derivedURL.toString(),
      options: {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': request.options.headers.Authorization
        },
        body: JSON.stringify({ id: commandId })
      }
    };
  };

  return {
    commandAckRequestFor,
    commandRequestFor,
    eventKey,
    eventRequestFor,
    forwardingKey,
    isAllowedBotAPIURL,
    requestFor,
    retryDelay,
    stateRequestFor
  };
});
