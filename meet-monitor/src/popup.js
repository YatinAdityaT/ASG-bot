const $ = s => document.querySelector(s);
const esc = s => String(s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));

async function activeMeetTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  return tab && /^https:\/\/meet\.google\.com\//.test(tab.url || '') ? tab : null;
}

function render(state) {
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

  $('#self').textContent = state
    ? `Your toolbar — camera ${state.self.cam}, mic ${state.self.mic}`
    : '';

  $('#feed').textContent = (state?.events || []).slice(-60).reverse()
    .map(e => {
      const time = e.ts.slice(11, 19);
      if (e.type === 'chat_message') {
        return `💬 ${time}  ${e.name}: ${e.text}`;
      }
      return `${time}  ${e.type.padEnd(11)} ${(e.name || e.id.slice(-6))}${e.self ? ' (you)' : ''}`;
    })
    .join('\n') || '(no events yet)';
}

async function refresh() {
  const tab = await activeMeetTab();
  if (!tab) { render(null); return; }
  try { render(await chrome.tabs.sendMessage(tab.id, { kind: 'getState' })); }
  catch { render(null); }
}

$('#csv').onclick = async () => {
  const tab = await activeMeetTab();
  if (!tab) return;
  const state = await chrome.tabs.sendMessage(tab.id, { kind: 'getState' }).catch(() => null);
  if (!state) return;

  const header = MMCSV.row(['ts', 'type', 'participantId', 'name', 'self', 'host', 'cam', 'mic', 'text', 'meeting']) + '\n';
  const body = state.events.map(e =>
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

refresh();
setInterval(refresh, 1000);
