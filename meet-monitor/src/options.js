const $ = s => document.querySelector(s);

Promise.all([
  chrome.storage.sync.get({ webhook: '', botAPIURL: '', consentBanner: true }),
  chrome.storage.local.get({ botAPIToken: '' })
]).then(([sync, local]) => {
  $('#webhook').value = sync.webhook;
  $('#botAPIURL').value = sync.botAPIURL;
  $('#botAPIToken').value = local.botAPIToken;
  $('#consentBanner').checked = sync.consentBanner;
});

$('#save').onclick = async () => {
  const previous = await chrome.storage.sync.get({ webhook: '', botAPIURL: '' });
  const webhook = $('#webhook').value.trim();
  const botAPIURL = $('#botAPIURL').value.trim();
  const botAPIToken = $('#botAPIToken').value.trim();
  if (!!botAPIURL !== !!botAPIToken) {
    $('#saved').textContent = 'Bot API URL and token are both required';
    return;
  }

  const origins = new Set();
  for (const [label, rawURL] of [['Webhook', webhook], ['Bot API', botAPIURL]]) {
    if (!rawURL) continue;
    try {
      const url = new URL(rawURL);
      if (label === 'Bot API' && !MMDelivery.isAllowedBotAPIURL(rawURL)) {
        throw new Error('unsafe bot API URL');
      }
      if (label === 'Webhook' &&
          (!['https:', 'http:'].includes(url.protocol) || url.username || url.password)) {
        throw new Error('unsupported URL');
      }
      origins.add(`${url.origin}/*`);
    } catch {
      $('#saved').textContent = label === 'Bot API'
        ? 'Bot API: use HTTPS ending in /v1/meet-events (HTTP is allowed only on loopback)'
        : 'Webhook: enter a valid HTTP(S) URL';
      return;
    }
  }
  if (origins.size) {
    const granted = await chrome.permissions.request({ origins: [...origins] });
    if (!granted) {
      $('#saved').textContent = 'Endpoint permission denied';
      return;
    }
  }

  await chrome.storage.sync.set({
    webhook,
    botAPIURL,
    consentBanner: $('#consentBanner').checked
  });
  await chrome.storage.local.set(botAPIURL
    ? { botAPIToken }
    : { botAPIToken: '', botAPIOutbox: [] });

  const previousOrigins = new Set(
    [previous.webhook, previous.botAPIURL]
      .filter(Boolean)
      .flatMap(value => {
        try { return [`${new URL(value).origin}/*`]; } catch { return []; }
      })
  );
  for (const origin of previousOrigins) {
    if (!origins.has(origin)) {
      await chrome.permissions.remove({ origins: [origin] });
    }
  }
  $('#saved').textContent = 'Saved ✓';
  setTimeout(() => $('#saved').textContent = '', 1500);
};
