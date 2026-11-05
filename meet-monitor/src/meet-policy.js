(function (root, factory) {
  const api = factory();
  root.MMMeetPolicy = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const DEFAULT_MEET_LINKS = ['https://meet.google.com/jpy-ymgz-tdo'];
  const codePattern = /^[a-z]{3}-[a-z]{4}-[a-z]{3}$/;

  function meetingCode(rawURL) {
    try {
      const url = new URL(String(rawURL || '').trim());
      if (url.protocol !== 'https:' || url.hostname !== 'meet.google.com') return null;
      const segments = url.pathname.split('/').filter(Boolean);
      return segments.length === 1 && codePattern.test(segments[0])
        ? segments[0]
        : null;
    } catch {
      return null;
    }
  }

  function normalizeMeetLinks(value) {
    const entries = Array.isArray(value)
      ? value
      : String(value || '').split(/[\n,]/);
    const links = [];
    for (const entry of entries) {
      const raw = String(entry || '').trim();
      if (!raw) continue;
      const code = meetingCode(raw);
      if (!code) throw new Error(`Invalid Google Meet link: ${raw}`);
      const normalized = `https://meet.google.com/${code}`;
      if (!links.includes(normalized)) links.push(normalized);
    }
    if (!links.length) throw new Error('Add at least one Google Meet link');
    return links;
  }

  function isMonitoredMeetURL(url, links) {
    const code = meetingCode(url);
    if (!code) return false;
    let normalized;
    try {
      normalized = normalizeMeetLinks(links);
    } catch {
      return false;
    }
    return normalized.includes(`https://meet.google.com/${code}`);
  }

  return {
    DEFAULT_MEET_LINKS,
    isMonitoredMeetURL,
    meetingCode,
    normalizeMeetLinks
  };
});
