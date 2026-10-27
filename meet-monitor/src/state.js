(function (root, factory) {
  const api = factory();
  root.MMParticipantTracker = api.ParticipantTracker;
  root.MMLeftParticipantName = api.leftParticipantName;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  class ParticipantTracker {
    constructor() {
      this.roster = new Map();
      this.departed = new Set();
    }

    update(now) {
      const changes = [];
      for (const id of this.departed) {
        if (!now.has(id)) this.departed.delete(id);
      }
      for (const [id, cur] of now) {
        if (this.departed.has(id)) continue;
        const prev = this.roster.get(id);
        if (!prev) {
          changes.push({ type: 'join', id, cur });
        } else {
          if (prev.cam !== cur.cam) changes.push({ type: cur.cam ? 'cam_on' : 'cam_off', id, cur });
          if (prev.mic !== cur.mic) changes.push({ type: cur.mic === 'muted' ? 'mic_muted' : 'mic_unmuted', id, cur });
          if (cur.name && prev.name !== cur.name) changes.push({ type: 'rename', id, cur });
        }
        this.roster.set(id, cur);
      }
      return changes;
    }

    leave(id) {
      const cur = this.roster.get(id);
      if (!cur) return null;
      this.roster.delete(id);
      this.departed.add(id);
      return { type: 'leave', id, cur };
    }
  }

  const leftParticipantName = text => {
    const match = String(text || '').trim().match(/^(.+?) left the (?:call|meeting)$/i);
    return match?.[1]?.trim() || null;
  };

  return { ParticipantTracker, leftParticipantName };
});
