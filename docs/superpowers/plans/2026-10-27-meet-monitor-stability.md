# Meet Monitor Event Stability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop Google Meet DOM redraws and duplicate observers from producing false or repeated WhatsApp events.

**Architecture:** Extract participant names only from Meet's dedicated name element, then pass raw participant observations through a pure state tracker that confirms media changes for two seconds. Apply a short cross-context duplicate guard in the extension background, and make chat idempotency independent of local observation time in both the extension and Go API.

**Tech Stack:** Chrome Manifest V3 JavaScript, Node.js built-in test runner, Go 1.26, SQLite outbound queue.

## Global Constraints

- Rename events are disabled.
- Camera and microphone transitions require two seconds of consistent observation.
- Full participant tile text is never a name fallback.
- Chat identity is event type + meeting code + Meet message ID, without observation timestamp.
- The existing authenticated outbox, generic webhook, CSV export, consent banner, and WhatsApp destination policy remain intact.
- Remote extension distribution and automatic updates remain out of scope.

## File Structure

- Modify `meet-monitor/src/selectors.js`: dedicated participant-name extraction and CommonJS test export.
- Create `meet-monitor/test/selectors.test.js`: name extraction regression coverage from the captured Meet DOM shape.
- Modify `meet-monitor/src/state.js`: confirmed participant state, two-second media candidates, and no rename events.
- Modify `meet-monitor/test/state.test.js`: deterministic state-transition tests.
- Modify `meet-monitor/src/content.js`: timestamped observations and deadline-driven confirmation scans.
- Modify `meet-monitor/src/delivery.js`: stable chat event keys and cross-context forwarding keys.
- Modify `meet-monitor/test/delivery.test.js`: identity-key tests.
- Modify `meet-monitor/src/background.js`: five-second duplicate guard before every forwarding path.
- Modify `meet-monitor/test/background.test.js`: duplicate forwarding and notification tests.
- Modify `messageapi/meet.go`: server-side stable chat idempotency.
- Modify `messageapi/meet_test.go`: repeated-chat and repeated-state API tests.
- Modify `meet-monitor/manifest.json`, `meet-monitor/src/options.html`, and `meet-monitor/README.md`: version and behavior documentation.

---

### Task 1: Extract Only Dedicated Participant Names

**Files:**
- Modify: `meet-monitor/src/selectors.js`
- Create: `meet-monitor/test/selectors.test.js`

**Interfaces:**
- Produces: `MMSelectors.nameOf(tile) -> string|null`
- Produces: `MMSelectors.pickName(tiles) -> string|null`
- Requires: a tile implementing `querySelector('div[jsname="giiMnc"]')`

- [ ] **Step 1: Write failing selector tests**

Create `meet-monitor/test/selectors.test.js`:

```js
const test = require('node:test');
const assert = require('node:assert/strict');

const selectors = require('../src/selectors.js');

const tile = ({ nameText = null, tileText = '' } = {}) => ({
  innerText: tileText,
  querySelector(selector) {
    if (selector === 'div[jsname="giiMnc"]' && nameText !== null) {
      return { innerText: nameText };
    }
    return null;
  }
});

test('reads and collapses the dedicated Meet participant name', () => {
  assert.equal(selectors.nameOf(tile({ nameText: 'Sruti\nSruti' })), 'Sruti');
  assert.equal(selectors.nameOf(tile({ nameText: 'Yatin T\nYatin T' })), 'Yatin T');
});

test('never treats participant controls as a name', () => {
  assert.equal(selectors.nameOf(tile({
    tileText: 'frame_person Reframe visual_effects Backgrounds and effects More options for Yatin T'
  })), null);
});

test('picks the first usable dedicated name', () => {
  assert.equal(selectors.pickName([
    tile({ tileText: 'keep_outline Pin Sruti mic_off More options for Sruti' }),
    tile({ nameText: 'Sruti\nSruti' })
  ]), 'Sruti');
});
```

- [ ] **Step 2: Run the selector tests and verify failure**

Run:

```powershell
node --test meet-monitor/test/selectors.test.js
```

Expected: FAIL because `selectors.js` does not export a CommonJS API and still reads `tile.innerText`.

- [ ] **Step 3: Implement dedicated name extraction**

Wrap `selectors.js` in the same browser/CommonJS factory pattern used by
`state.js` and `delivery.js`. Add:

```js
nameSel: 'div[jsname="giiMnc"]',

nameOf(tile) {
  const element = tile.querySelector(this.nameSel);
  if (!element) return null;
  const lines = String(element.innerText || element.textContent || '')
    .split(/\r?\n/)
    .map(value => value.replace(/\s+/g, ' ').trim())
    .filter(Boolean);
  if (!lines.length) return null;
  return lines.every(value => value === lines[0])
    ? lines[0]
    : lines.join(' ');
},

pickName(tiles) {
  for (const tile of tiles) {
    const name = this.nameOf(tile);
    if (name) return name;
  }
  return null;
},
```

Keep all existing selector and state-reading methods in the returned object.
The wrapper must assign it to `root.MMSelectors` and to `module.exports` when
CommonJS is present.

- [ ] **Step 4: Run selector and full extension tests**

Run:

```powershell
node --test meet-monitor/test/selectors.test.js
node --test meet-monitor/test/*.test.js
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```powershell
git add meet-monitor/src/selectors.js meet-monitor/test/selectors.test.js
git commit -m "fix: extract clean Meet participant names"
```

---

### Task 2: Confirm Media Changes and Remove Rename Events

**Files:**
- Modify: `meet-monitor/src/state.js`
- Modify: `meet-monitor/test/state.test.js`
- Modify: `meet-monitor/src/content.js`

**Interfaces:**
- Produces: `new ParticipantTracker(stabilityMs = 2000)`
- Produces: `tracker.update(now: Map, observedAt: number) -> change[]`
- Produces: `tracker.nextDeadline() -> number|null`
- Preserves: `tracker.leave(id) -> change|null`

- [ ] **Step 1: Replace state tests with deterministic transition coverage**

Retain the explicit-leave tests and add:

```js
test('does not emit rename events or replace the established name', () => {
  const tracker = new ParticipantTracker(2_000);
  tracker.update(new Map([['alex', {
    name: 'Alex', cam: false, mic: 'muted'
  }]]), 0);

  assert.deepEqual(tracker.update(new Map([['alex', {
    name: 'Reframe More options for Alex', cam: false, mic: 'muted'
  }]]), 3_000), []);
  assert.equal(tracker.roster.get('alex').name, 'Alex');
});

test('cancels a transient camera observation', () => {
  const tracker = new ParticipantTracker(2_000);
  tracker.update(new Map([['alex', {
    name: 'Alex', cam: false, mic: 'muted'
  }]]), 0);

  assert.deepEqual(tracker.update(new Map([['alex', {
    name: 'Alex', cam: true, mic: 'muted'
  }]]), 1_000), []);
  assert.equal(tracker.nextDeadline(), 3_000);
  assert.deepEqual(tracker.update(new Map([['alex', {
    name: 'Alex', cam: false, mic: 'muted'
  }]]), 2_000), []);
  assert.equal(tracker.nextDeadline(), null);
});

test('emits one media event after two seconds of consistent observation', () => {
  const tracker = new ParticipantTracker(2_000);
  tracker.update(new Map([['alex', {
    name: 'Alex', cam: false, mic: 'muted'
  }]]), 0);

  tracker.update(new Map([['alex', {
    name: 'Alex', cam: true, mic: 'unmuted'
  }]]), 1_000);
  const changes = tracker.update(new Map([['alex', {
    name: 'Alex', cam: true, mic: 'unmuted'
  }]]), 3_000);

  assert.deepEqual(changes.map(change => change.type), ['cam_on', 'mic_unmuted']);
  assert.equal(tracker.nextDeadline(), null);
  assert.deepEqual(tracker.update(new Map([['alex', {
    name: 'Alex', cam: true, mic: 'unmuted'
  }]]), 4_000), []);
});

test('waits for a usable name before joining', () => {
  const tracker = new ParticipantTracker();
  assert.deepEqual(tracker.update(new Map([['alex', {
    name: null, cam: false, mic: 'muted'
  }]]), 0), []);
  assert.deepEqual(
    tracker.update(new Map([['alex', {
      name: 'Alex', cam: false, mic: 'muted'
    }]]), 1_000).map(change => change.type),
    ['join']
  );
});
```

- [ ] **Step 2: Run state tests and verify failure**

Run:

```powershell
node --test meet-monitor/test/state.test.js
```

Expected: FAIL because rename is still emitted and candidate state/deadlines do not exist.

- [ ] **Step 3: Implement confirmed participant state**

Change `ParticipantTracker` to store `stabilityMs`, `roster`, `departed`, and
`candidates`. Use candidate keys `${id}:${field}` and candidate values
`{ value, since }`. For each existing participant:

```js
for (const [field, onType, offType] of [
  ['cam', 'cam_on', 'cam_off'],
  ['mic', 'mic_muted', 'mic_unmuted']
]) {
  const key = `${id}:${field}`;
  const observed = cur[field];
  const confirmed = prev[field];
  if (observed === confirmed) {
    this.candidates.delete(key);
    continue;
  }
  const candidate = this.candidates.get(key);
  if (!candidate || candidate.value !== observed) {
    this.candidates.set(key, { value: observed, since: observedAt });
    continue;
  }
  if (observedAt - candidate.since < this.stabilityMs) continue;
  prev[field] = observed;
  this.candidates.delete(key);
  changes.push({
    type: field === 'cam'
      ? (observed ? onType : offType)
      : (observed === 'muted' ? onType : offType),
    id,
    cur: { ...prev }
  });
}
```

Do not compare or replace `prev.name` after join. Update `self` and `host`
metadata from observations. `nextDeadline()` returns the minimum
`candidate.since + stabilityMs`, or `null`. `leave()` deletes every candidate
whose key begins with `${id}:`.

- [ ] **Step 4: Drive confirmation scans from content.js**

Construct `ParticipantTracker(2_000)`. Pass `Date.now()` into `update`. Add a
single stability timer:

```js
let stabilityTimer = null;

const scheduleStabilityScan = deadline => {
  if (stabilityTimer) clearTimeout(stabilityTimer);
  stabilityTimer = null;
  if (deadline === null) return;
  stabilityTimer = setTimeout(() => {
    stabilityTimer = null;
    scan();
  }, Math.max(deadline - Date.now(), 0));
};
```

After emitting tracker changes, call:

```js
scheduleStabilityScan(participantTracker.nextDeadline());
```

- [ ] **Step 5: Run extension tests**

Run:

```powershell
node --test meet-monitor/test/*.test.js
```

Expected: all tests PASS and no test expects a rename event.

- [ ] **Step 6: Commit**

```powershell
git add meet-monitor/src/state.js meet-monitor/src/content.js meet-monitor/test/state.test.js
git commit -m "fix: stabilize Meet participant state changes"
```

---

### Task 3: Suppress Duplicate Extension Events

**Files:**
- Modify: `meet-monitor/src/delivery.js`
- Modify: `meet-monitor/test/delivery.test.js`
- Modify: `meet-monitor/src/background.js`
- Modify: `meet-monitor/test/background.test.js`

**Interfaces:**
- Changes: `eventKey(event)` excludes `ts` only for `chat_message`
- Produces: `forwardingKey(event) -> string`
- Background rule: equivalent forwarding keys received within 5,000 ms are ignored

- [ ] **Step 1: Add failing identity tests**

Add to `delivery.test.js`:

```js
test('chat keys ignore local observation timestamps', () => {
  const base = {
    type: 'chat_message',
    id: 'spaces/space/messages/123',
    meeting: 'abc-defg-hij'
  };
  assert.equal(
    eventKey({ ...base, ts: '2026-10-27T08:00:00Z' }),
    eventKey({ ...base, ts: '2026-10-27T08:00:02Z' })
  );
});

test('non-chat keys retain timestamps for repeated real transitions', () => {
  const base = { type: 'cam_on', id: 'alex', meeting: 'abc-defg-hij' };
  assert.notEqual(
    eventKey({ ...base, ts: '2026-10-27T08:00:00Z' }),
    eventKey({ ...base, ts: '2026-10-27T09:00:00Z' })
  );
});

test('forwarding keys ignore observation time but include event content', () => {
  const base = {
    type: 'chat_message', id: 'message-1', meeting: 'abc-defg-hij',
    name: 'Alex', text: 'Hello'
  };
  assert.equal(
    forwardingKey({ ...base, ts: '2026-10-27T08:00:00Z' }),
    forwardingKey({ ...base, ts: '2026-10-27T08:00:01Z' })
  );
  assert.notEqual(forwardingKey(base), forwardingKey({ ...base, text: 'Different' }));
});
```

Update the import to include `forwardingKey`.

- [ ] **Step 2: Add a failing background duplicate test**

Extend the background harness to record fetch request bodies and notification
calls. Add:

```js
test('equivalent events from multiple contexts forward once within five seconds', async () => {
  const harness = await createHarness([202]);
  const first = {
    ...meetEvent('message-1'),
    type: 'chat_message',
    text: 'Hello',
    ts: '2026-10-27T08:00:00Z'
  };
  const second = { ...first, ts: '2026-10-27T08:00:01Z' };

  await harness.sendEvent(first);
  await harness.sendEvent(second);
  await waitFor(() => harness.localData.botAPIOutbox.length === 0);

  assert.equal(harness.requests.length, 1);
  assert.equal(harness.notifications.length, 1);
});
```

- [ ] **Step 3: Run identity and background tests and verify failure**

Run:

```powershell
node --test meet-monitor/test/delivery.test.js meet-monitor/test/background.test.js
```

Expected: FAIL because chat keys include timestamps, `forwardingKey` is absent,
and both events are processed.

- [ ] **Step 4: Implement stable keys**

In `delivery.js`:

```js
const eventKey = event => JSON.stringify([
  event?.type || '',
  event?.id || '',
  event?.meeting || '',
  event?.type === 'chat_message' ? '' : (event?.ts || '')
]);

const forwardingKey = event => JSON.stringify([
  event?.type || '',
  event?.id || '',
  event?.meeting || '',
  event?.name || '',
  event?.cam ?? null,
  event?.mic ?? null,
  event?.text ?? null
]);
```

Export `forwardingKey`.

- [ ] **Step 5: Implement the five-second background guard**

Add:

```js
const FORWARD_DEDUPE_MS = 5_000;
const recentForwarded = new Map();

function shouldForward(rec, now = Date.now()) {
  const key = MMDelivery.forwardingKey(rec);
  const previous = recentForwarded.get(key);
  for (const [candidate, seenAt] of recentForwarded) {
    if (now - seenAt >= FORWARD_DEDUPE_MS) recentForwarded.delete(candidate);
  }
  if (previous !== undefined && now - previous < FORWARD_DEDUPE_MS) return false;
  recentForwarded.set(key, now);
  return true;
}
```

At the start of the `event` message branch, return `{ queued: false }` without
notification, webhook forwarding, or outbox insertion when `shouldForward(rec)`
is false.

- [ ] **Step 6: Run all extension tests**

Run:

```powershell
node --test meet-monitor/test/*.test.js
```

Expected: all tests PASS.

- [ ] **Step 7: Commit**

```powershell
git add meet-monitor/src/delivery.js meet-monitor/src/background.js meet-monitor/test/delivery.test.js meet-monitor/test/background.test.js
git commit -m "fix: deduplicate Meet events across extension contexts"
```

---

### Task 4: Make Chat Idempotency Stable on the Server

**Files:**
- Modify: `messageapi/meet.go`
- Modify: `messageapi/meet_test.go`

**Interfaces:**
- Preserves: `POST /v1/meet-events`
- Changes: chat idempotency excludes `TS`; every other event type includes it

- [ ] **Step 1: Add failing server idempotency tests**

Add:

```go
func TestMeetChatReplayIgnoresObservationTimestamp(t *testing.T) {
	server, store := newFixture(t, func() time.Time { return fixedNow })
	firstEvent := meetEvent("chat_message", "Hello")
	firstEvent["id"] = "spaces/space/messages/123"
	secondEvent := meetEvent("chat_message", "Hello")
	secondEvent["id"] = "spaces/space/messages/123"
	secondEvent["ts"] = "2026-10-27T08:00:02Z"

	first := submitMeet(t, server.Handler(), firstEvent)
	second := submitMeet(t, server.Handler(), secondEvent)

	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d", first.Code, second.Code)
	}
	jobs, err := store.Due(t.Context(), fixedNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("queued jobs = %d, want 1", len(jobs))
	}
}

func TestMeetStateIdentityRetainsTimestamp(t *testing.T) {
	server, store := newFixture(t, func() time.Time { return fixedNow })
	first := meetEvent("cam_on", "")
	second := meetEvent("cam_on", "")
	second["ts"] = "2026-10-27T09:00:00Z"

	firstResponse := submitMeet(t, server.Handler(), first)
	secondResponse := submitMeet(t, server.Handler(), second)
	if firstResponse.Code != http.StatusAccepted || secondResponse.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d", firstResponse.Code, secondResponse.Code)
	}
	jobs, err := store.Due(t.Context(), fixedNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("queued jobs = %d, want 2", len(jobs))
	}
}
```

- [ ] **Step 2: Run the Go test and verify failure**

Run:

```powershell
go test ./messageapi -run "TestMeet(ChatReplay|StateIdentity)" -count=1
```

Expected: FAIL because the repeated chat timestamp creates a second job.

- [ ] **Step 3: Change chat identity normalization**

In `meetIdempotencyKey`, set the identity timestamp conditionally:

```go
identityTS := event.TS
if event.Type == "chat_message" {
	identityTS = ""
}
identity := struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Meeting string `json:"meeting"`
	TS      string `json:"ts,omitempty"`
}{
	Type:    event.Type,
	ID:      event.ID,
	Meeting: event.Meeting,
	TS:      identityTS,
}
```

- [ ] **Step 4: Run message API and full Go tests**

Run:

```powershell
go test ./messageapi -count=1
go test ./... -count=1
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```powershell
git add messageapi/meet.go messageapi/meet_test.go
git commit -m "fix: make Meet chat delivery idempotent"
```

---

### Task 5: Document, Version, and Verify the Release

**Files:**
- Modify: `meet-monitor/manifest.json`
- Modify: `meet-monitor/src/options.html`
- Modify: `meet-monitor/README.md`

**Interfaces:**
- Extension version becomes `1.3.1`.
- User-visible event list no longer advertises `rename`.

- [ ] **Step 1: Update release metadata and documentation**

Set `"version": "1.3.1"` in `manifest.json`. Remove `rename` from the options
event list. Update the README behavior and troubleshooting sections to state:

```text
Participant names are read only from Meet's dedicated name element. Camera and
microphone changes must remain stable for two seconds before they are logged.
Rename events are intentionally not emitted. Chat delivery uses Meet's stable
message ID so refreshes, retries, and multiple tabs do not duplicate a message.
```

Add update instructions: pull the branch, reload Meet Monitor from
`chrome://extensions`, and refresh every open Meet tab.

- [ ] **Step 2: Run JavaScript syntax and extension tests**

Run:

```powershell
node -e "JSON.parse(require('fs').readFileSync('meet-monitor/manifest.json','utf8'))"
node --check meet-monitor/src/selectors.js
node --check meet-monitor/src/state.js
node --check meet-monitor/src/content.js
node --check meet-monitor/src/delivery.js
node --check meet-monitor/src/background.js
node --test meet-monitor/test/*.test.js
```

Expected: every command exits zero and all extension tests PASS.

- [ ] **Step 3: Run complete repository verification**

Run:

```powershell
gofmt -d .
go vet ./...
go test ./... -count=1
go build ./...
git diff --check
git status --short
```

Expected: formatting output is empty; vet, tests, build, and diff check exit
zero; status lists only the intended documentation/version changes.

- [ ] **Step 4: Commit release documentation**

```powershell
git add meet-monitor/manifest.json meet-monitor/src/options.html meet-monitor/README.md
git commit -m "docs: release stable Meet monitoring"
```

- [ ] **Step 5: Perform live acceptance**

On the user's extension laptop:

1. Pull `revive-asg-bot`.
2. Reload Meet Monitor at `chrome://extensions`.
3. Refresh all Meet tabs.
4. Join a controlled Meet with two participants.
5. Open and close participant controls and change layouts; confirm no rename or
   false media messages reach WhatsApp.
6. Toggle camera and microphone; confirm one message after about two seconds.
7. Send one uniquely worded chat message with the same meeting open in a second
   tab; confirm one WhatsApp message.
8. Have the other participant leave; confirm one departure message.

- [ ] **Step 6: Push the branch**

```powershell
git push origin revive-asg-bot
```

Expected: `origin/revive-asg-bot` advances to the verified release commit.
