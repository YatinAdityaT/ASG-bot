(() => {
  const S = window.MMSelectors;
  if (!S || window.__mmLoaded) return;
  window.__mmLoaded = true;

  const meeting = S.meetingCode();
  const participantTracker = new window.MMParticipantTracker();
  const roster = participantTracker.roster;
  const events = [];
  const seenChats = new Set();
  const recentLeaveAnnouncements = new Map();
  const MAX = 2000;

  const emit = (type, id, cur) => {
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
    chrome.runtime.sendMessage({ kind: 'event', rec }).catch(() => {});
  };

  const scan = () => {
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
      info.mic = (myState.mic === 'off') ? 'muted' : 'unmuted';
      info.cam = (myState.cam === 'on');
      break;
    }

    for (const change of participantTracker.update(now)) {
      emit(change.type, change.id, change.cur);
    }

    const observedAt = Date.now();
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
        seenChats.add(msgId);

        const textEl = row.querySelector(S.chatTextSel);
        const text = textEl ? textEl.innerText.trim() : '';
        if (text) emit('chat_message', msgId, { name: senderName, text });
      }
    }
  };

  let t = null;
  const schedule = () => {
    if (!t) t = setTimeout(() => {
      t = null;
      scan();
    }, 200);
  };
  new MutationObserver(schedule).observe(document.body, {
    childList: true, subtree: true, attributes: true, attributeFilter: ['data-is-muted']
  });
  setInterval(scan, 3000);
  scan();

  chrome.storage.sync.get({ consentBanner: true }).then(({ consentBanner }) => {
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
