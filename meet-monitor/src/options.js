const $ = s => document.querySelector(s);

chrome.storage.sync.get({ webhook: '', consentBanner: true }).then(o => {
  $('#webhook').value = o.webhook;
  $('#consentBanner').checked = o.consentBanner;
});

$('#save').onclick = async () => {
  const webhook = $('#webhook').value.trim();
  if (webhook) {
    let origin;
    try {
      const url = new URL(webhook);
      if (!['https:', 'http:'].includes(url.protocol)) throw new Error('unsupported protocol');
      origin = `${url.origin}/*`;
    } catch {
      $('#saved').textContent = 'Enter a valid HTTP(S) URL';
      return;
    }

    const granted = await chrome.permissions.request({ origins: [origin] });
    if (!granted) {
      $('#saved').textContent = 'Webhook permission denied';
      return;
    }
  }

  await chrome.storage.sync.set({
    webhook,
    consentBanner: $('#consentBanner').checked
  });
  $('#saved').textContent = 'Saved ✓';
  setTimeout(() => $('#saved').textContent = '', 1500);
};
