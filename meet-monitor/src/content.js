(() => {
  const S = window.MMSelectors;
  if (!S || window.__mmLoaded) return;
  window.__mmLoaded = true;

  const meeting = S.meetingCode();
  const participantTracker = new window.MMParticipantTracker(2000);
  const roster = participantTracker.roster;
  const events = [];
  const seenChats = new Set();
  const recentLeaveAnnouncements = new Map();
  const MAX = 2000;
  let confirmationTimer = null;
  let confirmationGeneration = 0;
  let mutationTimer = null;
  let scanInterval = null;
  let observer = null;
  let stopped = false;

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
  };

  const handleContextFailure = error => {
    if (!isContextInvalidated(error)) return false;
    stopStaleWork();
    return true;
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
    for (const change of participantTracker.update(now, observedAt)) {
      emit(change.type, change.id, change.cur);
    }

    for (const element of document.querySelectorAll(S.announcementSel)) {
      const text = (element.innerText || element.textContent || '').trim();
      const name = window.MMLeftParticipantName(text);
      if (!name || observedAt - (recentLeaveAnnouncements.get(text) || 0) < 10_000) continue;

      for (const [id, info] of roster) {
        if (info.name !== name) continue;
        const change = participantTracker.leave(id);
        if (change) {
          recentLeaveAnnouncements.set(text, observedAt);
          emit(change.type, change.id, change.cur);
        }
        break;
      }
    }
    for (const [text, timestamp] of recentLeaveAnnouncements) {
      if (observedAt - timestamp >= 60_000) recentLeaveAnnouncements.delete(text);
    }

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
  scan();
  if (stopped) return;

  chrome.storage.sync.get({ consentBanner: true }).then(({ consentBanner }) => {
    if (stopped || !consentBanner) return;
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
    if (msg.kind === 'getState') {
      sendResponse({
        meeting,
        self: S.selfState(),
        roster: Object.fromEntries(roster),
        events: events.slice(-500)
      });
    }
  });
})();
