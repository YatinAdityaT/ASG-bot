importScripts('meet-policy.js', 'version-update.js', 'delivery.js', 'snapshot.js');

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
const HEARTBEAT_ALARM = 'meet-state-heartbeat';
const STATE_RETRY_ALARM = 'meet-state-retry';
const HEARTBEAT_MS = 30_000;
const SOURCE_STALE_MS = 45_000;
const SESSION_KEY = 'monitorSession';
const PENDING_SNAPSHOT_KEY = 'pendingMeetSnapshot';
const HISTORY_KEY = 'monitorHistory';
const DEBUG_HISTORY_KEY = 'monitorDebugHistory';
const DEBUG_ENABLED_KEY = 'monitorDebugEnabled';
const POSTED_COMMANDS_KEY = 'postedMeetCommandIds';
const TAB_MONITORING_KEY = 'tabMonitoringEnabled';
const UPDATE_ALARM = 'meet-monitor-update-check';
const RUNNING_VERSION_KEY = 'runningExtensionVersion';
const PENDING_VERSION_KEY = 'pendingExtensionVersion';
const CONTENT_SCRIPT_FILES = [
  'src/meet-policy.js',
  'src/selectors.js',
  'src/state.js',
  'src/meet-chat.js',
  'src/identity-probe.js',
  'src/content.js'
];
const MAX_HISTORY_SIZE = 1_000;
const MAX_DEBUG_HISTORY_SIZE = 250;
const MAX_SNAPSHOT_ATTEMPTS = 10;
const storageProtection = chrome.storage.local.setAccessLevel({
  accessLevel: 'TRUSTED_CONTEXTS'
});
const managedConfigReady = storageProtection.then(applyManagedBotAPIConfig);
let outboxOperation = Promise.resolve();
let stateOperation = Promise.resolve();
let commandPollInFlight = null;
let nextSendAllowedAt = 0;
const recentlyForwarded = new Map();

chrome.runtime.onInstalled.addListener(async () => {
  await managedConfigReady;
  const existing = await chrome.storage.sync.get(DEFAULTS);
  const policy = await chrome.storage.sync.get({
    monitoredMeetLinks: MMMeetPolicy.DEFAULT_MEET_CODES
  });
  let monitoredMeetCodes;
  try {
    monitoredMeetCodes = MMMeetPolicy.normalizeMeetCodes(
      policy.monitoredMeetLinks
    );
  } catch {
    monitoredMeetCodes = MMMeetPolicy.DEFAULT_MEET_CODES;
  }
  await chrome.storage.sync.set({
    ...DEFAULTS,
    ...existing,
    sendingEnabled: true,
    monitoredMeetLinks: monitoredMeetCodes
  });
  createHeartbeatAlarm();
  createUpdateAlarm();
});

createHeartbeatAlarm();
createUpdateAlarm();
refreshMeetActions().catch(() => {});
refreshTabsAfterVersionChange().catch(() => {});
repairMonitoredTabs().catch(() => {});

chrome.tabs.onUpdated?.addListener((tabId, change, tab) => {
  if (change.url || change.status === 'complete') {
    updateMeetAction(tabId, tab?.url || change.url).catch(() => {});
  }
  if (change.status === 'complete') {
    ensureMonitorContentScript(tabId, tab?.url).catch(() => {});
  }
});
chrome.tabs.onActivated?.addListener(({ tabId }) => {
  updateMeetAction(tabId).catch(() => {});
});

chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg?.kind === 'getMonitorModel') {
    if (!isTrustedExtensionView(_sender)) {
      sendResponse(null);
      return false;
    }
    getMonitorModel(msg.tabId).then(sendResponse).catch(() => sendResponse(null));
    return true;
  }

  if (msg?.kind === 'setTabMonitoring') {
    if (!isTrustedExtensionView(_sender) || !Number.isInteger(msg.tabId)) {
      sendResponse({ saved: false });
      return false;
    }
    setTabMonitoring(msg.tabId, msg.enabled === true)
      .then(saved => sendResponse({ saved }))
      .catch(() => sendResponse({ saved: false }));
    return true;
  }

  if (msg?.kind === 'setForwarding') {
    if (!isTrustedExtensionView(_sender)) {
      sendResponse({ saved: false });
      return false;
    }
    chrome.storage.sync.set({ sendingEnabled: msg.enabled === true })
      .then(() => broadcastMonitorModel())
      .then(() => sendResponse({ saved: true }))
      .catch(() => sendResponse({ saved: false }));
    return true;
  }

  if (msg?.kind === 'setDebugMode') {
    if (!isTrustedExtensionView(_sender)) {
      sendResponse({ saved: false });
      return false;
    }
    setDebugMode(msg.enabled === true)
      .then(() => broadcastMonitorModel())
      .then(() => sendResponse({ saved: true }))
      .catch(() => sendResponse({ saved: false }));
    return true;
  }

  if (msg?.kind === 'clearMonitorHistory') {
    if (!isTrustedExtensionView(_sender)) {
      sendResponse({ cleared: false });
      return false;
    }
    clearMonitorHistory()
      .then(() => broadcastMonitorModel())
      .then(() => sendResponse({ cleared: true }))
      .catch(() => sendResponse({ cleared: false }));
    return true;
  }

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

  if (msg?.kind === 'monitorObservation') {
    withStateLock(() => receiveObservation(msg, _sender))
      .then(sendResponse)
      .catch(() => sendResponse({ elected: false }));
    return true;
  }

  if (msg?.kind === 'event') {
    recordAndForwardEvent(msg.rec, _sender)
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
  if (alarm.name === HEARTBEAT_ALARM) {
    withStateLock(sendHeartbeat).catch(() => {
      console.warn('Meet state heartbeat failed');
    });
    repairMonitoredTabs().catch(() => {
      console.warn('Meet content-script recovery failed');
    });
  }
  if (alarm.name === STATE_RETRY_ALARM) {
    withStateLock(retryPendingSnapshot).catch(() => {
      console.warn('Meet state retry failed');
    });
  }
  if (alarm.name === UPDATE_ALARM) {
    checkForExtensionUpdate().catch(() => {
      console.warn('Meet Monitor update check failed');
    });
  }
});

chrome.runtime.onStartup.addListener(() => {
  flushBotAPIOutbox().catch(() => {
    console.warn('Meet event bot API startup retry failed');
  });
  withStateLock(restorePendingSnapshot).catch(() => {
    console.warn('Meet state startup retry failed');
  });
  checkForExtensionUpdate().catch(() => {
    console.warn('Meet Monitor startup update check failed');
  });
});

chrome.storage.onChanged.addListener((changes, areaName) => {
  if (areaName === 'sync' && changes.monitoredMeetLinks) {
    refreshMeetActions().catch(() => {});
    repairMonitoredTabs().catch(() => {});
  }
  if (areaName === 'sync' && changes.sendingEnabled) {
    withStateLock(() => queueCurrentSnapshot(true)).catch(() => {
      console.warn('Meet state forwarding update failed');
    });
    if (changes.sendingEnabled.newValue === true) {
      flushBotAPIOutbox().catch(() => {
        console.warn('Meet event bot API re-enable retry failed');
      });
    }
    broadcastMonitorModel();
  }
});

async function updateMeetAction(tabId, knownURL) {
  let url = knownURL;
  if (!url) {
    try {
      url = (await chrome.tabs.get(tabId)).url;
    } catch {
      return;
    }
  }
  const links = await monitoredMeetLinks();
  const allowed = MMMeetPolicy.isMonitoredMeetURL(url, links);
  await chrome.action.enable(tabId);
  try {
    await chrome.sidePanel?.setOptions?.({
      tabId,
      path: 'src/side-panel.html',
      enabled: allowed
    });
  } catch {
    // Keep the toolbar popup usable even if this Chrome build rejects
    // per-tab side panel configuration.
  }
}

async function refreshMeetActions() {
  const tabs = await chrome.tabs.query({});
  await Promise.all(tabs.map(tab =>
    Number.isInteger(tab.id) ? updateMeetAction(tab.id, tab.url) : null
  ));
}

async function ensureMonitorContentScript(tabId, knownURL) {
  if (!Number.isInteger(tabId) || !chrome.scripting?.executeScript) return false;
  let url = knownURL;
  if (!url) {
    try {
      url = (await chrome.tabs.get(tabId)).url;
    } catch {
      return false;
    }
  }
  const links = await monitoredMeetLinks();
  if (!MMMeetPolicy.isMonitoredMeetURL(url, links)) return false;
  try {
    const response = await chrome.tabs.sendMessage(tabId, { kind: 'monitorPing' });
    if (response?.alive) return false;
  } catch {
    // A missing receiver is repaired below.
  }
  await chrome.scripting.executeScript({
    target: { tabId },
    files: CONTENT_SCRIPT_FILES
  });
  return true;
}

async function repairMonitoredTabs() {
  if (!chrome.scripting?.executeScript) return;
  const tabs = await chrome.tabs.query({});
  await Promise.all(tabs.map(tab =>
    ensureMonitorContentScript(tab.id, tab.url).catch(() => false)
  ));
}

async function getMonitorModel(tabId) {
  const [sessionStored, localStored, policy] = await Promise.all([
    chrome.storage.session.get({
      [SESSION_KEY]: null,
      [DEBUG_ENABLED_KEY]: false,
      [DEBUG_HISTORY_KEY]: []
    }),
    chrome.storage.local.get({ [HISTORY_KEY]: [] }),
    chrome.storage.sync.get({ sendingEnabled: true })
  ]);
  const session = sessionStored[SESSION_KEY];
  let tabState = null;
  if (Number.isInteger(tabId)) {
    try {
      tabState = await chrome.tabs.sendMessage(tabId, { kind: 'getState' });
    } catch {
      tabState = null;
    }
  }
  const selectedMeeting = tabState?.meeting || session?.meeting || ALLOWED_MEETING;
  const result = safeServerResult(session?.lastServerResult);
  const error = safeServerError(session?.lastServerError);
  const now = Date.now();
  const lastSuccessAt = Number(session?.lastServerSuccessAt) || 0;
  const lastErrorAt = Number(error?.timestamp) || 0;
  const successFresh = lastSuccessAt > 0 &&
    now - lastSuccessAt <= SOURCE_STALE_MS;
  const errorFresh = lastErrorAt > 0 &&
    now - lastErrorAt <= SOURCE_STALE_MS;
  const apiState = errorFresh && lastErrorAt >= lastSuccessAt
    ? 'offline'
    : successFresh
      ? 'online'
      : 'unknown';
  const tabEnabled = Number.isInteger(tabId)
    ? await isTabMonitoringEnabled(tabId)
    : true;
  return {
    meeting: selectedMeeting,
    extensionVersion: chrome.runtime.getManifest().version,
    forwardingEnabled: tabEnabled,
    debugEnabled: sessionStored[DEBUG_ENABLED_KEY] === true,
    content: {
      online: Boolean(tabState) ||
        (session?.meeting === selectedMeeting && isFreshSession(session)),
      lastObservedAt: tabState
        ? Date.now()
        : session?.lastObservedAt || null,
      sourceTabId: Number.isInteger(tabId)
        ? tabId
        : Number.isInteger(session?.sourceTabId)
          ? session.sourceTabId
          : null
    },
    api: {
      state: apiState,
      online: apiState === 'unknown' ? null : apiState === 'online',
      lastSuccessAt: lastSuccessAt || null,
      serverTime: result?.serverTime || null,
      result,
      error
    },
    whatsAppConnected: apiState === 'online' && result
      ? result.whatsAppConnected
      : null,
    roster: tabState?.roster || (
      session?.meeting === selectedMeeting ? session?.roster : {}
    ) || {},
    history: boundedArray(localStored[HISTORY_KEY], MAX_HISTORY_SIZE)
      .filter(entry => entry?.meeting === selectedMeeting),
    debugHistory: sessionStored[DEBUG_ENABLED_KEY] === true
      ? boundedArray(sessionStored[DEBUG_HISTORY_KEY], MAX_DEBUG_HISTORY_SIZE)
      : []
  };
}

function safeServerResult(result) {
  if (!result) return null;
  return {
    status: Number(result.status) || 0,
    acceptedSequence: Number(result.acceptedSequence) || 0,
    serverTime: result.serverTime || null,
    monitorOnline: Boolean(result.monitorOnline),
    whatsAppConnected: Boolean(result.whatsAppConnected),
    queuedChanges: Number(result.queuedChanges) || 0
  };
}

function safeServerError(error) {
  if (!error) return null;
  return {
    category: String(error.category || 'unknown'),
    status: Number(error.status) || 0,
    timestamp: Number(error.timestamp) || null
  };
}

async function broadcastMonitorModel() {
  const model = await getMonitorModel();
  try {
    await chrome.runtime.sendMessage({ kind: 'monitorModelChanged', model });
  } catch {
    // Extension views are not always open.
  }
}

async function setDebugMode(enabled) {
  await chrome.storage.session.set({ [DEBUG_ENABLED_KEY]: enabled });
  if (!enabled) await chrome.storage.session.remove(DEBUG_HISTORY_KEY);
}

async function clearMonitorHistory() {
  await Promise.all([
    chrome.storage.local.set({ [HISTORY_KEY]: [] }),
    chrome.storage.session.remove(DEBUG_HISTORY_KEY)
  ]);
}

function boundedArray(value, maximum) {
  return Array.isArray(value) ? value.slice(-maximum) : [];
}

async function recordAndForwardEvent(rec, sender) {
  if (rec?.type === 'chat_message' &&
      String(rec?.name || '').trim().toLowerCase() === 'you') {
    return false;
  }
  if (!await electedEventAllowed(rec, sender)) return false;
  const isChat = rec?.type === 'chat_message';
  const directSecondaryEvent = !isChat && !await isPrimaryMeeting(rec?.meeting);
  if (isChat) {
    const whatsAppText = meetWhatsAppText(rec.text);
    if (whatsAppText !== null) {
      rec = { ...rec, text: whatsAppText };
    } else if (!await debugReflectionEnabled()) {
      await appendHistory(rec, null, 'silent');
      await broadcastMonitorModel();
      return false;
    }
  }
  const historyId = isChat || directSecondaryEvent
    ? await appendHistory(rec, null, 'pending')
    : null;
  const queued = await forwardEvent(rec, directSecondaryEvent);
  if (historyId) {
    await updateHistoryDelivery(
      [historyId],
      queued ? 'accepted' : 'silent'
    );
    await broadcastMonitorModel();
  }
  return queued;
}

function meetWhatsAppText(text) {
  const match = String(text || '').match(/^\/w\s+([\s\S]*\S)\s*$/i);
  return match ? match[1] : null;
}

async function debugReflectionEnabled() {
  const stored = await chrome.storage.session.get({
    [DEBUG_ENABLED_KEY]: false
  });
  return stored[DEBUG_ENABLED_KEY] === true;
}

async function forwardEvent(rec, directNonChat = false) {
  const now = Date.now();
  for (const [key, seenAt] of recentlyForwarded) {
    if (now - seenAt >= FORWARD_DEDUPE_MS) recentlyForwarded.delete(key);
  }
  const key = MMDelivery.forwardingKey(rec);
  if (recentlyForwarded.has(key)) return false;
  recentlyForwarded.set(key, now);

  forwardWebhook(rec).catch(() => {});
  if (rec?.type !== 'chat_message' && !directNonChat) return false;
  const queued = await enqueueBotAPI(rec);
  if (queued) await flushBotAPIOutbox();
  return queued;
}

async function forwardWebhook(rec) {
  const { webhook } = await chrome.storage.sync.get({ webhook: '' });
  if (!webhook) return;
  if (!await outboundAllowed(rec)) return;
  await fetch(webhook, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(rec)
  });
}

async function applyManagedBotAPIConfig() {
  let config;
  try {
    const response = await fetch(
      chrome.runtime.getURL('src/local-config.json'),
      { cache: 'no-store' }
    );
    if (!response.ok) return;
    config = await response.json();
  } catch {
    return;
  }
  if (!MMDelivery.isAllowedBotAPIURL(config?.url) ||
      typeof config?.token !== 'string' ||
      !config.token) {
    return;
  }
  const managed = {
    url: config.url,
    token: config.token,
    generation: String(config.generation || 'managed-local')
  };
  const stored = await chrome.storage.local.get({ botAPIConfig: null });
  if (stored.botAPIConfig?.url === managed.url &&
      stored.botAPIConfig?.token === managed.token &&
      stored.botAPIConfig?.generation === managed.generation) {
    return;
  }
  await chrome.storage.local.set({ botAPIConfig: managed });
}

async function botAPIConfig() {
  await storageProtection;
  await applyManagedBotAPIConfig();
  const { botAPIConfig } = await chrome.storage.local.get({ botAPIConfig: null });
  return {
    botAPIURL: botAPIConfig?.url || '',
    botAPIToken: botAPIConfig?.token || '',
    generation: botAPIConfig?.generation || ''
  };
}

async function monitoredMeetLinks() {
  const stored = await chrome.storage.sync.get({
    monitoredMeetLinks: MMMeetPolicy.DEFAULT_MEET_CODES
  });
  try {
    return MMMeetPolicy.normalizeMeetCodes(stored.monitoredMeetLinks);
  } catch {
    return MMMeetPolicy.DEFAULT_MEET_CODES;
  }
}

async function isAllowedMeeting(meeting) {
  const links = await monitoredMeetLinks();
  return links.some(link => MMMeetPolicy.meetingCode(link) === meeting);
}

async function isPrimaryMeeting(meeting) {
  const links = await monitoredMeetLinks();
  return MMMeetPolicy.meetingCode(links[0]) === meeting;
}

async function isTabMonitoringEnabled(tabId) {
  const stored = await chrome.storage.session.get({
    [TAB_MONITORING_KEY]: {}
  });
  return stored[TAB_MONITORING_KEY]?.[String(tabId)] !== false;
}

async function setTabMonitoring(tabId, enabled) {
  const tab = await chrome.tabs.get(tabId);
  const links = await monitoredMeetLinks();
  if (!MMMeetPolicy.isMonitoredMeetURL(tab.url, links)) return false;
  const stored = await chrome.storage.session.get({
    [TAB_MONITORING_KEY]: {}
  });
  const states = stored[TAB_MONITORING_KEY] || {};
  states[String(tabId)] = enabled;
  await chrome.storage.session.set({ [TAB_MONITORING_KEY]: states });
  return true;
}

async function tabCanMonitor(sender, meeting) {
  const tabId = sender?.tab?.id;
  if (!Number.isInteger(tabId) || !await isAllowedMeeting(meeting)) return false;
  return isTabMonitoringEnabled(tabId);
}

function enqueueBotAPI(rec) {
  return withOutboxLock(async () => {
    const { botAPIURL, botAPIToken, generation } = await botAPIConfig();
    if (!MMDelivery.eventRequestFor(rec, botAPIURL, botAPIToken)) return false;

    const stored = await chrome.storage.local.get({ [OUTBOX_KEY]: [] });
    const outbox = stored[OUTBOX_KEY];
    const key = MMDelivery.eventKey(rec);
    if (!outbox.some(entry => entry.key === key)) {
      if (!await outboundAllowed(rec)) return false;
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

async function outboundAllowed(rec) {
  const { sendingEnabled } = await chrome.storage.sync.get({
    sendingEnabled: true
  });
  return sendingEnabled && await isAllowedMeeting(rec?.meeting);
}

function flushBotAPIOutbox() {
  return withOutboxLock(async () => {
    const stored = await chrome.storage.local.get({ [OUTBOX_KEY]: [] });
    const outbox = stored[OUTBOX_KEY];
    const allowedOutbox = [];
    for (const entry of outbox) {
      const primary = await isPrimaryMeeting(entry.rec?.meeting);
      if (await isAllowedMeeting(entry.rec?.meeting) &&
          (entry.rec?.type === 'chat_message' || !primary)) {
        allowedOutbox.push(entry);
      }
    }
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

      const request = MMDelivery.eventRequestFor(
        entry.rec,
        botAPIURL,
        botAPIToken
      );
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
      await withStateLock(clearPendingSnapshot);
      return true;
    }
    if (!MMDelivery.eventRequestFor({}, url, token)) return false;
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
    await withStateLock(async () => {
      await clearPendingSnapshot();
      await queueCurrentSnapshot(true);
    });
    return true;
  });
}

function withStateLock(operation) {
  stateOperation = stateOperation.then(operation, operation);
  return stateOperation;
}

async function receiveObservation(message, sender) {
  const tabId = sender?.tab?.id;
  if (!Number.isInteger(tabId) ||
      !message.roster ||
      typeof message.roster !== 'object' ||
      Array.isArray(message.roster)) {
    return { elected: false };
  }
  if (!await tabCanMonitor(sender, message.meeting)) {
    return { elected: false };
  }
  if (!await isPrimaryMeeting(message.meeting)) {
    await recordRawObservation(message, tabId, Date.now());
    return { elected: true };
  }

  const now = Date.now();
  const stored = await chrome.storage.session.get({ [SESSION_KEY]: null });
  let session = stored[SESSION_KEY];
  const takeover = session && session.sourceTabId !== tabId &&
    now - session.lastObservedAt > SOURCE_STALE_MS;

  if (session && session.sourceTabId !== tabId && !takeover) {
    return { elected: false };
  }

  if (!session || takeover) {
    session = newMonitorSession(tabId, message, now);
    await chrome.storage.session.remove(PENDING_SNAPSHOT_KEY);
  } else {
    session.lastObservedAt = now;
    session.roster = message.roster;
    session.self = message.self || {};
  }

  await recordRawObservation(message, tabId, now);
  await chrome.storage.session.set({ [SESSION_KEY]: session });
  const sequence = session.sequence + 1;
  const historyIds = [];
  for (const change of Array.isArray(message.changes) ? message.changes : []) {
    if (change?.type === 'chat_message') continue;
    historyIds.push(await appendHistory(change, sequence, 'pending'));
  }
  const accepted = await queueSnapshotForSession(session, false);
  if (historyIds.length) {
    const deliveryState = accepted
      ? 'accepted'
      : session.lastServerError
        ? 'failed'
        : 'silent';
    await updateHistoryDelivery(historyIds, deliveryState);
  }
  await broadcastMonitorModel();
  pollMeetCommand(tabId).catch(() => {
    console.warn('Meet command polling failed');
  });
  return { elected: true };
}

function pollMeetCommand(tabId) {
  if (commandPollInFlight) return commandPollInFlight;
  commandPollInFlight = performMeetCommandPoll(tabId)
    .finally(() => {
      commandPollInFlight = null;
    });
  return commandPollInFlight;
}

async function performMeetCommandPoll(tabId) {
  const config = await botAPIConfig();
  const request = MMDelivery.commandRequestFor(
    config.botAPIURL,
    config.botAPIToken
  );
  if (!request) return false;

  const response = await fetchWithTimeout(request);
  if (response.status === 204) return false;
  if (!response.ok) return false;

  const command = await response.json();
  const id = String(command?.id || '').trim();
  const text = String(command?.text || '').trim();
  if (!id || !text) return false;

  const stored = await chrome.storage.local.get({ [POSTED_COMMANDS_KEY]: [] });
  const posted = boundedArray(stored[POSTED_COMMANDS_KEY], 100);
  if (!posted.includes(id)) {
    const result = await chrome.tabs.sendMessage(tabId, {
      kind: 'postMeetMessage',
      id,
      text
    });
    if (!result?.sent) return false;
    posted.push(id);
    await chrome.storage.local.set({
      [POSTED_COMMANDS_KEY]: boundedArray(posted, 100)
    });
  }

  const acknowledgement = MMDelivery.commandAckRequestFor(
    id,
    config.botAPIURL,
    config.botAPIToken
  );
  if (!acknowledgement) return false;
  const acknowledged = await fetchWithTimeout(acknowledgement);
  return acknowledged.ok;
}

async function recordRawObservation(message, sourceTabId, observedAt) {
  const stored = await chrome.storage.session.get({
    [DEBUG_ENABLED_KEY]: false,
    [DEBUG_HISTORY_KEY]: []
  });
  if (stored[DEBUG_ENABLED_KEY] !== true) return;
  const history = boundedArray(stored[DEBUG_HISTORY_KEY], MAX_DEBUG_HISTORY_SIZE);
  history.push({
    observedAt,
    sourceTabId,
    meeting: message.meeting,
    rawRoster: message.rawRoster || {}
  });
  await chrome.storage.session.set({
    [DEBUG_HISTORY_KEY]: boundedArray(history, MAX_DEBUG_HISTORY_SIZE)
  });
}

async function appendHistory(rec, sequence, deliveryState) {
  const stored = await chrome.storage.local.get({ [HISTORY_KEY]: [] });
  const history = boundedArray(stored[HISTORY_KEY], MAX_HISTORY_SIZE);
  const historyId = crypto.randomUUID();
  history.push({
    historyId,
    ts: rec?.ts || new Date(Date.now()).toISOString(),
    type: String(rec?.type || 'unknown'),
    id: String(rec?.id || ''),
    name: rec?.name == null ? null : String(rec.name),
    state: eventState(rec),
    text: rec?.text == null ? null : String(rec.text),
    meeting: String(rec?.meeting || ALLOWED_MEETING),
    sequence: Number.isInteger(sequence) ? sequence : null,
    deliveryState
  });
  await chrome.storage.local.set({
    [HISTORY_KEY]: boundedArray(history, MAX_HISTORY_SIZE)
  });
  return historyId;
}

function eventState(rec) {
  if (rec?.state != null) return String(rec.state);
  const state = {};
  for (const key of ['cam', 'mic', 'self', 'host']) {
    if (rec?.[key] != null) state[key] = rec[key];
  }
  return Object.keys(state).length ? state : null;
}

async function updateHistoryDelivery(historyIds, deliveryState) {
  const selected = new Set(historyIds);
  const stored = await chrome.storage.local.get({ [HISTORY_KEY]: [] });
  const history = boundedArray(stored[HISTORY_KEY], MAX_HISTORY_SIZE)
    .map(entry => selected.has(entry.historyId)
      ? { ...entry, deliveryState }
      : entry);
  await chrome.storage.local.set({ [HISTORY_KEY]: history });
}

function newMonitorSession(sourceTabId, message, now) {
  return {
    sessionId: crypto.randomUUID(),
    sourceTabId,
    sequence: 0,
    meeting: message.meeting,
    lastObservedAt: now,
    roster: message.roster,
    self: message.self || {},
    lastStateKey: '',
    acceptedSequence: 0,
    lastServerResult: null,
    lastServerSuccessAt: null,
    lastServerError: null
  };
}

async function queueCurrentSnapshot(force) {
  const stored = await chrome.storage.session.get({ [SESSION_KEY]: null });
  const session = stored[SESSION_KEY];
  if (!isFreshSession(session) ||
      !await isTabMonitoringEnabled(session.sourceTabId)) return false;
  return queueSnapshotForSession(session, force);
}

async function queueSnapshotForSession(session, force, heartbeat = false) {
  const config = await botAPIConfig();
  const policy = await chrome.storage.sync.get({ sendingEnabled: true });
  const pendingStored = await chrome.storage.session.get({
    [PENDING_SNAPSHOT_KEY]: null
  });
  const pending = pendingStored[PENDING_SNAPSHOT_KEY];

  if (heartbeat && pending?.sessionId === session.sessionId) {
    return flushPendingSnapshot(session, pending);
  }

  const sequence = session.sequence + 1;
  const tabEnabled = await isTabMonitoringEnabled(session.sourceTabId);
  const body = MMSnapshot.build({
    meeting: session.meeting,
    sessionId: session.sessionId,
    sequence,
    capturedAt: new Date(Date.now()).toISOString(),
    extensionVersion: chrome.runtime.getManifest().version,
    forwardingEnabled: policy.sendingEnabled && tabEnabled,
    roster: session.roster
  });
  const stateKey = MMSnapshot.stateKey(body);
  const request = MMDelivery.stateRequestFor(
    body,
    config.botAPIURL,
    config.botAPIToken
  );
  if (!request) return false;

  if (!force) {
    if (pending?.sessionId === session.sessionId &&
        pending.stateKey === stateKey &&
        pending.generation === config.generation) {
      return false;
    }
    if (!pending && session.lastStateKey === stateKey) return false;
  }

  session.sequence = sequence;
  const next = {
    sessionId: session.sessionId,
    sequence,
    stateKey,
    body,
    generation: config.generation,
    attempts: 0,
    nextAttemptAt: 0
  };
  await chrome.storage.session.set({
    [SESSION_KEY]: session,
    [PENDING_SNAPSHOT_KEY]: next
  });
  return flushPendingSnapshot(session, next);
}

async function sendHeartbeat() {
  const stored = await chrome.storage.session.get({
    [SESSION_KEY]: null,
    [PENDING_SNAPSHOT_KEY]: null
  });
  const session = stored[SESSION_KEY];
  if (!isFreshSession(session) ||
      !await isTabMonitoringEnabled(session.sourceTabId)) return false;
  if (stored[PENDING_SNAPSHOT_KEY]?.sessionId === session.sessionId) {
    return flushPendingSnapshot(session, stored[PENDING_SNAPSHOT_KEY]);
  }
  return queueSnapshotForSession(session, true, true);
}

async function retryPendingSnapshot() {
  const stored = await chrome.storage.session.get({
    [SESSION_KEY]: null,
    [PENDING_SNAPSHOT_KEY]: null
  });
  const session = stored[SESSION_KEY];
  const pending = stored[PENDING_SNAPSHOT_KEY];
  if (!pending || !isFreshSession(session) ||
      pending.sessionId !== session.sessionId) {
    return false;
  }
  const delay = pending.nextAttemptAt - Date.now();
  if (delay > 0) {
    scheduleStateRetry(delay);
    return false;
  }
  return flushPendingSnapshot(session, pending);
}

async function restorePendingSnapshot() {
  await sanitizeOutbox();
  return retryPendingSnapshot();
}

async function flushPendingSnapshot(session, pending) {
  const config = await botAPIConfig();
  if (pending.generation !== config.generation) {
    await chrome.storage.session.remove(PENDING_SNAPSHOT_KEY);
    return false;
  }
  const request = MMDelivery.stateRequestFor(
    pending.body,
    config.botAPIURL,
    config.botAPIToken
  );
  if (!request) return false;

  let response;
  try {
    response = await fetchWithTimeout(request);
  } catch {
    response = null;
  }

  if (response?.ok) {
    let result = {};
    try {
      result = typeof response.json === 'function' ? await response.json() : {};
    } catch {
      result = {};
    }
    await chrome.storage.session.remove(PENDING_SNAPSHOT_KEY);
    session.lastStateKey = pending.stateKey;
    session.acceptedSequence = Number(result.acceptedSequence) ||
      pending.sequence;
    session.lastServerResult = serverResultMetadata(
      response.status,
      result,
      session.acceptedSequence
    );
    session.lastServerSuccessAt = Date.now();
    session.lastServerError = null;
    await chrome.storage.session.set({ [SESSION_KEY]: session });
    await broadcastMonitorModel();
    return true;
  }

  const status = response?.status || 0;
  if (!response || isRetryableStateStatus(status)) {
    pending.attempts += 1;
    if (pending.attempts >= MAX_SNAPSHOT_ATTEMPTS) {
      await chrome.storage.session.remove(PENDING_SNAPSHOT_KEY);
      setServerError(session, 'retry_limit', status);
    } else {
      const delay = MMDelivery.retryDelay(pending.attempts - 1);
      pending.nextAttemptAt = Date.now() + delay;
      await chrome.storage.session.set({ [PENDING_SNAPSHOT_KEY]: pending });
      setServerError(session, response ? 'retryable_http' : 'unreachable', status);
      scheduleStateRetry(delay);
    }
  } else {
    await chrome.storage.session.remove(PENDING_SNAPSHOT_KEY);
    setServerError(session, stateClientError(status), status);
  }
  await chrome.storage.session.set({ [SESSION_KEY]: session });
  await broadcastMonitorModel();
  return false;
}

function serverResultMetadata(status, result, fallbackSequence) {
  return {
    status,
    acceptedSequence: Number(result?.acceptedSequence) || fallbackSequence,
    serverTime: result?.serverTime || null,
    monitorOnline: Boolean(result?.monitorOnline),
    whatsAppConnected: Boolean(result?.whatsAppConnected),
    queuedChanges: Number(result?.queuedChanges) || 0
  };
}

function setServerError(session, category, status) {
  session.lastServerError = {
    category,
    status: status || 0,
    timestamp: Date.now()
  };
}

function stateClientError(status) {
  if (status === 400) return 'invalid_snapshot';
  if (status === 401) return 'unauthorized';
  if (status === 403) return 'origin_forbidden';
  if (status === 409) return 'sequence_conflict';
  return 'client_error';
}

function isRetryableStateStatus(status) {
  return [408, 425, 429].includes(status) || status >= 500;
}

function isFreshSession(session) {
  return Boolean(session?.meeting) &&
    Date.now() - session.lastObservedAt <= SOURCE_STALE_MS;
}

async function electedEventAllowed(rec, sender) {
  if (!await outboundAllowed(rec)) return false;
  if (!await tabCanMonitor(sender, rec?.meeting)) return false;
  if (!await isPrimaryMeeting(rec?.meeting)) return true;
  const stored = await chrome.storage.session.get({ [SESSION_KEY]: null });
  const session = stored[SESSION_KEY];
  return isFreshSession(session) && session.sourceTabId === sender.tab.id;
}

async function clearPendingSnapshot() {
  await chrome.storage.session.remove(PENDING_SNAPSHOT_KEY);
}

async function sanitizeOutbox() {
  return withOutboxLock(async () => {
    const stored = await chrome.storage.local.get({ [OUTBOX_KEY]: [] });
    const filtered = [];
    for (const entry of stored[OUTBOX_KEY]) {
      const primary = await isPrimaryMeeting(entry.rec?.meeting);
      if (await isAllowedMeeting(entry.rec?.meeting) &&
          (entry.rec?.type === 'chat_message' || !primary)) {
        filtered.push(entry);
      }
    }
    if (filtered.length !== stored[OUTBOX_KEY].length) {
      await chrome.storage.local.set({ [OUTBOX_KEY]: filtered });
    }
  });
}

function createHeartbeatAlarm() {
  chrome.alarms.create(HEARTBEAT_ALARM, { periodInMinutes: HEARTBEAT_MS / 60_000 });
}

function createUpdateAlarm() {
  chrome.alarms.create(UPDATE_ALARM, { periodInMinutes: 1 });
}

async function checkForExtensionUpdate() {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 10_000);
  try {
    const response = await fetch(MMVersionUpdate.manifestURL, {
      cache: 'no-store',
      signal: controller.signal
    });
    if (!response.ok) return false;
    const manifest = await response.json();
    const current = chrome.runtime.getManifest().version;
    if (!MMVersionUpdate.isNewer(current, manifest?.version)) return false;
    await chrome.storage.local.set({
      [PENDING_VERSION_KEY]: manifest.version
    });
    chrome.runtime.reload();
    return true;
  } finally {
    clearTimeout(timer);
  }
}

async function refreshTabsAfterVersionChange() {
  const current = chrome.runtime.getManifest().version;
  const stored = await chrome.storage.local.get({
    [RUNNING_VERSION_KEY]: '',
    [PENDING_VERSION_KEY]: ''
  });
  const previous = stored[RUNNING_VERSION_KEY];
  if (!previous) {
    await chrome.storage.local.set({ [RUNNING_VERSION_KEY]: current });
    if (stored[PENDING_VERSION_KEY] === current) {
      await chrome.storage.local.remove(PENDING_VERSION_KEY);
    }
    return false;
  }
  if (previous === current) return false;

  await chrome.storage.local.set({ [RUNNING_VERSION_KEY]: current });
  if (stored[PENDING_VERSION_KEY] === current) {
    await chrome.storage.local.remove(PENDING_VERSION_KEY);
  }
  const links = await monitoredMeetLinks();
  const tabs = await chrome.tabs.query({});
  await Promise.all(tabs.map(tab =>
    Number.isInteger(tab.id) &&
    MMMeetPolicy.isMonitoredMeetURL(tab.url, links)
      ? chrome.tabs.reload(tab.id)
      : null
  ));
  return true;
}

function scheduleStateRetry(delay) {
  chrome.alarms.create(STATE_RETRY_ALARM, {
    when: Date.now() + Math.max(0, delay)
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

function isTrustedExtensionView(sender) {
  if (sender?.id !== chrome.runtime.id || typeof sender?.url !== 'string') {
    return false;
  }
  return [
    'src/side-panel.html',
    'src/popup.html',
    'src/options.html'
  ].some(path => sender.url === chrome.runtime.getURL(path));
}

storageProtection
  .then(() => Promise.all([
    flushBotAPIOutbox(),
    withStateLock(restorePendingSnapshot)
  ]))
  .catch(() => {
    console.warn('Meet Monitor could not protect local credential storage');
  });
