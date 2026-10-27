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
      if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) {
        throw new Error('unsupported URL');
      }
      if (label === 'Bot API' && (url.pathname !== '/v1/meet-events' || url.search || url.hash)) {
        throw new Error('wrong endpoint');
      }
      origins.add(`${url.origin}/*`);
    } catch {
      $('#saved').textContent = `${label}: enter a valid HTTP(S) URL${label === 'Bot API' ? ' ending in /v1/meet-events' : ''}`;
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
  await chrome.storage.local.set({ botAPIToken });
  $('#saved').textContent = 'Saved ✓';
  setTimeout(() => $('#saved').textContent = '', 1500);
};
