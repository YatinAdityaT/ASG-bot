# Meet Monitor v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build reliable extension/server state synchronization, silent recovery, heartbeat visibility, `/status`, and a persistent Chrome side panel while sending WhatsApp messages only for confirmed changes.

**Architecture:** The Meet content script produces confirmed roster observations and chat records. The extension service worker elects one source tab, maintains a browser-session ID and monotonic sequence, and sends full snapshots to a server-side state service. The Go service serializes each meeting's transitions, persists the accepted snapshot in SQLite, creates deterministic outbound jobs only for online diffs, and exposes the same state to the authenticated API and `/status`.

**Tech Stack:** Chrome Manifest V3 JavaScript, Chrome storage/alarms/sidePanel APIs, Node.js built-in test runner, Go 1.26, SQLite, whatsmeow, existing persistent outbound queue.

## Global Constraints

- The only monitored meeting is `jpy-ymgz-tdo`.
- The extension protocol version is exactly `1`.
- Extension version for this release is `1.4.0`.
- Heartbeats run every 30 seconds; content is stale after 45 seconds; the server marks the monitor offline after 90 seconds.
- The first snapshot after startup, extension reload, browser restart, source takeover, server reset, session change, more than 90 seconds offline, or forwarding OFF-to-ON is a silent baseline.
- Forwarding OFF suppresses state-change and chat jobs but does not suppress heartbeats or server state updates.
- Routine heartbeats never create unsolicited WhatsApp messages.
- Participant media changes remain subject to the existing two-second confirmation rule; rename events remain disabled.
- Chat continues through `POST /v1/meet-events` and remains idempotent by Meet message ID.
- Automatic WhatsApp Meet messages never contain the meeting code or `Meet <code>:` prefix. `/status` retains the meeting code for diagnostics.
- Existing bearer authentication, exact extension-origin CORS, API rate limiting, fixed destination group, credential protection, webhook support, and invalidated-context shutdown remain in force.
- Tokens, request bodies, participant snapshots, and participant-controlled text are never written to logs or UI history.
- `/pcall` and remote extension distribution remain out of scope.

## File Structure

- Create `meetmonitor/types.go`: snapshot, participant, result, persisted-state, status, and service errors.
- Create `meetmonitor/format.go`: shared prefix-free WhatsApp event formatting.
- Create `meetmonitor/store.go`: SQLite load/upsert operations for one row per meeting.
- Create `meetmonitor/service.go`: validation, canonicalization, sequence handling, silent baselines, deterministic diffs, outbound job creation, and status reads.
- Create `meetmonitor/store_test.go` and `meetmonitor/service_test.go`: persistence and transition behavior.
- Modify `db/db.go` and `db/db_test.go`: idempotent `meet_monitor_state` schema.
- Modify `config/config.go` and `config/config_test.go`: required `ASG_BOT_MEET_CODE`.
- Modify `messageapi/meet.go` and `messageapi/meet_test.go`: prefix-free event/chat formatting.
- Create `messageapi/meet_state.go` and `messageapi/meet_state_test.go`: authenticated 256 KiB snapshot endpoint.
- Modify `messageapi/server.go` and `messageapi/server_test.go`: route and service/connectivity injection.
- Modify `cmd/manager.go`, `cmd/handlers.go`, and `cmd/handlers_test.go`: `/status` provider option and handler.
- Modify `main.go` and `main_test.go`: construct one shared monitor service for API and commands.
- Create `meet-monitor/src/snapshot.js`: canonical roster and state-request helpers.
- Modify `meet-monitor/src/delivery.js` and `meet-monitor/test/delivery.test.js`: derive `/v1/meet-state` while preserving chat delivery.
- Rewrite the synchronization portions of `meet-monitor/src/background.js` and `meet-monitor/test/background.test.js`: election, session, sequence, pending snapshot, heartbeat, retry, history, and view model.
- Modify `meet-monitor/src/content.js`, `meet-monitor/test/state.test.js`, and `meet-monitor/test/content-context.test.js`: confirmed-state observations and source freshness.
- Create `meet-monitor/src/side-panel.html`, `meet-monitor/src/side-panel.js`, and `meet-monitor/test/side-panel.test.js`: persistent monitor UI.
- Modify `meet-monitor/src/popup.html`, `meet-monitor/src/popup.js`, `meet-monitor/manifest.json`, and popup tests: open side panel and use the background model.
- Modify `README.md`, `meet-monitor/README.md`, and `meet-monitor/src/options.html`: operator configuration, behavior, version, and verification.

---

### Task 1: Add Server Configuration, Schema, and Prefix-Free Formatting

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`
- Modify: `db/db.go`
- Modify: `db/db_test.go`
- Create: `meetmonitor/format.go`
- Create: `meetmonitor/format_test.go`
- Modify: `messageapi/meet.go`
- Modify: `messageapi/meet_test.go`

**Interfaces:**
- Produces: `config.Config.MeetCode string`
- Produces: `meetmonitor.FormatEvent(eventType, name, text string) (string, error)`
- Produces: SQLite table `meet_monitor_state`
- Preserves: `POST /v1/meet-events` validation and stable chat idempotency

- [ ] **Step 1: Add failing configuration, schema, and formatting tests**

Add configuration cases that require `ASG_BOT_MEET_CODE` only when the API is
enabled and accept only `^[a-z]{3}-[a-z]{4}-[a-z]{3}$`. Extend the database
schema test to assert all columns in this exact table:

```sql
CREATE TABLE IF NOT EXISTS meet_monitor_state (
  meeting_code TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  snapshot_json TEXT NOT NULL,
  snapshot_hash TEXT NOT NULL,
  extension_version TEXT NOT NULL,
  forwarding_enabled INTEGER NOT NULL CHECK (forwarding_enabled IN (0, 1)),
  last_heartbeat_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

Create `meetmonitor/format_test.go` with table-driven expectations:

```go
tests := []struct{ eventType, text, want string }{
	{"join", "", "🎥 Alex joined the call."},
	{"leave", "", "🚪 Alex left the call."},
	{"mic_muted", "", "🎙️ Alex muted their mic."},
	{"mic_unmuted", "", "🎙️ Alex unmuted their mic."},
	{"cam_on", "", "📷 Alex turned their camera on."},
	{"cam_off", "", "📷 Alex turned their camera off."},
	{"chat_message", "Hello", "💬 Alex said: Hello"},
}
```

Update `messageapi/meet_test.go` to expect these strings and sanitized
`"💬 Alex Admin said: hello world"`. Remove the rename success case and add it
to invalid event types.

- [ ] **Step 2: Run focused tests and verify failure**

```powershell
go test ./config ./db ./meetmonitor ./messageapi -run "Meet|SetupDB" -count=1
```

Expected: FAIL because `MeetCode`, the table, package, and prefix-free
formatter do not exist.

- [ ] **Step 3: Implement configuration and schema**

Add:

```go
var meetCodePattern = regexp.MustCompile(`^[a-z]{3}-[a-z]{4}-[a-z]{3}$`)

type Config struct {
	// existing fields
	MeetCode string
}
```

After API origins are parsed:

```go
cfg.MeetCode = strings.TrimSpace(getenv("ASG_BOT_MEET_CODE"))
if !meetCodePattern.MatchString(cfg.MeetCode) {
	return Config{}, fmt.Errorf("ASG_BOT_MEET_CODE must be a Google Meet code")
}
```

Execute the table DDL from `SetupDB` after `outbound_jobs`, returning
`"create meet monitor state schema"` on failure.

- [ ] **Step 4: Implement and reuse prefix-free formatting**

In `meetmonitor/format.go`, implement `FormatEvent` with the exact messages
from Step 1. It rejects blank names, text on non-chat events, blank chat text,
and every unsupported type. Keep sanitization and length validation in
`messageapi.normalizeAndFormatMeetEvent`, then replace its switch with:

```go
formatted, err := meetmonitor.FormatEvent(event.Type, event.Name, text)
if err != nil {
	return "", err
}
if event.Type == "chat_message" {
	event.Text = &text
}
return formatted, nil
```

Meeting remains required and normalized because it is part of API policy and
idempotency; it is not passed to the formatter.

- [ ] **Step 5: Run focused and complete Go tests**

```powershell
gofmt -w config/config.go config/config_test.go db/db.go db/db_test.go meetmonitor/format.go meetmonitor/format_test.go messageapi/meet.go messageapi/meet_test.go
go test ./config ./db ./meetmonitor ./messageapi -count=1
go test ./... -count=1
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```powershell
git add config db meetmonitor/format.go meetmonitor/format_test.go messageapi/meet.go messageapi/meet_test.go
git commit -m "feat: add Meet monitor server foundations"
```

---

### Task 2: Persist and Reconcile Meet Snapshots

**Files:**
- Create: `meetmonitor/types.go`
- Create: `meetmonitor/store.go`
- Create: `meetmonitor/store_test.go`
- Create: `meetmonitor/service.go`
- Create: `meetmonitor/service_test.go`

**Interfaces:**
- Consumes: `outbound.Store.Create(context.Context, outbound.CreateRequest)`
- Consumes: `FormatEvent(eventType, name, text string)`
- Produces: `NewStore(*sql.DB) *Store`
- Produces: `NewService(StateStore, JobCreator, string, func() bool, func() time.Time) *Service`
- Produces: `Service.Process(context.Context, Snapshot) (Result, error)`
- Produces: `Service.Status(context.Context) (Status, error)`

- [ ] **Step 1: Define failing service tests**

Create fixtures using an in-memory SQLite database, the real monitor store, and
the real outbound store. Cover:

```go
func snapshot(session string, sequence int64, forwarding bool, participants ...Participant) Snapshot {
	return Snapshot{
		ProtocolVersion: 1,
		Meeting: "jpy-ymgz-tdo",
		SessionID: session,
		Sequence: sequence,
		CapturedAt: fixedNow,
		ExtensionVersion: "1.4.0",
		ForwardingEnabled: forwarding,
		Participants: participants,
	}
}
```

Tests must assert:

- first snapshot is `baseline` with zero jobs;
- identical next snapshot is `no_change`;
- add, remove, camera, and microphone diffs produce one prefix-free job each;
- changes are ordered by participant ID, then `join`, `leave`, `cam_on`,
  `cam_off`, `mic_muted`, `mic_unmuted`;
- session change, age greater than 90 seconds, and OFF-to-ON are silent
  baselines;
- forwarding OFF updates state and heartbeat with zero jobs;
- an older sequence returns `stale`;
- same sequence/same canonical request returns `replay`;
- same sequence/different canonical request returns `ErrSequenceConflict`;
- a partial job-creation failure leaves prior state intact, and replay after
  recovery creates no duplicate jobs;
- concurrent calls resolve to one deterministic accepted sequence;
- status reports never-seen, online, and offline states.

- [ ] **Step 2: Run tests and verify failure**

```powershell
go test ./meetmonitor -run "Store|Service|Status" -count=1
```

Expected: FAIL because snapshot types, persistence, and reconciliation do not
exist.

- [ ] **Step 3: Implement public types and validation**

Define:

```go
type Participant struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Cam bool `json:"cam"`
	Mic string `json:"mic"`
	Self bool `json:"self"`
	Host bool `json:"host"`
}

type Snapshot struct {
	ProtocolVersion int `json:"protocolVersion"`
	Meeting string `json:"meeting"`
	SessionID string `json:"sessionId"`
	Sequence int64 `json:"sequence"`
	CapturedAt time.Time `json:"capturedAt"`
	ExtensionVersion string `json:"extensionVersion"`
	ForwardingEnabled bool `json:"forwardingEnabled"`
	Participants []Participant `json:"participants"`
}

type Result struct {
	Status string `json:"status"`
	AcceptedSequence int64 `json:"acceptedSequence"`
	ServerTime time.Time `json:"serverTime"`
	MonitorOnline bool `json:"monitorOnline"`
	WhatsAppConnected bool `json:"whatsAppConnected"`
	QueuedChanges int `json:"queuedChanges"`
}
```

Add `ErrSequenceConflict`. Validation enforces every limit from the design:
protocol `1`, configured meeting, canonical UUID, positive sequence, captured
time within five minutes of server time, extension version 1–32 runes, no more
than 500 participants, unique 1–255-rune IDs, 1–200-rune names, and microphone
exactly `muted` or `unmuted`. Reject control/format characters in IDs; sanitize
names with the same control/format-character policy used by Meet chat.

- [ ] **Step 4: Implement canonical persistence**

`Store.Load(ctx, meeting)` returns `ErrNotSeen` for no row. `Store.Save`
performs an `INSERT ... ON CONFLICT(meeting_code) DO UPDATE` for every persisted
field. Canonical JSON contains participants sorted by ID; the SHA-256 hash is
computed over the normalized complete request identity:

```go
type requestIdentity struct {
	ProtocolVersion int `json:"protocolVersion"`
	Meeting string `json:"meeting"`
	SessionID string `json:"sessionId"`
	Sequence int64 `json:"sequence"`
	CapturedAt time.Time `json:"capturedAt"`
	ExtensionVersion string `json:"extensionVersion"`
	ForwardingEnabled bool `json:"forwardingEnabled"`
	Participants []Participant `json:"participants"`
}
```

Store `last_heartbeat_at` and `updated_at` as UTC Unix nanoseconds.

- [ ] **Step 5: Implement serialized reconciliation**

`Service` owns a mutex so one process cannot interleave snapshots for the same
meeting. `Process` follows this order:

```go
validate -> canonicalize -> hash -> lock -> load -> classify sequence/baseline
-> calculate sorted changes -> create deterministic jobs -> save state -> result
```

Baseline conditions are never-seen, session change, last heartbeat older than
90 seconds, and prior forwarding OFF with current forwarding ON. Forwarding
OFF saves silently. A replay refreshes the heartbeat without creating jobs.
Job keys are SHA-256 hashes over:

```text
meeting | session | sequence | participant ID | change type
```

Create every job before saving the new state. Treat an idempotent replay from
`outbound.Store.Create` as success. Any other error returns without saving,
allowing the same request to resume safely.

- [ ] **Step 6: Run package and full Go tests**

```powershell
gofmt -w meetmonitor
go test ./meetmonitor -count=1
go test ./... -count=1
```

Expected: all tests PASS, including race-free concurrent snapshot tests.

- [ ] **Step 7: Commit**

```powershell
git add meetmonitor
git commit -m "feat: reconcile persisted Meet snapshots"
```

---

### Task 3: Expose `POST /v1/meet-state` and Wire Runtime Health

**Files:**
- Create: `messageapi/meet_state.go`
- Create: `messageapi/meet_state_test.go`
- Modify: `messageapi/server.go`
- Modify: `messageapi/server_test.go`
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `meetmonitor.Service.Process`
- Produces: authenticated `POST /v1/meet-state`
- Changes: `messageapi.New` accepts options without breaking existing tests
- Produces: `messageapi.WithMeetService(*meetmonitor.Service)`

- [ ] **Step 1: Add failing endpoint tests**

Add a fixture with a real service and submit:

```json
{
  "protocolVersion": 1,
  "meeting": "jpy-ymgz-tdo",
  "sessionId": "8f14e45f-ea2f-4f41-9f6d-44c5b95c6301",
  "sequence": 1,
  "capturedAt": "2026-11-03T10:00:00Z",
  "extensionVersion": "1.4.0",
  "forwardingEnabled": true,
  "participants": []
}
```

Assert `200 OK`, `status: baseline`, accepted sequence, server time,
`monitorOnline: true`, injected WhatsApp connectivity, and zero queued
changes. Also cover unknown fields, malformed JSON, wrong content type,
wrong meeting, invalid UUID/time/participants, 256 KiB limit, method policy,
authentication, exact-origin CORS, rate limiting, stale/replay/conflict, and
concurrent submissions. A same-sequence conflict returns:

```json
{"error":{"code":"sequence_conflict","message":"snapshot conflicts with the accepted sequence"}}
```

- [ ] **Step 2: Run endpoint tests and verify failure**

```powershell
go test ./messageapi -run "MeetState" -count=1
```

Expected: FAIL because the route and handler do not exist.

- [ ] **Step 3: Implement the handler and server option**

Add `maxMeetStateBodyBytes = 256 * 1024`. The handler parses only
`application/json`, wraps the body with that limit, uses
`DisallowUnknownFields`, requires JSON EOF, calls `Service.Process`, maps
`ErrSequenceConflict` to `409`, validation to `400`, body size to `413`, and
other errors to `500`.

Add:

```go
type Option func(*Server)

func WithMeetService(service *meetmonitor.Service) Option {
	return func(server *Server) { server.meet = service }
}

func New(cfg config.Config, store *outbound.Store, now func() time.Time, options ...Option) *Server
```

Register exact path `/v1/meet-state`; return `503 service_unavailable` if no
monitor service was injected.

- [ ] **Step 4: Construct one service in `main.go`**

After the WhatsApp client and outbound store exist:

```go
monitorStore := meetmonitor.NewStore(database)
monitorService := meetmonitor.NewService(
	monitorStore,
	outboundStore,
	cfg.MeetCode,
	client.IsConnected,
	time.Now,
)
```

Pass it to the API through `messageapi.WithMeetService`. Extend
`runtimeDependencies.startAPI` and the main tests to receive the service.
Assert startup passes the configured meeting and live connectivity function.

- [ ] **Step 5: Run formatting, vet, and tests**

```powershell
gofmt -w messageapi/meet_state.go messageapi/meet_state_test.go messageapi/server.go messageapi/server_test.go main.go main_test.go
go test ./messageapi -count=1
go test ./... -count=1
go vet ./...
```

Expected: all commands exit zero.

- [ ] **Step 6: Commit**

```powershell
git add messageapi main.go main_test.go
git commit -m "feat: accept Meet monitor snapshots"
```

---

### Task 4: Add the WhatsApp `/status` Command

**Files:**
- Modify: `cmd/manager.go`
- Modify: `cmd/handlers.go`
- Modify: `cmd/handlers_test.go`
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**
- Consumes: `meetmonitor.Service.Status(context.Context)`
- Produces: `cmd.WithStatusProvider(StatusProvider) ManagerOption`
- Produces: non-admin `/status`

- [ ] **Step 1: Add failing command tests**

Define a fake provider and test exact responses:

```text
Bot: online
WhatsApp: connected
Monitor: online
Last heartbeat: 12s ago
Meeting: jpy-ymgz-tdo
Participants: 7
Forwarding: ON
Extension: 1.4.0
```

Also assert `Monitor: never seen`, offline age after 90 seconds, forwarding
OFF, disconnected WhatsApp, provider error propagation, case-insensitive
matching, help listing, and no administrator requirement.

- [ ] **Step 2: Run command tests and verify failure**

```powershell
go test ./cmd -run "Status|Help" -count=1
```

Expected: FAIL because `/status` and provider injection do not exist.

- [ ] **Step 3: Add provider option and handler**

Define:

```go
type StatusProvider interface {
	Status(context.Context) (meetmonitor.Status, error)
}

type ManagerOption func(*CommandManager)

func WithStatusProvider(provider StatusProvider) ManagerOption {
	return func(manager *CommandManager) { manager.statusProvider = provider }
}
```

Change `NewCommandManager` to accept `options ...ManagerOption`, preserving all
existing call sites. Register `/status` only when a provider is supplied.
`handleStatus` formats elapsed time as `Xs`, `Ym`, or `Zh`; it obtains bot and
WhatsApp connectivity from the returned status and replies through
`utils.ReplyToMessage`.

- [ ] **Step 4: Inject the shared service**

Create the monitor service before `cmd.NewCommandManager`, then pass
`cmd.WithStatusProvider(monitorService)`. API and command paths must share the
same service, store, clock, and connectivity callback.

- [ ] **Step 5: Run command and full Go tests**

```powershell
gofmt -w cmd/manager.go cmd/handlers.go cmd/handlers_test.go main.go main_test.go
go test ./cmd -count=1
go test ./... -count=1
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```powershell
git add cmd main.go main_test.go
git commit -m "feat: report Meet monitor status"
```

---

### Task 5: Add Extension Snapshot and Delivery Primitives

**Files:**
- Create: `meet-monitor/src/snapshot.js`
- Create: `meet-monitor/test/snapshot.test.js`
- Modify: `meet-monitor/src/delivery.js`
- Modify: `meet-monitor/test/delivery.test.js`
- Modify: `meet-monitor/src/options.js`
- Modify: `meet-monitor/src/options.html`

**Interfaces:**
- Produces: `MMSnapshot.canonicalParticipants(roster)`
- Produces: `MMSnapshot.build(input)`
- Produces: `MMSnapshot.stateKey(snapshot)`
- Produces: `MMDelivery.eventRequestFor(event, configuredURL, token)`
- Produces: `MMDelivery.stateRequestFor(snapshot, configuredURL, token)`
- Preserves: saved URL ending in `/v1/meet-events`

- [ ] **Step 1: Add failing pure JavaScript tests**

Test canonical sorting, exact snapshot shape, participant normalization, stable
state keys, URL safety, and request bodies. The state request derived from
`https://bot.example.com/v1/meet-events` must target:

```text
https://bot.example.com/v1/meet-state
```

Both request builders must preserve the bearer header. Reject query strings,
fragments, credentials, non-HTTPS remote URLs, and non-loopback HTTP URLs.

- [ ] **Step 2: Run tests and verify failure**

```powershell
node --test meet-monitor/test/snapshot.test.js meet-monitor/test/delivery.test.js
```

Expected: FAIL because snapshot helpers and the state request do not exist.

- [ ] **Step 3: Implement `snapshot.js`**

Use the existing browser/CommonJS module pattern. Canonical participants are:

```js
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
```

`build` emits only the protocol fields from the design. `stateKey` serializes
meeting, session, forwarding, extension version, and canonical participants;
it excludes sequence and captured time so unchanged roster heartbeats can be
identified without suppressing their submission.

- [ ] **Step 4: Split event and snapshot request builders**

Keep `isAllowedBotAPIURL` validating the configured `/v1/meet-events` URL.
Rename the current request function to `eventRequestFor`. Implement
`stateRequestFor` by replacing the validated URL pathname with
`/v1/meet-state`. Task 6 updates all background callers. Retain `requestFor`
as an alias to `eventRequestFor` for this commit so existing tests remain
green.

Update option copy to say that the configured endpoint ends in
`/v1/meet-events` and the extension securely derives its state endpoint.

- [ ] **Step 5: Run all extension tests**

```powershell
node --check meet-monitor/src/snapshot.js
node --check meet-monitor/src/delivery.js
node --test meet-monitor/test/*.test.js
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```powershell
git add meet-monitor/src/snapshot.js meet-monitor/src/delivery.js meet-monitor/src/options.js meet-monitor/src/options.html meet-monitor/test/snapshot.test.js meet-monitor/test/delivery.test.js
git commit -m "feat: build Meet state requests"
```

---

### Task 6: Elect One Source and Synchronize Heartbeats

**Files:**
- Modify: `meet-monitor/src/background.js`
- Modify: `meet-monitor/src/content.js`
- Modify: `meet-monitor/test/background.test.js`
- Modify: `meet-monitor/test/content-context.test.js`

**Interfaces:**
- Content to background: `{ kind: 'monitorObservation', meeting, observedAt, roster, self, changes }`
- Content to background chat: existing `{ kind: 'event', rec }`
- View to background: `{ kind: 'getMonitorModel' }`
- Background to views: `{ kind: 'monitorModelChanged', model }`
- Session storage: `monitorSession`
- Session storage: `pendingMeetSnapshot`
- Local storage: `monitorHistory`

- [ ] **Step 1: Replace per-participant delivery tests with synchronization tests**

Extend the Chrome harness with `storage.session`, sender tab IDs, alarm
creation, runtime version, and delayed fetches. Assert:

- first healthy tab is elected and starts a UUID session at sequence 1;
- a second tab cannot advance sequence, heartbeat, chat, or webhook;
- elected source takeover after 45 seconds creates a new session and sequence
  1;
- a confirmed roster change submits immediately;
- alarm `meet-state-heartbeat` submits after 30 seconds;
- stale content never heartbeats;
- retries preserve sequence and body;
- one newer pending snapshot supersedes one unsent older snapshot;
- 408/425/429/5xx retry and auth/origin/validation failures are permanent;
- 409 is surfaced as a sequence conflict rather than treated as success;
- service-worker restoration reads session/pending state;
- forwarding OFF still sends snapshots with `forwardingEnabled: false` and
  suppresses chat/webhook;
- OFF-to-ON sends an immediate snapshot that the server treats as baseline;
- non-chat events never call `/v1/meet-events`;
- chat continues to use the existing outbox and idempotent event request;
- credentials and request bodies never enter console or history.

- [ ] **Step 2: Run background tests and verify failure**

```powershell
node --test meet-monitor/test/background.test.js meet-monitor/test/content-context.test.js
```

Expected: FAIL because source election and state synchronization do not exist.

- [ ] **Step 3: Emit complete confirmed observations from content**

After every scan, send the confirmed roster plus a local-only raw observation:

```js
deliver({
  kind: 'monitorObservation',
  meeting,
  observedAt,
  roster: Object.fromEntries(roster),
  rawRoster: Object.fromEntries(now),
  self: S.selfState(),
  changes
});
```

Collect tracker changes before emitting individual local records. Preserve
chat `event` messages and invalidated-context handling. The observation roster
contains only confirmed tracker state. `rawRoster` is used only when local
debug mode is enabled and is stripped before building API, webhook, or
WhatsApp payloads.

- [ ] **Step 4: Implement session and source election**

Add constants:

```js
const HEARTBEAT_ALARM = 'meet-state-heartbeat';
const HEARTBEAT_MS = 30_000;
const SOURCE_STALE_MS = 45_000;
const SESSION_KEY = 'monitorSession';
const PENDING_SNAPSHOT_KEY = 'pendingMeetSnapshot';
const HISTORY_KEY = 'monitorHistory';
```

An observation is eligible only when its meeting is `jpy-ymgz-tdo` and
`sender.tab.id` is present. Elect the first healthy tab. If the elected source
is stale or missing, elect the new tab, call `crypto.randomUUID()`, reset
sequence to zero, and send a silent-baseline snapshot. Persist source tab ID,
session ID, sequence, latest confirmed roster, and observation time in
`chrome.storage.session`.

- [ ] **Step 5: Implement snapshot scheduling and retries**

Use one serialized operation chain. Creating a new submission increments
sequence once, builds an immutable body, and stores it before fetch. Retry the
same body and sequence. A newer observation replaces an unsent pending body;
an in-flight body completes before the latest state is built. Schedule a
periodic 30-second alarm with:

```js
chrome.alarms.create(HEARTBEAT_ALARM, { periodInMinutes: 0.5 });
```

Do not submit when `Date.now() - observedAt > SOURCE_STALE_MS`. On every
accepted response, store server time, result status, accepted sequence,
queued changes, monitor online, WhatsApp connectivity, and last success.

- [ ] **Step 6: Preserve chat and webhook behavior only for the elected source**

Route `chat_message` through `eventRequestFor` and the current bounded outbox
only when forwarding is ON. Stop inserting join/leave/media records into that
outbox. Keep the generic webhook for confirmed records, but apply the same
meeting, forwarding, elected-source, and duplicate policies.

- [ ] **Step 7: Run extension tests**

```powershell
node --check meet-monitor/src/content.js
node --check meet-monitor/src/background.js
node --test meet-monitor/test/*.test.js
```

Expected: all tests PASS.

- [ ] **Step 8: Commit**

```powershell
git add meet-monitor/src/background.js meet-monitor/src/content.js meet-monitor/test/background.test.js meet-monitor/test/content-context.test.js
git commit -m "feat: synchronize Meet monitor heartbeats"
```

---

### Task 7: Add Bounded History and the Chrome Side Panel

**Files:**
- Create: `meet-monitor/src/side-panel.html`
- Create: `meet-monitor/src/side-panel.js`
- Create: `meet-monitor/test/side-panel.test.js`
- Modify: `meet-monitor/src/background.js`
- Modify: `meet-monitor/src/popup.html`
- Modify: `meet-monitor/src/popup.js`
- Modify: `meet-monitor/test/background.test.js`
- Modify: `meet-monitor/manifest.json`

**Interfaces:**
- Produces: background monitor model with roster, health, forwarding, and history
- Produces: side-panel messages `setForwarding`, `setDebugMode`, `clearMonitorHistory`
- Produces: CSV export from confirmed history
- Changes: popup reads the background model and opens the side panel

- [ ] **Step 1: Add failing model and UI tests**

Assert confirmed history is stored in `chrome.storage.local` and bounded to
1,000 records. Debug history is opt-in, stored in `chrome.storage.session`,
bounded to 250 records, and never appears in network requests. History entries
contain timestamp, type, participant ID/name, state/text, meeting, sequence,
and delivery state (`pending`, `accepted`, `silent`, or `failed`).

Load `side-panel.js` in a DOM harness and test roster rendering, mic/camera
icons, self/host badges, meeting/version, monitor/content/API/WhatsApp health,
event filters, forwarding toggle, debug toggle, clear, CSV escaping, and
permanent error display. Test popup `Open Monitor` calls:

```js
chrome.sidePanel.open({ windowId: tab.windowId })
```

- [ ] **Step 2: Run tests and verify failure**

```powershell
node --test meet-monitor/test/background.test.js meet-monitor/test/side-panel.test.js
```

Expected: FAIL because model persistence and side-panel assets do not exist.

- [ ] **Step 3: Build and broadcast the monitor model**

Add `getMonitorModel` and trusted extension-view control messages to
`background.js`. Return:

```js
{
  meeting,
  extensionVersion,
  forwardingEnabled,
  debugEnabled,
  content: { online, lastObservedAt, sourceTabId },
  api: { online, lastSuccessAt, serverTime, result, error },
  whatsAppConnected,
  roster,
  history,
  debugHistory
}
```

After each model mutation, call `chrome.runtime.sendMessage` with
`monitorModelChanged`; ignore the expected no-listener rejection. Mark local
events accepted/silent/failed when their snapshot resolves.

- [ ] **Step 4: Build the side panel**

Create accessible controls and sections for health, roster, timeline, filters,
forwarding, debug mode, clear, export, and options. Render with `textContent`
or an escaping helper; never interpolate participant-controlled text without
escaping. Side panel startup requests the complete model, then listens for
model broadcasts.

- [ ] **Step 5: Update popup and manifest**

Add `"sidePanel"` to permissions, add:

```json
"side_panel": { "default_path": "src/side-panel.html" }
```

Load `snapshot.js` before `background.js` using:

```js
importScripts('delivery.js', 'snapshot.js');
```

Replace popup polling of the active content script with `getMonitorModel`.
Retain its compact roster/feed, Sending toggle, CSV, and Options button; add
`Open Monitor`.

- [ ] **Step 6: Run all extension tests and syntax checks**

```powershell
node --check meet-monitor/src/background.js
node --check meet-monitor/src/content.js
node --check meet-monitor/src/delivery.js
node --check meet-monitor/src/snapshot.js
node --check meet-monitor/src/popup.js
node --check meet-monitor/src/side-panel.js
node --test meet-monitor/test/*.test.js
```

Expected: all commands exit zero.

- [ ] **Step 7: Commit**

```powershell
git add meet-monitor/src meet-monitor/test meet-monitor/manifest.json
git commit -m "feat: add Meet monitor side panel"
```

---

### Task 8: Document, Version, Verify, and Release

**Files:**
- Modify: `meet-monitor/manifest.json`
- Modify: `meet-monitor/README.md`
- Modify: `README.md`

**Interfaces:**
- Extension version is `1.4.0`
- Operator environment includes `ASG_BOT_MEET_CODE=jpy-ymgz-tdo`
- Release is merged directly to `master`

- [ ] **Step 1: Update release and operating documentation**

Set manifest version `1.4.0`. Document:

```powershell
[Environment]::SetEnvironmentVariable(
  "ASG_BOT_MEET_CODE",
  "jpy-ymgz-tdo",
  "User"
)
```

Document extension reload, Meet-page refresh, side-panel controls, 30/45/90
second health thresholds, silent recovery, forwarding semantics, `/status`,
prefix-free WhatsApp examples, chat compatibility, and permanent versus
retryable errors. State explicitly that the server laptop, bot process,
Cloudflare tunnel service, monitor laptop, Meet tab, and extension must remain
running for continuous monitoring.

- [ ] **Step 2: Run complete automated verification**

```powershell
node -e "JSON.parse(require('fs').readFileSync('meet-monitor/manifest.json','utf8'))"
Get-ChildItem meet-monitor/src/*.js | ForEach-Object { node --check $_.FullName; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE } }
node --test meet-monitor/test/*.test.js
gofmt -d .
go vet ./...
go test ./... -count=1
go build ./...
git diff --check
```

Expected: JSON and syntax checks exit zero; all Node and Go tests PASS;
`gofmt -d .` prints nothing; vet/build/diff check exit zero.

- [ ] **Step 3: Commit documentation**

```powershell
git add README.md meet-monitor/README.md meet-monitor/manifest.json
git commit -m "docs: release Meet Monitor v1.4.0"
```

- [ ] **Step 4: Run whole-branch review and resolve findings**

Review all commits from the design commit through `HEAD` against
`docs/superpowers/specs/2026-11-03-meet-monitor-v2-design.md`. Fix every
correctness, security, test, or documentation finding in focused commits, then
repeat Step 2.

- [ ] **Step 5: Perform live acceptance**

1. Start the bot with `ASG_BOT_MEET_CODE=jpy-ymgz-tdo`.
2. Reload extension `1.4.0` and refresh all Meet tabs.
3. Open the side panel; verify silent baseline, roster, and healthy heartbeat.
4. Wait at least 30 seconds; verify no WhatsApp heartbeat message.
5. Run `/status`; verify meeting, online state, participant count, forwarding,
   extension version, and recent heartbeat.
6. Perform one join, leave, mic, and camera change; verify exactly one
   prefix-free WhatsApp message each.
7. Send one chat; verify exactly one prefix-free WhatsApp message.
8. Turn forwarding OFF; verify heartbeat remains healthy and events stay local.
9. Turn forwarding ON; verify silent baseline and only the next change posts.
10. Stop monitoring for more than 90 seconds; verify `/status` reports offline.
11. Reconnect with a different roster; verify silent recovery.
12. Open a second same-meeting tab; verify one elected source.
13. Enable raw debug mode; verify local-only observations.

- [ ] **Step 6: Push verified `master`**

```powershell
git status --short
git log -1 --oneline
git push origin master
```

Expected: no unintended working-tree changes and `origin/master` advances to
the verified release commit.
