(function (root, factory) {
  const api = factory();
  root.MMMeetPolicy = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const DEFAULT_MEET_CODES = ['jpy-ymgz-tdo'];
  const DEFAULT_MEET_LINKS = DEFAULT_MEET_CODES;
  const codePattern = /^[a-z]{3}-[a-z]{4}-[a-z]{3}$/;

  function meetingCode(rawURL) {
    const raw = String(rawURL || '').trim();
    if (codePattern.test(raw)) return raw;
    try {
      const url = new URL(raw);
      if (url.protocol !== 'https:' || url.hostname !== 'meet.google.com') return null;
      const segments = url.pathname.split('/').filter(Boolean);
      return segments.length === 1 && codePattern.test(segments[0])
        ? segments[0]
        : null;
    } catch {
      return null;
    }
  }

  function normalizeMeetCodes(value) {
    const entries = Array.isArray(value)
      ? value
      : String(value || '').split(/[\n,]/);
    const codes = [];
    for (const entry of entries) {
      const raw = String(entry || '').trim();
      if (!raw) continue;
      const code = meetingCode(raw);
      if (!code) throw new Error(`Invalid Google Meet code: ${raw}`);
      if (!codes.includes(code)) codes.push(code);
    }
    if (!codes.length) throw new Error('Add at least one Google Meet code');
    return codes;
  }

  function isMonitoredMeetURL(url, codes) {
    const code = meetingCode(url);
    if (!code) return false;
    let normalized;
    try {
      normalized = normalizeMeetCodes(codes);
    } catch {
      return false;
    }
    return normalized.includes(code);
  }

  return {
    DEFAULT_MEET_CODES,
    DEFAULT_MEET_LINKS,
    isMonitoredMeetURL,
    meetingCode,
    normalizeMeetCodes,
    normalizeMeetLinks: normalizeMeetCodes
  };
});
