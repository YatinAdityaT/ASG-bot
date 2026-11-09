// All Meet DOM assumptions live here. When Meet redesigns, fix only this file.
(function (root, factory) {
  const api = factory();
  root.MMSelectors = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => ({
  // --- PARTICIPANT SELECTORS ---
  tileSel: '[data-participant-id]',
  idAttr: 'data-participant-id',
  videoSel: 'video',
  nameSel: 'div[jsname="giiMnc"]',
  selfMicSel: 'button[jsname="hw0c9"]',
  selfCamSel: 'button[jsname="psRWwc"]',
  muteIconPathSel: 'path[d^="M19 11h"]', // The exact SVG path for the muted mic

  // --- CHAT SELECTORS ---
  chatGroupSel: '.eWHPEf',                   // Container for a person's consecutive messages
  chatMsgRowSel: '.RLrADb[data-message-id]', // The specific message row
  chatTextSel: 'div[jsname="dTKtvb"]',       // Exact text container (ignores buttons)
  chatSenderSel: '.poVWob',                  // Sender's name (absent if it's your message)
  announcementSel: '[aria-live="polite"], [aria-live="assertive"]',

  meetingCode() {
    const segs = location.pathname.split('/').filter(Boolean);
    return segs[segs.length - 1] || location.pathname;
  },

  nameOf(tile) {
    const nameElement = tile.querySelector(this.nameSel);
    const raw = nameElement?.innerText || nameElement?.textContent || '';
    const lines = raw
      .split(/\r?\n/)
      .map(line => line.replace(/\s+/g, ' ').trim())
      .filter(Boolean);
    return [...new Set(lines)].join(' ') || null;
  },

  pickName(tiles) {
    for (const tile of tiles) {
      const name = this.nameOf(tile);
      if (name) return name;
    }
    return null;
  },

  camOf(tile) {
    return [...tile.querySelectorAll(this.videoSel)]
      .some(video => video.readyState >= 2 && video.videoWidth > 0);
  },

  micOf(tiles) {
    let muted = false;
    let unmuted = false;
    for (const tile of tiles) {
      for (const element of tile.querySelectorAll('[aria-label]')) {
        const label = element.getAttribute('aria-label') || '';
        if (/can['’]t unmute/i.test(label)) muted = true;
        else if (/can['’]t mute/i.test(label)) unmuted = true;
      }
    }
    if (unmuted) return 'unmuted';
    if (muted) return 'muted';
    if (tiles.some(t => t.querySelector(this.muteIconPathSel))) return 'muted';
    return null;
  },

  detectSelf(tile) { return /\(You\)/.test(tile.innerText || ''); },
  detectHost(tile) { return /Meeting host/i.test(tile.innerText || ''); },

  selfState() {
    const mic = document.querySelector(this.selfMicSel)?.getAttribute('aria-label') || '';
    const cam = document.querySelector(this.selfCamSel)?.getAttribute('aria-label') || '';
    const onOff = s => /turn on/i.test(s) ? 'off' : /turn off/i.test(s) ? 'on' : 'unknown';
    return { mic: onOff(mic), cam: onOff(cam) };
  }
}));
