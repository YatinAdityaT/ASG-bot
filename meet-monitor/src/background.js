importScripts('delivery.js');

const DEFAULTS = { webhook: '', botAPIURL: '', consentBanner: true };

chrome.runtime.onInstalled.addListener(async () => {
  const existing = await chrome.storage.sync.get(DEFAULTS);
  chrome.storage.sync.set({ ...DEFAULTS, ...existing });
});

chrome.runtime.onMessage.addListener((msg) => {
  if (msg?.kind === 'event') {
    const rec = msg.rec;

    if (rec.type === 'chat_message' && rec.text) {
      chrome.notifications.create({
        type: 'basic',
        iconUrl: 'https://www.gstatic.com/images/branding/product/1x/hangouts_48dp.png',
        title: `Meet Chat - ${rec.name}`,
        message: rec.text.slice(0, 150),
        priority: 2
      }).catch(() => {});
    }

    forwardWebhook(rec).catch(() => {});
    forwardBotAPI(rec).catch(() => {
      console.warn('Meet event bot API delivery failed');
    });
  }
});

async function forwardWebhook(rec) {
  const { webhook } = await chrome.storage.sync.get({ webhook: '' });
  if (!webhook) return;
  await fetch(webhook, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(rec)
  });
}

async function forwardBotAPI(rec) {
  const [{ botAPIURL }, { botAPIToken }] = await Promise.all([
    chrome.storage.sync.get({ botAPIURL: '' }),
    chrome.storage.local.get({ botAPIToken: '' })
  ]);
  const request = MMDelivery.requestFor(rec, botAPIURL, botAPIToken);
  if (!request) return;

  const response = await fetch(request.url, request.options);
  if (!response.ok) throw new Error(`bot API returned HTTP ${response.status}`);
}
