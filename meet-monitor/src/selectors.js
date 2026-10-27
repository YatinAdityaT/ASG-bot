// All Meet DOM assumptions live here. When Meet redesigns, fix only this file.
window.MMSelectors = {
  // --- PARTICIPANT SELECTORS ---
  tileSel: '[data-participant-id]',
  idAttr: 'data-participant-id',
  videoSel: 'video',
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
    let raw = (tile.innerText || '').replace(/\s+/g, ' ').trim();
    raw = raw
      .replace(/\(You\)/g, '')
      .replace(/Meeting host/gi, '')
      .replace(/You can't unmute someone else/gi, '')
      .replace(/You can't mute/gi, '')
      .replace(/more_vert/gi, '')
      .replace(/More actions/gi, '')
      .replace(/\bdevices\b/gi, '')
      .trim();
    const doubled = raw.match(/^(.+?)\s+\1$/);
    return doubled ? doubled[1] : (raw || null);
  },

  pickName(tiles) {
    const scored = tiles.map(t => {
      const txt = t.innerText || '';
      const isPanelRow = /More actions|more_vert/.test(txt);
      const name = this.nameOf(t);
      const score = (isPanelRow ? 0 : 100) + (name ? name.length : 0);
      return { name, score };
    }).filter(x => x.name);
    scored.sort((a, b) => b.score - a.score);
    return scored[0]?.name || null;
  },

  camOf(tile) {
    const v = tile.querySelector(this.videoSel);
    return !!v && v.readyState >= 2 && v.videoWidth > 0;
  },

  micOf(tiles) {
    return tiles.some(t => t.querySelector(this.muteIconPathSel)) ? 'muted' : 'unmuted';
  },

  detectSelf(tile) { return /\(You\)/.test(tile.innerText || ''); },
  detectHost(tile) { return /Meeting host/i.test(tile.innerText || ''); },

  selfState() {
    const mic = document.querySelector(this.selfMicSel)?.getAttribute('aria-label') || '';
    const cam = document.querySelector(this.selfCamSel)?.getAttribute('aria-label') || '';
    const onOff = s => /turn on/i.test(s) ? 'off' : /turn off/i.test(s) ? 'on' : 'unknown';
    return { mic: onOff(mic), cam: onOff(cam) };
  }
};
