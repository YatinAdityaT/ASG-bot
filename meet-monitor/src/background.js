importScripts('delivery.js');

const DEFAULTS = {
  webhook: '',
  botAPIURL: '',
  consentBanner: true,
  sendingEnabled: true
};
const ALLOWED_MEETING = 'jpy-ymgz-tdo';
const FORWARD_DEDUPE_MS = 5_000;
const OUTBOX_KEY = 'botAPIOutbox';
const RETRY_ALARM = 'bot-api-outbox';
const SEND_SPACING_MS = 2_500;
const MIN_ALARM_DELAY_MS = 30_000;
const MAX_BATCH_SIZE = 10;
const MAX_OUTBOX_SIZE = 500;
const MAX_ATTEMPTS = 10;
const REQUEST_TIMEOUT_MS = 15_000;
const storageProtection = chrome.storage.local.setAccessLevel({
  accessLevel: 'TRUSTED_CONTEXTS'
});
let outboxOperation = Promise.resolve();
let nextSendAllowedAt = 0;
const recentlyForwarded = new Map();

chrome.runtime.onInstalled.addListener(async () => {
  await storageProtection;
  const existing = await chrome.storage.sync.get(DEFAULTS);
  await chrome.storage.sync.set({ ...DEFAULTS, ...existing });
});

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg?.kind === 'setBotAPIConfig') {
    if (!isTrustedOptionsPage(_sender)) {
      sendResponse({ saved: false });
      return false;
    }
    saveBotAPIConfig(msg.config)
      .then(saved => {
        sendResponse({ saved });
        if (saved) {
          flushBotAPIOutbox().catch(() => {
            console.warn('Meet event bot API configuration retry failed');
          });
        }
      })
      .catch(() => sendResponse({ saved: false }));
    return true;
  }

  if (msg?.kind === 'event') {
    forwardEvent(msg.rec)
      .then(queued => sendResponse({ queued }))
      .catch(() => {
        console.warn('Meet event forwarding failed');
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

chrome.storage.onChanged.addListener((changes, areaName) => {
  if (areaName === 'sync' && changes.sendingEnabled?.newValue === true) {
    flushBotAPIOutbox().catch(() => {
      console.warn('Meet event bot API re-enable retry failed');
    });
  }
});

async function forwardEvent(rec) {
  const { sendingEnabled } = await chrome.storage.sync.get({
    sendingEnabled: true
  });
  if (!sendingEnabled || rec?.meeting !== ALLOWED_MEETING) return false;

  const now = Date.now();
  for (const [key, seenAt] of recentlyForwarded) {
    if (now - seenAt >= FORWARD_DEDUPE_MS) recentlyForwarded.delete(key);
  }
  const key = MMDelivery.forwardingKey(rec);
  if (recentlyForwarded.has(key)) return false;
  recentlyForwarded.set(key, now);

  forwardWebhook(rec).catch(() => {});
  const queued = await enqueueBotAPI(rec);
  if (queued) await flushBotAPIOutbox();
  return queued;
}

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
  const { botAPIConfig } = await chrome.storage.local.get({ botAPIConfig: null });
  return {
    botAPIURL: botAPIConfig?.url || '',
    botAPIToken: botAPIConfig?.token || '',
    generation: botAPIConfig?.generation || ''
  };
}

function enqueueBotAPI(rec) {
  return withOutboxLock(async () => {
    const { botAPIURL, botAPIToken, generation } = await botAPIConfig();
    if (!MMDelivery.requestFor(rec, botAPIURL, botAPIToken)) return false;

    const stored = await chrome.storage.local.get({ [OUTBOX_KEY]: [] });
    const outbox = stored[OUTBOX_KEY];
    const key = MMDelivery.eventKey(rec);
    if (!outbox.some(entry => entry.key === key)) {
      if (outbox.length >= MAX_OUTBOX_SIZE) {
        console.warn('Meet event bot API outbox is full');
        return false;
      }
      outbox.push({ key, rec, generation, attempts: 0, nextAttemptAt: 0 });
      await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
    }
    return true;
  });
}

function flushBotAPIOutbox() {
  return withOutboxLock(async () => {
    const stored = await chrome.storage.local.get({ [OUTBOX_KEY]: [] });
    const outbox = stored[OUTBOX_KEY];
    const allowedOutbox = outbox.filter(
      entry => entry.rec?.meeting === ALLOWED_MEETING
    );
    if (allowedOutbox.length !== outbox.length) {
      outbox.splice(0, outbox.length, ...allowedOutbox);
      await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
    }
    if (!outbox.length) return;

    const { sendingEnabled } = await chrome.storage.sync.get({
      sendingEnabled: true
    });
    if (!sendingEnabled) return;

    const { botAPIURL, botAPIToken, generation } = await botAPIConfig();
    for (let processed = 0; outbox.length && processed < MAX_BATCH_SIZE; processed++) {
      const entry = outbox[0];
      if (entry.generation !== generation) {
        outbox.shift();
        await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
        continue;
      }
      const delay = Math.max(entry.nextAttemptAt, nextSendAllowedAt) - Date.now();
      if (delay > 0) {
        scheduleRetry(delay);
        break;
      }

      const request = MMDelivery.requestFor(entry.rec, botAPIURL, botAPIToken);
      if (!request) return;

      const currentPolicy = await chrome.storage.sync.get({
        sendingEnabled: true
      });
      if (!currentPolicy.sendingEnabled) return;

      let response;
      try {
        nextSendAllowedAt = Date.now() + SEND_SPACING_MS;
        response = await fetchWithTimeout(request);
      } catch {
        response = null;
      }

      if (response?.ok || response?.status === 409) {
        outbox.shift();
        await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
      } else if (response && isPermanentClientError(response.status)) {
        outbox.shift();
        await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
        console.warn('Meet event bot API rejected an event permanently');
      } else {
        entry.attempts += 1;
        if (entry.attempts >= MAX_ATTEMPTS) {
          outbox.shift();
          await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
          console.warn('Meet event bot API retry limit reached');
        } else {
          entry.nextAttemptAt = Date.now() + MMDelivery.retryDelay(entry.attempts - 1);
          await chrome.storage.local.set({ [OUTBOX_KEY]: outbox });
          scheduleRetry(entry.nextAttemptAt - Date.now());
          break;
        }
      }

      if (outbox.length && processed+1 < MAX_BATCH_SIZE) {
        await sleep(Math.max(nextSendAllowedAt - Date.now(), 0));
      }
    }

    if (outbox.length) {
      scheduleRetry(Math.max(outbox[0].nextAttemptAt, nextSendAllowedAt) - Date.now());
    }
  });
}

function saveBotAPIConfig(config) {
  return withOutboxLock(async () => {
    await storageProtection;
    const url = String(config?.url || '').trim();
    const token = String(config?.token || '').trim();
    if (!url && !token) {
      await chrome.storage.local.set({
        botAPIConfig: null,
        [OUTBOX_KEY]: []
      });
      return true;
    }
    if (!MMDelivery.requestFor({}, url, token)) return false;
    const stored = await chrome.storage.local.get({
      botAPIConfig: null,
      [OUTBOX_KEY]: []
    });
    const oldConfig = stored.botAPIConfig;
    const outbox = oldConfig?.url === url
      ? stored[OUTBOX_KEY].map(entry => ({
          ...entry,
          generation: String(config?.generation || '')
        }))
      : [];
    await chrome.storage.local.set({
      botAPIConfig: {
        url,
        token,
        generation: String(config?.generation || '')
      },
      [OUTBOX_KEY]: outbox
    });
    return true;
  });
}

function withOutboxLock(operation) {
  outboxOperation = outboxOperation.then(operation, operation);
  return outboxOperation;
}

function scheduleRetry(delay) {
  chrome.alarms.create(RETRY_ALARM, {
    when: Date.now() + Math.max(delay, MIN_ALARM_DELAY_MS)
  });
}

function sleep(delay) {
  return new Promise(resolve => setTimeout(resolve, delay));
}

async function fetchWithTimeout(request) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    return await fetch(request.url, {
      ...request.options,
      signal: controller.signal
    });
  } finally {
    clearTimeout(timer);
  }
}

function isPermanentClientError(status) {
  return status >= 400 && status < 500 &&
    ![408, 409, 425, 429].includes(status);
}

function isTrustedOptionsPage(sender) {
  return sender?.id === chrome.runtime.id &&
    sender?.url === chrome.runtime.getURL('src/options.html');
}

storageProtection
  .then(() => flushBotAPIOutbox())
  .catch(() => {
    console.warn('Meet Monitor could not protect local credential storage');
  });
