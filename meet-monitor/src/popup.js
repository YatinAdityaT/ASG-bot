const $ = s => document.querySelector(s);
const esc = s => String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
let currentModel = null;
let activeTabId = null;
let activeTabForMonitor = null;
let activeTabAllowed = false;

async function activeMeetTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return tab && /^https:\/\/meet\.google\.com\//.test(tab.url || '') ? tab : null;
}

function render(state) {
  currentModel = state;
  $('#meeting').textContent = state?.meeting || '—';
  const rows = Object.entries(state?.roster || {});
  $('#count').textContent = rows.length;

  if (!rows.length) {
    $('#roster').innerHTML = '<tr><td colspan="3" id="empty">No participants detected</td></tr>';
  } else {
    $('#roster').innerHTML = rows.map(([, r]) => `
      <tr>
        <td>${esc(r.name || '(unnamed)')}${r.self ? '<span class="badge">you</span>' : ''}${r.host ? '<span class="badge">host</span>' : ''}</td>
        <td>${r.cam ? '🎥' : '—'}</td>
        <td>${r.mic === 'muted' ? '🔇' : '🎙'}</td>
      </tr>`).join('');
  }

  const self = rows.find(([, participant]) => participant.self)?.[1];
  $('#self').textContent = self
    ? `You — camera ${self.cam ? 'on' : 'off'}, mic ${self.mic}`
    : '';

  $('#feed').textContent = (state?.history || []).slice(-60).reverse()
    .map(e => {
      const time = e.ts.slice(11, 19);
      if (e.type === 'chat_message') {
        return `💬 ${time}  ${e.name}: ${e.text}`;
      }
      return `${time}  ${e.type.padEnd(11)} ${(e.name || e.id.slice(-6))}${e.self ? ' (you)' : ''}`;
    })
    .join('\n') || '(no events yet)';
  $('#sending').checked = state?.forwardingEnabled === true;
  renderSendingState();
}

function renderSendingState() {
  $('#sending-state').textContent = $('#sending').checked ? 'ON' : 'OFF';
}

function initSendingControl() {
  $('#sending').onchange = () => {
    renderSendingState();
    return chrome.runtime.sendMessage({
      kind: 'setTabMonitoring',
      tabId: activeTabId,
      enabled: $('#sending').checked
    });
  };
}

async function refresh() {
  try {
    const [tab, policy] = await Promise.all([
      activeMeetTab(),
      chrome.storage.sync.get({
        monitoredMeetLinks: MMMeetPolicy.DEFAULT_MEET_LINKS
      })
    ]);
    activeTabId = Number.isInteger(tab?.id) ? tab.id : null;
    activeTabForMonitor = tab;
    activeTabAllowed = Boolean(tab) && MMMeetPolicy.isMonitoredMeetURL(
      tab.url,
      policy.monitoredMeetLinks
    );
    $('#open-monitor').disabled = !activeTabAllowed;
    $('#monitor-status').textContent = tab && !activeTabAllowed
      ? 'This Meet link is not configured for monitoring.'
      : '';
    render(await chrome.runtime.sendMessage({
      kind: 'getMonitorModel',
      tabId: activeTabId
    }));
  }
  catch { render(null); }
}

$('#csv').onclick = async () => {
  const state = currentModel;
  if (!state) return;

  const header = MMCSV.row(['ts', 'type', 'participantId', 'name', 'self', 'host', 'cam', 'mic', 'text', 'meeting']) + '\n';
  const body = (state.history || []).map(e =>
    MMCSV.row([
      e.ts, e.type, e.id, e.name || '', e.self, e.host,
      e.cam ?? '', e.mic ?? '', e.text || '', e.meeting
    ])
  ).join('\n');

  const url = URL.createObjectURL(new Blob([header + body], { type: 'text/csv' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = `meet-${state.meeting}-${Date.now()}.csv`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
};

$('#opts').onclick = () => chrome.runtime.openOptionsPage();
$('#open-monitor').onclick = async () => {
  if (!activeTabForMonitor || !activeTabAllowed) return;
  const status = $('#monitor-status');
  try {
    await chrome.sidePanel.setOptions({
      tabId: activeTabForMonitor.id,
      path: 'src/side-panel.html',
      enabled: true
    });
    await chrome.sidePanel.open({ tabId: activeTabForMonitor.id });
    status.textContent = '';
  } catch (error) {
    status.textContent = `Could not open monitor: ${error?.message || 'unknown error'}`;
  }
};

initSendingControl();
refresh();
chrome.runtime.onMessage.addListener(message => {
  if (message?.kind === 'monitorModelChanged') refresh();
});
