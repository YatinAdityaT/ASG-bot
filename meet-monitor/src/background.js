importScripts('delivery.js');

const DEFAULTS = { webhook: '', botAPIURL: '', consentBanner: true };
const OUTBOX_KEY = 'botAPIOutbox';
const RETRY_ALARM = 'bot-api-outbox';
const SEND_SPACING_MS = 2_100;
const storageProtection = chrome.storage.local.setAccessLevel({
  accessLevel: 'TRUSTED_CONTEXTS'
});
let outboxOperation = Promise.resolve();
let nextSendAllowedAt = 0;

chrome.runtime.onInstalled.addListener(async () => {
  await storageProtection;
  const existing = await chrome.storage.sync.get(DEFAULTS);
  await chrome.storage.sync.set({ ...DEFAULTS, ...existing });
});

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
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
    enqueueBotAPI(rec)
      .then(queued => queued ? flushBotAPIOutbox() : null)
      .then(() => sendResponse({ queued: true }))
      .catch(() => {
        console.warn('Meet event bot API queue failed');
        sendResponse({ queued: false });
      });
    return true;
  }
});

chrome.alarms.onAlarm.addListener(alarm => {
  if (alarm.name === RETRY_ALARM) {
    flushBotAPIOutbox().catch(() => {
      console.warn('Meet event bot API retry failed');
    });
  }
});

chrome.runtime.onStartup.addListener(() => {
  flushBotAPIOutbox().catch(() => {
    console.warn('Meet event bot API startup retry failed');
  });
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

async function botAPIConfig() {
  await storageProtection;
  const [{ botAPIURL }, { botAPIToken }] = await Promise.all([
    chrome.storage.sync.get({ botAPIURL: '' }),
    chrome.storage.local.get({ botAPIToken: '' })
  ]);
  return { botAPIURL, botAPIToken };
}

async function enqueueBotAPI(rec) {
  const { botAPIURL, botAPIToken } = await botAPIConfig();
  if (!MMDelivery.requestFor(rec, botAPIURL, botAPIToken)) return false;

  return withOutboxLock(async () => {
    const stored = await chrome.storage.local.get({ [OUTBOX_KEY]: [] });
    const outbox = stored[OUTBOX_KEY];
    const key = MMDelivery.eventKey(rec);
    if (!outbox.some(entry => entry.key === key)) {
      outbox.push({ key, rec, attempts: 0, nextAttemptAt: 0 });
      await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
    }
    return true;
  });
}

function flushBotAPIOutbox() {
  return withOutboxLock(async () => {
    const { botAPIURL, botAPIToken } = await botAPIConfig();
    const stored = await chrome.storage.local.get({ [OUTBOX_KEY]: [] });
    const outbox = stored[OUTBOX_KEY];
    if (!outbox.length) return;

    const entry = outbox[0];
    const delay = Math.max(entry.nextAttemptAt, nextSendAllowedAt) - Date.now();
    if (delay > 0) {
      scheduleRetry(delay);
      return;
    }

    const request = MMDelivery.requestFor(entry.rec, botAPIURL, botAPIToken);
    if (!request) return;

    try {
      nextSendAllowedAt = Date.now() + SEND_SPACING_MS;
      const response = await fetch(request.url, request.options);
      if (!response.ok && response.status !== 409) {
        throw new Error(`HTTP ${response.status}`);
      }
      outbox.shift();
      await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
      if (outbox.length) scheduleRetry(nextSendAllowedAt - Date.now());
    } catch {
      entry.attempts += 1;
      entry.nextAttemptAt = Date.now() + MMDelivery.retryDelay(entry.attempts - 1);
      await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
      scheduleRetry(entry.nextAttemptAt - Date.now());
      console.warn('Meet event bot API delivery deferred');
    }
  });
}

function withOutboxLock(operation) {
  outboxOperation = outboxOperation.then(operation, operation);
  return outboxOperation;
}

function scheduleRetry(delay) {
  chrome.alarms.create(RETRY_ALARM, { when: Date.now() + Math.max(delay, 500) });
}

storageProtection
  .then(() => flushBotAPIOutbox())
  .catch(() => {
    console.warn('Meet Monitor could not protect local credential storage');
  });
