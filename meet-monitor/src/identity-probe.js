(function (root, factory) {
  const api = factory();
  root.MMIdentityProbe = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const EMAIL = /[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+/gi;
  const IDENTITY_ATTRIBUTE = /(?:email|user|account|participant|device|gaia|profile|identifier|(^|[-_:])id$)/i;

  function inspect(document, selectors) {
    const participants = [];
    const allEmails = new Set(emailsFromNode(document.documentElement));

    for (const tile of document.querySelectorAll(selectors.tileSel)) {
      const emailCandidates = emailsFromNode(tile);
      for (const email of emailCandidates) allEmails.add(email);
      participants.push({
        id: tile.getAttribute(selectors.idAttr) || '',
        name: selectors.pickName([tile]) || '',
        emailCandidates,
        identityAttributes: identityAttributes(tile)
      });
    }

    participants.sort((a, b) =>
      a.name.localeCompare(b.name) || a.id.localeCompare(b.id)
    );
    return {
      emailCandidates: [...allEmails].sort(),
      participants
    };
  }

  function nodesWithin(node) {
    if (!node) return [];
    let descendants = [];
    try {
      descendants = [...(node.querySelectorAll?.('*') || [])];
    } catch {
      descendants = [];
    }
    return [node, ...descendants];
  }

  function emailsFromNode(node) {
    const found = new Set();
    for (const current of nodesWithin(node)) {
      addEmails(found, current.innerText);
      addEmails(found, current.textContent);
      for (const attribute of current.attributes || []) {
        addEmails(found, attribute.value);
      }
    }
    return [...found].sort();
  }

  function addEmails(target, value) {
    const text = String(value || '');
    for (const match of text.matchAll(EMAIL)) {
      target.add(match[0].toLowerCase());
    }
  }

  function identityAttributes(node) {
    const found = new Map();
    for (const current of nodesWithin(node)) {
      for (const attribute of current.attributes || []) {
        const name = String(attribute.name || '');
        const value = String(attribute.value || '').trim();
        if (!IDENTITY_ATTRIBUTE.test(name) || !value || value.length > 300) continue;
        found.set(`${name}\u0000${value}`, { name, value });
      }
    }
    return [...found.values()].sort((a, b) =>
      a.name.localeCompare(b.name) || a.value.localeCompare(b.value)
    );
  }

  return { inspect };
});
