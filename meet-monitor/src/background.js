const DEFAULTS = { webhook: '', consentBanner: true };

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
