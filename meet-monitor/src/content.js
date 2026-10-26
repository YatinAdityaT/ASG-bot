(() => {
  const S = window.MMSelectors;
  if (!S || window.__mmLoaded) return;
  window.__mmLoaded = true;

  const meeting = S.meetingCode();
  const roster = new Map();
  const events = [];
  const seenChats = new Set();
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

    for (const [id, cur] of now) {
      const prev = roster.get(id);
      if (!prev) {
        emit('join', id, cur);
      } else {
        if (prev.cam !== cur.cam) emit(cur.cam ? 'cam_on' : 'cam_off', id, cur);
        if (prev.mic !== cur.mic) emit(cur.mic === 'muted' ? 'mic_muted' : 'mic_unmuted', id, cur);
        if (cur.name && prev.name !== cur.name) emit('rename', id, cur);
      }
    }
    for (const [id, prev] of roster) if (!now.has(id)) emit('leave', id, prev);

    roster.clear();
    for (const [k, v] of now) roster.set(k, v);

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
