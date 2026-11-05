const $ = s => document.querySelector(s);

Promise.all([
  chrome.storage.sync.get({
    webhook: '',
    botAPIURL: '',
    consentBanner: true,
    meetScheduleEnabled: true,
    meetQuietStart: '00:00',
    meetQuietEnd: '05:00',
    monitoredMeetLinks: MMMeetPolicy.DEFAULT_MEET_CODES
  }),
  chrome.storage.local.get({ botAPIConfig: null })
]).then(([sync, local]) => {
  $('#webhook').value = sync.webhook;
  $('#botAPIURL').value = local.botAPIConfig?.url || sync.botAPIURL;
  $('#botAPIToken').value = local.botAPIConfig?.token || '';
  $('#consentBanner').checked = sync.consentBanner;
  $('#meetScheduleEnabled').checked = sync.meetScheduleEnabled;
  $('#meetQuietStart').value = sync.meetQuietStart;
  $('#meetQuietEnd').value = sync.meetQuietEnd;
  let monitoredMeetCodes;
  try {
    monitoredMeetCodes = MMMeetPolicy.normalizeMeetCodes(sync.monitoredMeetLinks);
  } catch {
    monitoredMeetCodes = MMMeetPolicy.DEFAULT_MEET_CODES;
  }
  $('#monitoredMeetLinks').value = monitoredMeetCodes.join(',\n');
});

$('#save').onclick = async () => {
  const previous = await chrome.storage.sync.get({ webhook: '', botAPIURL: '' });
  const webhook = $('#webhook').value.trim();
  const botAPIURL = $('#botAPIURL').value.trim();
  const botAPIToken = $('#botAPIToken').value.trim();
  const meetQuietStart = $('#meetQuietStart').value;
  const meetQuietEnd = $('#meetQuietEnd').value;
  let monitoredMeetLinks;
  try {
    monitoredMeetLinks = MMMeetPolicy.normalizeMeetCodes(
      $('#monitoredMeetLinks').value
    );
  } catch (error) {
    $('#saved').textContent = error.message;
    return;
  }
  if (!!botAPIURL !== !!botAPIToken) {
    $('#saved').textContent = 'Bot API URL and token are both required';
    return;
  }
  if (!/^\d{2}:\d{2}$/.test(meetQuietStart) ||
      !/^\d{2}:\d{2}$/.test(meetQuietEnd) ||
      meetQuietStart === meetQuietEnd) {
    $('#saved').textContent = 'Leave and rejoin times must be different';
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

  const saved = await chrome.runtime.sendMessage({
    kind: 'setBotAPIConfig',
    config: botAPIURL ? {
      url: botAPIURL,
      token: botAPIToken,
      generation: crypto.randomUUID()
    } : null
  });
  if (!saved?.saved) {
    $('#saved').textContent = 'Bot API settings could not be saved';
    return;
  }

  await chrome.storage.sync.set({
    webhook,
    botAPIURL,
    consentBanner: $('#consentBanner').checked,
    meetScheduleEnabled: $('#meetScheduleEnabled').checked,
    meetQuietStart,
    meetQuietEnd,
    monitoredMeetLinks
  });

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
