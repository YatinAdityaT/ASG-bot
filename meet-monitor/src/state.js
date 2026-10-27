(function (root, factory) {
  const api = factory();
  root.MMParticipantTracker = api.ParticipantTracker;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  class ParticipantTracker {
    constructor(leaveGraceMs = 30_000) {
      this.leaveGraceMs = leaveGraceMs;
      this.roster = new Map();
      this.lastSeen = new Map();
    }

    update(now, timestamp = Date.now()) {
      const changes = [];
      for (const [id, cur] of now) {
        const prev = this.roster.get(id);
        if (!prev) {
          changes.push({ type: 'join', id, cur });
        } else {
          if (prev.cam !== cur.cam) changes.push({ type: cur.cam ? 'cam_on' : 'cam_off', id, cur });
          if (prev.mic !== cur.mic) changes.push({ type: cur.mic === 'muted' ? 'mic_muted' : 'mic_unmuted', id, cur });
          if (cur.name && prev.name !== cur.name) changes.push({ type: 'rename', id, cur });
        }
        this.roster.set(id, cur);
        this.lastSeen.set(id, timestamp);
      }

      for (const [id, prev] of this.roster) {
        if (now.has(id)) continue;
        const lastSeen = this.lastSeen.get(id);
        if (lastSeen !== undefined && timestamp - lastSeen >= this.leaveGraceMs) {
          changes.push({ type: 'leave', id, cur: prev });
          this.roster.delete(id);
          this.lastSeen.delete(id);
        }
      }
      return changes;
    }
  }

  return { ParticipantTracker };
});
