(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.MMSidePanel = api;
  if (root?.document && root?.chrome) api.start(root.document, root.chrome);
})(typeof window === 'undefined' ? null : window, function () {
  'use strict';

  let currentModel = null;
  let currentFilter = 'all';

  function filterHistory(history, filter) {
    const records = Array.isArray(history) ? history : [];
    return filter && filter !== 'all'
      ? records.filter(entry => entry?.type === filter)
      : records;
  }

  function csvCell(value) {
    let text = value == null
      ? ''
      : typeof value === 'object'
        ? JSON.stringify(value)
        : String(value);
    if (/^[=+\-@]/.test(text)) text = `'${text}`;
    return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
  }

  function csvForHistory(history) {
    const columns = [
      'timestamp', 'type', 'participantId', 'name', 'state', 'text',
      'meeting', 'sequence', 'deliveryState'
    ];
    const rows = (Array.isArray(history) ? history : []).map(entry => [
      entry.ts,
      entry.type,
      entry.id,
      entry.name,
      entry.state,
      entry.text,
      entry.meeting,
      entry.sequence,
      entry.deliveryState
    ].map(csvCell).join(','));
    return [columns.join(','), ...rows].join('\n');
  }

  function setText(document, id, value) {
    const node = document.getElementById(id);
    if (node) node.textContent = value == null ? '' : String(value);
  }

  function renderModel(document, model, filter = 'all') {
    currentModel = model || {};
    currentFilter = filter;
    setText(document, 'meeting', currentModel.meeting || '—');
    setText(document, 'version', currentModel.extensionVersion || '—');
    setText(
      document,
      'content-health',
      currentModel.content?.online ? 'Online' : 'Offline'
    );
    setText(
      document,
      'api-health',
      currentModel.api?.online ? 'Online' : 'Offline'
    );
    setText(
      document,
      'whatsapp-health',
      currentModel.whatsAppConnected ? 'Connected' : 'Disconnected'
    );
    setText(
      document,
      'permanent-error',
      currentModel.api?.error
        ? `API error: ${currentModel.api.error.category}` +
          (currentModel.api.error.status
            ? ` (${currentModel.api.error.status})`
            : '')
        : ''
    );
    setText(
      document,
      'debug-count',
      Array.isArray(currentModel.debugHistory)
        ? currentModel.debugHistory.length
        : 0
    );

    const forwarding = document.getElementById('forwarding');
    if (forwarding) forwarding.checked = currentModel.forwardingEnabled === true;
    const debug = document.getElementById('debug');
    if (debug) debug.checked = currentModel.debugEnabled === true;

    renderRoster(document, currentModel.roster);
    renderHistory(document, currentModel.history, filter);
  }

  function renderRoster(document, roster) {
    const container = document.getElementById('roster');
    if (!container) return;
    const rows = Object.entries(roster || {});
    if (!rows.length) {
      const empty = document.createElement('li');
      empty.id = 'empty-roster';
      empty.textContent = 'No participants detected';
      container.replaceChildren(empty);
      return;
    }
    const nodes = rows.map(([id, participant]) => {
      const item = document.createElement('li');
      const name = document.createElement('div');
      name.textContent = participant?.name || id || '(unnamed)';
      const state = document.createElement('div');
      state.className = 'badges';
      const labels = [
        participant?.cam ? 'Camera on' : 'Camera off',
        participant?.mic === 'muted' ? 'Mic muted' : 'Mic on'
      ];
      if (participant?.self) labels.push('You');
      if (participant?.host) labels.push('Host');
      state.textContent = labels.join(' · ');
      item.append(name, state);
      return item;
    });
    container.replaceChildren(...nodes);
  }

  function renderHistory(document, history, filter) {
    const container = document.getElementById('timeline');
    if (!container) return;
    const rows = filterHistory(history, filter).slice().reverse();
    if (!rows.length) {
      const empty = document.createElement('li');
      empty.id = 'empty-history';
      empty.textContent = 'No matching events';
      container.replaceChildren(empty);
      return;
    }
    const nodes = rows.map(entry => {
      const item = document.createElement('li');
      const meta = document.createElement('div');
      meta.className = 'event-meta';
      meta.textContent = [
        formatTime(entry.ts),
        entry.type || 'event',
        entry.deliveryState || 'pending'
      ].join(' · ');
      const detail = document.createElement('div');
      detail.className = 'event-text';
      detail.textContent = historyText(entry);
      item.append(meta, detail);
      return item;
    });
    container.replaceChildren(...nodes);
  }

  function formatTime(timestamp) {
    const text = String(timestamp || '');
    return text.length >= 19 ? text.slice(11, 19) : text || '—';
  }

  function historyText(entry) {
    const participant = entry?.name || entry?.id || '(unknown)';
    if (entry?.type === 'chat_message') {
      return `${participant}: ${entry?.text || ''}`;
    }
    const state = entry?.state == null
      ? ''
      : typeof entry.state === 'object'
        ? ` ${JSON.stringify(entry.state)}`
        : ` ${entry.state}`;
    return `${participant}${state}`;
  }

  function downloadHistory(document, model) {
    const csv = csvForHistory(model?.history || []);
    const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = `meet-${model?.meeting || 'monitor'}-${Date.now()}.csv`;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1_000);
  }

  async function start(document, chrome) {
    const filter = document.getElementById('event-filter');
    filter?.addEventListener('change', () => {
      currentFilter = filter.value;
      renderModel(document, currentModel, currentFilter);
    });
    document.getElementById('forwarding')?.addEventListener('change', event => {
      chrome.runtime.sendMessage({
        kind: 'setForwarding',
        enabled: event.currentTarget.checked
      });
    });
    document.getElementById('debug')?.addEventListener('change', event => {
      chrome.runtime.sendMessage({
        kind: 'setDebugMode',
        enabled: event.currentTarget.checked
      });
    });
    document.getElementById('clear-history')?.addEventListener('click', () => {
      chrome.runtime.sendMessage({ kind: 'clearMonitorHistory' });
    });
    document.getElementById('export-csv')?.addEventListener('click', () => {
      downloadHistory(document, currentModel);
    });
    document.getElementById('open-options')?.addEventListener(
      'click',
      () => chrome.runtime.openOptionsPage()
    );
    chrome.runtime.onMessage.addListener(message => {
      if (message?.kind === 'monitorModelChanged' && message.model) {
        renderModel(document, message.model, currentFilter);
      }
    });
    const model = await chrome.runtime.sendMessage({ kind: 'getMonitorModel' });
    renderModel(document, model, currentFilter);
  }

  return {
    csvForHistory,
    filterHistory,
    historyText,
    renderModel,
    start
  };
});
