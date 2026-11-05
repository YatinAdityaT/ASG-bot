(function (root, factory) {
  const api = factory();
  root.MMVersionUpdate = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const manifestURL =
    'https://raw.githubusercontent.com/YatinAdityaT/ASG-bot/master/meet-monitor/manifest.json';

  function versionParts(value) {
    const text = String(value || '');
    return /^\d+(?:\.\d+){0,3}$/.test(text)
      ? text.split('.').map(Number)
      : null;
  }

  function isNewer(current, candidate) {
    const left = versionParts(current);
    const right = versionParts(candidate);
    if (!left || !right) return false;
    for (let index = 0; index < Math.max(left.length, right.length); index++) {
      const currentPart = left[index] || 0;
      const candidatePart = right[index] || 0;
      if (candidatePart !== currentPart) return candidatePart > currentPart;
    }
    return false;
  }

  return { isNewer, manifestURL };
});
