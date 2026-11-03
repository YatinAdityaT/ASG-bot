const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const SRC = path.join(__dirname, '..', 'src');

test('side panel exposes health, roster, filters, controls, and actions', () => {
  const html = fs.readFileSync(path.join(SRC, 'side-panel.html'), 'utf8');

  for (const id of [
    'meeting', 'version', 'content-health', 'api-health', 'whatsapp-health',
    'roster', 'timeline', 'event-filter', 'forwarding', 'debug',
    'clear-history', 'export-csv', 'open-options', 'permanent-error'
  ]) {
    assert.match(html, new RegExp(`id="${id}"`));
  }
  assert.match(html, /aria-label="Event type filter"/);
  assert.match(html, /src="side-panel\.js"/);
});

test('side panel helpers filter history and safely escape CSV formulas and quotes', () => {
  const panel = require('../src/side-panel.js');
  const history = [
    {
      ts: '2026-11-03T12:00:00Z',
      type: 'join',
      id: 'p-1',
      name: '=HYPERLINK("bad")',
      text: 'hello, "team"',
      meeting: 'jpy-ymgz-tdo',
      sequence: 7,
      deliveryState: 'accepted'
    },
    {
      ts: '2026-11-03T12:01:00Z',
      type: 'leave',
      id: 'p-2',
      name: '<img src=x onerror=alert(1)>',
      meeting: 'jpy-ymgz-tdo',
      sequence: 8,
      deliveryState: 'failed'
    }
  ];

  assert.deepEqual(
    panel.filterHistory(history, 'leave').map(entry => entry.id),
    ['p-2']
  );
  const csv = panel.csvForHistory(history);
  assert.match(csv, /^timestamp,type,participantId,name,state,text,meeting,sequence,deliveryState/m);
  assert.match(csv, /"'=HYPERLINK\(""bad""\)"/);
  assert.match(csv, /"hello, ""team"""/);
  assert.doesNotMatch(csv, /token|requestBody/i);
});

test('side panel rendering uses text nodes for participant and chat content', () => {
  const panel = require('../src/side-panel.js');
  const document = fakeDocument();
  const hostile = '<img src=x onerror=alert(1)>';
  panel.renderModel(document, {
    meeting: 'jpy-ymgz-tdo',
    extensionVersion: '2.0.0',
    forwardingEnabled: true,
    debugEnabled: false,
    content: { online: true, lastObservedAt: 1 },
    api: { online: false, error: { category: 'unauthorized' } },
    whatsAppConnected: false,
    roster: {
      participant: {
        name: hostile,
        cam: true,
        mic: 'muted',
        self: true,
        host: true
      }
    },
    history: [{
      ts: '2026-11-03T12:00:00Z',
      type: 'chat_message',
      id: 'chat-1',
      name: hostile,
      text: hostile,
      deliveryState: 'failed'
    }],
    debugHistory: []
  }, 'all');

  assert.match(document.nodes.roster.textContent, /<img src=x/);
  assert.match(document.nodes.timeline.textContent, /<img src=x/);
  assert.equal(document.nodes.roster.innerHTMLWrites, 0);
  assert.equal(document.nodes.timeline.innerHTMLWrites, 0);
  assert.match(document.nodes['permanent-error'].textContent, /unauthorized/);
});

function fakeDocument() {
  class Node {
    constructor(tag = 'div') {
      this.tag = tag;
      this.children = [];
      this._text = '';
      this.innerHTMLWrites = 0;
      this.checked = false;
    }
    set textContent(value) {
      this._text = String(value ?? '');
      this.children = [];
    }
    get textContent() {
      return this._text + this.children.map(child => child.textContent).join('');
    }
    set innerHTML(_value) {
      this.innerHTMLWrites += 1;
    }
    append(...children) {
      this.children.push(...children);
    }
    appendChild(child) {
      this.children.push(child);
      return child;
    }
    replaceChildren(...children) {
      this.children = children;
      this._text = '';
    }
    setAttribute() {}
  }

  const ids = [
    'meeting', 'version', 'content-health', 'api-health', 'whatsapp-health',
    'roster', 'timeline', 'forwarding', 'debug', 'permanent-error',
    'debug-count'
  ];
  const nodes = Object.fromEntries(ids.map(id => [id, new Node()]));
  return {
    nodes,
    getElementById(id) {
      if (!nodes[id]) nodes[id] = new Node();
      return nodes[id];
    },
    createElement(tag) {
      return new Node(tag);
    }
  };
}
