(function (root, factory) {
  const api = factory();
  root.MMDelivery = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => ({
  requestFor(event, apiURL, token) {
    const url = String(apiURL || '').trim();
    const bearer = String(token || '').trim();
    if (!url || !bearer) return null;

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
  }
}));
