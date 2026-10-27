(function (root, factory) {
  const api = factory();
  root.MMParticipantTracker = api.ParticipantTracker;
  root.MMLeftParticipantName = api.leftParticipantName;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  class ParticipantTracker {
    constructor(stabilityMs = 2000) {
      this.stabilityMs = stabilityMs;
      this.roster = new Map();
      this.departed = new Set();
      this.candidates = new Map();
    }

    update(now, observedAt = Date.now()) {
      const changes = [];
      for (const id of this.departed) {
        if (!now.has(id)) this.departed.delete(id);
      }
      for (const key of this.candidates.keys()) {
        const separator = key.lastIndexOf(':');
        if (!now.has(key.slice(0, separator))) this.candidates.delete(key);
      }
      for (const [id, cur] of now) {
        if (this.departed.has(id)) continue;
        const prev = this.roster.get(id);
        if (!prev) {
          const name = String(cur.name || '').trim();
          if (!name) continue;
          const confirmed = { ...cur, name };
          this.roster.set(id, confirmed);
          changes.push({ type: 'join', id, cur: confirmed });
          continue;
        }

        this.roster.set(id, {
          ...cur,
          name: prev.name,
          cam: prev.cam,
          mic: prev.mic
        });

        for (const field of ['cam', 'mic']) {
          const key = `${id}:${field}`;
          if (cur[field] === prev[field]) {
            this.candidates.delete(key);
            continue;
          }

          const candidate = this.candidates.get(key);
          if (!candidate || candidate.value !== cur[field]) {
            this.candidates.set(key, { value: cur[field], since: observedAt });
            continue;
          }
          if (observedAt - candidate.since < this.stabilityMs) continue;

          const confirmed = { ...this.roster.get(id), [field]: candidate.value };
          this.roster.set(id, confirmed);
          this.candidates.delete(key);
          const type = field === 'cam'
            ? (candidate.value ? 'cam_on' : 'cam_off')
            : (candidate.value === 'muted' ? 'mic_muted' : 'mic_unmuted');
          changes.push({ type, id, cur: confirmed });
        }
      }
      return changes;
    }

    nextDeadline() {
      let deadline = null;
      for (const candidate of this.candidates.values()) {
        const candidateDeadline = candidate.since + this.stabilityMs;
        if (deadline === null || candidateDeadline < deadline) deadline = candidateDeadline;
      }
      return deadline;
    }

    leave(id) {
      const cur = this.roster.get(id);
      if (!cur) return null;
      this.roster.delete(id);
      this.departed.add(id);
      this.candidates.delete(`${id}:cam`);
      this.candidates.delete(`${id}:mic`);
      return { type: 'leave', id, cur };
    }
  }

  const leftParticipantName = text => {
    const match = String(text || '').trim().match(/^(.+?) left the (?:call|meeting)$/i);
    return match?.[1]?.trim() || null;
  };

  return { ParticipantTracker, leftParticipantName };
});
