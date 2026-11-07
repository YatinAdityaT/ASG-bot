(() => {
  const S = window.MMSelectors;
  if (!S) return;
  if (typeof window.__mmStop === 'function') window.__mmStop();

  const meeting = S.meetingCode();
  let participantTracker = new window.MMParticipantTracker(2000);
  let roster = participantTracker.roster;
  const events = [];
  let instanceId = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  let baselineUntil = Date.now() + 8000;
  const seenChats = new Set();
  const recentLeaveAnnouncements = new Map();
  const MAX = 2000;
  let confirmationTimer = null;
  let confirmationGeneration = 0;
  let mutationTimer = null;
  let scanInterval = null;
  let observer = null;
  let stopped = false;
  let chatOpened = false;

  const isContextInvalidated = error =>
    /extension context invalidated/i.test(error?.message || '');

  const stopStaleWork = () => {
    if (stopped) return;
    stopped = true;
    confirmationGeneration += 1;
    observer?.disconnect();
    observer = null;
    if (mutationTimer !== null) clearTimeout(mutationTimer);
    mutationTimer = null;
    if (confirmationTimer !== null) clearTimeout(confirmationTimer);
    confirmationTimer = null;
    if (scanInterval !== null) clearInterval(scanInterval);
    scanInterval = null;
    if (window.__mmStop === stopStaleWork) window.__mmStop = null;
  };
  window.__mmStop = stopStaleWork;

  const handleContextFailure = error => {
    if (!isContextInvalidated(error)) return false;
    stopStaleWork();
    return true;
  };

  const resetTrackingBaseline = () => {
    confirmationGeneration += 1;
    if (confirmationTimer !== null) clearTimeout(confirmationTimer);
    confirmationTimer = null;
    participantTracker = new window.MMParticipantTracker(2000);
    roster = participantTracker.roster;
    instanceId = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    baselineUntil = Date.now() + 8000;
  };

  const deliver = message => {
    if (stopped) return;
    let delivery;
    try {
      delivery = chrome.runtime.sendMessage(message);
    } catch (error) {
      if (!handleContextFailure(error)) throw error;
      return;
    }
    Promise.resolve(delivery).catch(error => {
      if (!handleContextFailure(error)) throw error;
    });
  };

  const emit = (type, id, cur) => {
    if (stopped) return;
    const rec = {
      type, id, meeting, ts: new Date().toISOString(),
      name: cur?.name ?? null,
      cam: cur?.cam ?? null,
      mic: cur?.mic ?? null,
      self: cur?.self ?? false,
      host: cur?.host ?? false,
      text: cur?.text ?? null
    };
    events.push(rec);
    if (events.length > MAX) events.splice(0, events.length - MAX);
    deliver({ kind: 'event', rec });
    return rec;
  };

  const scheduleConfirmation = () => {
    if (stopped) return;
    const generation = ++confirmationGeneration;
    if (confirmationTimer !== null) {
      clearTimeout(confirmationTimer);
      confirmationTimer = null;
    }
    const deadline = participantTracker.nextDeadline();
    if (deadline === null) return;
    confirmationTimer = setTimeout(() => {
      if (stopped || generation !== confirmationGeneration) return;
      confirmationTimer = null;
      scan();
    }, Math.max(0, deadline - Date.now()));
  };

  const scan = () => {
    if (stopped) return;
    if (window.MMMeetCall?.isInCall(document)) {
      chatOpened = window.MMMeetChat?.openChat(document) || chatOpened;
    }
    const groups = new Map();
    for (const tile of document.querySelectorAll(S.tileSel)) {
      const id = tile.getAttribute(S.idAttr);
      if (!id) continue;
      if (!groups.has(id)) groups.set(id, []);
      groups.get(id).push(tile);
    }

    const now = new Map();
    for (const [id, tiles] of groups) {
      now.set(id, {
        name: S.pickName(tiles),
        cam: tiles.some(t => S.camOf(t)),
        mic: S.micOf(tiles),
        self: tiles.some(t => S.detectSelf(t)),
        host: tiles.some(t => S.detectHost(t))
      });
    }

    const myState = S.selfState();
    for (const [, info] of now) {
      if (!info.self) continue;
      if (myState.mic === 'off') info.mic = 'muted';
      if (myState.mic === 'on') info.mic = 'unmuted';
      if (myState.cam === 'off') info.cam = false;
      if (myState.cam === 'on') info.cam = true;
      break;
    }

    const observedAt = Date.now();
    const baselining = observedAt < baselineUntil;
    const changes = [];
    for (const change of participantTracker.update(
      now,
      observedAt,
      baselining
    )) {
      const rec = emit(change.type, change.id, change.cur);
      if (rec) changes.push(rec);
    }

    if (!baselining) {
      for (const element of document.querySelectorAll(S.announcementSel)) {
        const text = (element.innerText || element.textContent || '').trim();
        const name = window.MMLeftParticipantName(text);
        if (!name || observedAt - (recentLeaveAnnouncements.get(text) || 0) < 10_000) continue;

        for (const [id, info] of roster) {
          if (info.name !== name) continue;
          const change = participantTracker.leave(id);
          if (change) {
            recentLeaveAnnouncements.set(text, observedAt);
            const rec = emit(change.type, change.id, change.cur);
            if (rec) changes.push(rec);
          }
          break;
        }
      }
    }
    for (const [text, timestamp] of recentLeaveAnnouncements) {
      if (observedAt - timestamp >= 60_000) recentLeaveAnnouncements.delete(text);
    }

    deliver({
      kind: 'monitorObservation',
      meeting,
      instanceId,
      baselining,
      observedAt,
      roster: Object.fromEntries(roster),
      rawRoster: Object.fromEntries(now),
      self: S.selfState(),
      changes
    });

    for (const group of document.querySelectorAll(S.chatGroupSel)) {
      const senderEl = group.querySelector(S.chatSenderSel);
      const senderName = senderEl ? senderEl.innerText.trim() : 'You';

      for (const row of group.querySelectorAll(S.chatMsgRowSel)) {
        const msgId = row.getAttribute('data-message-id');
        if (!msgId || seenChats.has(msgId)) continue;

        const textEl = row.querySelector(S.chatTextSel);
        const text = textEl ? textEl.innerText.trim() : '';
        if (!text) continue;
        emit('chat_message', msgId, { name: senderName, text });
        seenChats.add(msgId);
      }
    }

    scheduleConfirmation();
  };

  const schedule = () => {
    if (stopped || mutationTimer !== null) return;
    mutationTimer = setTimeout(() => {
      mutationTimer = null;
      scan();
    }, 200);
  };
  observer = new MutationObserver(schedule);
  observer.observe(document.body, {
    childList: true, subtree: true, attributes: true, attributeFilter: ['data-is-muted']
  });
  scanInterval = setInterval(scan, 3000);
  const meetPolicy = window.MMMeetPolicy;
  if (!meetPolicy) scan();

  chrome.storage.sync.get({
    consentBanner: true,
    monitoredMeetLinks: meetPolicy?.DEFAULT_MEET_CODES || []
  }).then(({ consentBanner, monitoredMeetLinks }) => {
    if (stopped) return;
    if (meetPolicy &&
        !meetPolicy.isMonitoredMeetURL(
          `https://meet.google.com/${meeting}`,
          monitoredMeetLinks
        )) {
      stopStaleWork();
      return;
    }
    if (meetPolicy) scan();
    if (!consentBanner) return;
    const b = document.createElement('div');
    b.textContent = 'This call is being monitored (join/leave, camera, mute, chat).';
    Object.assign(b.style, {
      position: 'fixed', bottom: '8px', left: '8px', zIndex: 2147483647,
      background: '#1a1a1a', color: '#fff', font: '12px system-ui',
      padding: '6px 10px', borderRadius: '6px', opacity: '0.85',
      pointerEvents: 'none'
    });
    document.body.appendChild(b);
  }).catch(error => {
    if (!handleContextFailure(error)) throw error;
  });

  chrome.runtime.onMessage.addListener((msg, _s, sendResponse) => {
    if (msg.kind === 'monitorPing') {
      scan();
      sendResponse({ alive: !stopped, meeting });
      return false;
    }
    if (msg.kind === 'setScheduledPresence') {
      if (msg.present === false) {
        window.MMMeetCall?.leave(document);
        sendResponse({ applied: true });
        return false;
      }
      const wasInCall = window.MMMeetCall?.isInCall(document) === true;
      Promise.resolve(window.MMMeetCall?.join(document) || false)
        .then(joined => {
          if (joined && !wasInCall) {
            resetTrackingBaseline();
            scan();
          }
          if (joined) {
            chatOpened = window.MMMeetChat?.openChat(document) || chatOpened;
          }
          sendResponse({ applied: joined });
        })
        .catch(() => sendResponse({ applied: false }));
      return true;
    }
    if (msg.kind === 'postMeetMessage') {
      window.MMMeetChat.postMessage(document, msg.text)
        .then(sent => sendResponse({ sent }))
        .catch(() => sendResponse({ sent: false }));
      return true;
    }
    if (msg.kind === 'getState') {
      sendResponse({
        meeting,
        self: S.selfState(),
        roster: Object.fromEntries(roster),
        events: events.slice(-500)
      });
    }
    if (msg.kind === 'getIdentityProbe') {
      sendResponse({
        generatedAt: new Date().toISOString(),
        meeting,
        ...window.MMIdentityProbe.inspect(document, S)
      });
    }
  });
})();
