(function (root, factory) {
  const api = factory();
  root.MMSnapshot = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const canonicalParticipants = roster =>
    Object.entries(roster || {})
      .map(([id, value]) => ({
        id,
        name: String(value?.name || '').trim(),
        cam: Boolean(value?.cam),
        mic: value?.mic === 'unmuted' ? 'unmuted' : 'muted',
        self: Boolean(value?.self),
        host: Boolean(value?.host)
      }))
      .sort((left, right) => left.id.localeCompare(right.id));

  const build = input => ({
    protocolVersion: 1,
    meeting: String(input?.meeting || ''),
    sessionId: String(input?.sessionId || ''),
    sequence: Number(input?.sequence || 0),
    capturedAt: String(input?.capturedAt || ''),
    extensionVersion: String(input?.extensionVersion || ''),
    forwardingEnabled: Boolean(input?.forwardingEnabled),
    participants: canonicalParticipants(input?.roster)
  });

  const stateKey = snapshot =>
    JSON.stringify([
      snapshot?.meeting || '',
      snapshot?.sessionId || '',
      snapshot?.extensionVersion || '',
      Boolean(snapshot?.forwardingEnabled),
      canonicalParticipants(
        Object.fromEntries((snapshot?.participants || []).map(value => [value.id, value]))
      )
    ]);

  return { build, canonicalParticipants, stateKey };
});
