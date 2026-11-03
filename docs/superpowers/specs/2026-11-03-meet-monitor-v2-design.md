# Meet Monitor v2 State Synchronization and Sidebar Design

## Goal

Meet Monitor must operate continuously from the dedicated Google Meet account
named `ADHD SUPPORT GROUP`, maintain consistent call state in both the Chrome
extension and ASG bot server, and send WhatsApp messages only for confirmed
changes. Routine heartbeats must never create group messages.

The account name is informational in this release. No event filtering or
identity decisions depend on that display name.

## Scope

This release includes:

- extension/server snapshot synchronization;
- a silent baseline after startup, reload, or offline recovery;
- 30-second heartbeats and 90-second offline detection;
- server-side state diffing and WhatsApp change delivery;
- a `/status` WhatsApp command;
- a persistent Chrome side panel for roster, events, and health;
- confirmed-event history with optional raw-observation debug mode; and
- documentation, migrations, tests, and operating instructions.

The future `/pcall` command and automatic extension distribution are explicitly
out of scope.

## Architecture

The extension owns DOM observation and confirmation. The server owns the
authoritative accepted snapshot and the decision to create WhatsApp jobs.

The content script continues to:

- extract stable participant identity and names;
- retain participants until Meet explicitly announces departure;
- confirm camera and microphone changes for two seconds;
- identify chats by Meet message ID; and
- stop stale work when its extension context is invalidated.

The background service worker owns:

- one elected content source for the locked meeting;
- extension session ID and monotonic sequence;
- the latest confirmed snapshot and content-observation time;
- 30-second synchronization alarms;
- endpoint credentials and retry behavior;
- bounded side-panel event history; and
- live status broadcasts to the popup and side panel.

The server persists the latest accepted state and computes diffs. Existing
outbound jobs remain the only path to WhatsApp.

## Locked Meeting and Configuration

The extension remains locked to meeting `jpy-ymgz-tdo`.

The server adds required API configuration:

```text
ASG_BOT_MEET_CODE=jpy-ymgz-tdo
```

When the API is enabled, this value must be a valid Meet code and snapshots for
any other meeting are rejected. Existing bearer authentication, exact
extension-origin CORS, rate limiting, and fixed WhatsApp destination remain in
force.

## Snapshot Protocol

The extension submits:

```http
POST /v1/meet-state
Authorization: Bearer <token>
Content-Type: application/json
```

Request shape:

```json
{
  "protocolVersion": 1,
  "meeting": "jpy-ymgz-tdo",
  "sessionId": "UUID",
  "sequence": 42,
  "capturedAt": "2026-11-03T10:00:00.000Z",
  "extensionVersion": "1.4.0",
  "forwardingEnabled": true,
  "participants": [
    {
      "id": "participant-id",
      "name": "Participant",
      "cam": false,
      "mic": "muted",
      "self": false,
      "host": false
    }
  ]
}
```

Validation rules:

- protocol version is exactly `1`;
- meeting equals `ASG_BOT_MEET_CODE`;
- session ID is a canonical UUID;
- sequence is a positive integer;
- timestamp is valid RFC 3339 and within five minutes of server time;
- extension version is non-empty and at most 32 characters;
- at most 500 participants are accepted;
- participant IDs are unique, non-empty, and at most 255 Unicode code points;
- names are non-empty and at most 200 Unicode code points;
- camera is Boolean and microphone is exactly `muted` or `unmuted`;
- unknown JSON fields, malformed data, and bodies over 256 KiB are rejected;
- participant-controlled strings use the existing control-character and
  formatting sanitization.

Participants are canonicalized by participant ID before hashing or diffing.

Response shape:

```json
{
  "status": "baseline",
  "acceptedSequence": 42,
  "serverTime": "2026-11-03T10:00:00Z",
  "monitorOnline": true,
  "whatsAppConnected": true,
  "queuedChanges": 0
}
```

`status` is one of:

- `baseline`: accepted silently as a new baseline;
- `no_change`: heartbeat accepted with no state difference;
- `changed`: diff accepted and one or more jobs queued;
- `stale`: an older sequence was ignored; or
- `replay`: the same sequence and snapshot were already accepted.

The same sequence with different content returns `409 Conflict`.

## Session, Sequence, and Silent Recovery

The extension session ID lives in `chrome.storage.session`, so service-worker
suspension does not create a new session. Browser restart, extension reload, or
source takeover creates a new session.

The first snapshot for any new session is always a silent baseline.

The server treats a snapshot as a silent baseline when:

- it has never seen the meeting;
- the session ID changes;
- the previous heartbeat is more than 90 seconds old;
- forwarding changes from OFF to ON; or
- server state has been reset.

This deliberately does not infer or announce joins, leaves, or media changes
that may have happened while monitoring was offline.

Within one live session:

- sequence numbers increase for every distinct snapshot submission;
- an older sequence is ignored as stale;
- an identical replay is idempotent;
- a same-sequence content conflict is rejected; and
- state updates and job identities remain deterministic across request retries.

## Heartbeat and Liveness

The content source refreshes its observation timestamp at least every 30
seconds. The background sends the latest snapshot immediately after a confirmed
change and on a 30-second Chrome alarm.

The background sends a heartbeat only when the elected content source has been
observed within the previous 45 seconds. A cached snapshot from a dead or
suspended Meet page must not keep the monitor online.

The monitor is online when the server's last accepted heartbeat is no more than
90 seconds old.

The Sending toggle becomes a forwarding toggle:

- ON: heartbeats continue and online state diffs may create WhatsApp jobs.
- OFF: heartbeats continue, state is updated silently, and no state or chat
  messages are queued.
- OFF-to-ON: the first enabled snapshot is a silent baseline, preventing a
  backlog of inferred changes.

Every heartbeat response is also a server heartbeat. The extension records the
last successful response, round-trip result, accepted sequence, server time,
and WhatsApp connectivity for display.

## State Diff and WhatsApp Delivery

For an online, same-session, forwarding-enabled snapshot, the server compares
the prior and current canonical participant maps.

It creates changes for:

- participant added: joined;
- participant removed: left;
- camera Boolean changed: camera on/off; and
- microphone value changed: muted/unmuted.

Rename events remain disabled. Changes are sorted deterministically by
participant ID and change type.

Each outbound job uses an idempotency key derived from meeting, session,
sequence, participant ID, and change type. If the server crashes after creating
jobs but before persisting the snapshot, replay creates no duplicate jobs and
can complete the state update.

Snapshot persistence occurs only after all deterministic jobs have been
created or replayed successfully. An error leaves the prior snapshot intact and
returns a retryable server error.

The existing `/v1/meet-events` endpoint remains for chat delivery and backward
compatibility. The extension stops sending participant state events through
that endpoint. Chat continues to use Meet message ID for persistent
idempotency and is suppressed while forwarding is OFF.

Automatic WhatsApp Meet messages never include the meeting code or a
`Meet <code>:` prefix. The meeting code remains internal for validation,
state, and idempotency. Event text is formatted as:

```text
🎥 Alex joined the call.
🚪 Alex left the call.
🎙️ Alex muted their mic.
🎙️ Alex unmuted their mic.
📷 Alex turned their camera on.
📷 Alex turned their camera off.
💬 Alex said: Hello
```

## Persistent Server State

SQLite adds one row per meeting:

```text
meet_monitor_state
  meeting_code          PRIMARY KEY
  session_id            NOT NULL
  sequence              NOT NULL
  snapshot_json         NOT NULL
  snapshot_hash         NOT NULL
  extension_version     NOT NULL
  forwarding_enabled    NOT NULL
  last_heartbeat_at     NOT NULL
  updated_at            NOT NULL
```

Schema creation is idempotent and covered by database tests. Snapshot JSON is
canonical and bounded by API validation.

The state store provides transaction-safe load and compare-and-update
operations. It does not expose tokens, participant data, or snapshots in logs.

## WhatsApp `/status` Command

`/status` is available only in existing allowed groups, like other commands.
It requires no administrator privilege.

Example online response:

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

When no heartbeat has ever been received, it reports `Monitor: never seen`.
After 90 seconds, it reports `Monitor: offline` and the last-seen age.

The command reads persisted state through a small status-provider interface.
Command code does not directly own the database. Existing unknown-command,
group allowlist, and error propagation behavior remain unchanged.

Unlike automatic event messages, the manually requested `/status` response
retains the meeting code for diagnostics.

Routine heartbeats never post unsolicited WhatsApp messages.

## Chrome Side Panel

The manifest adds the `sidePanel` permission and a side-panel page. The current
popup remains and gains an `Open Monitor` button.

The side panel shows:

- meeting code and extension version;
- forwarding ON/OFF control;
- monitor online/offline and content freshness;
- API/bot heartbeat status and last successful synchronization;
- WhatsApp connected/disconnected from the latest response;
- current confirmed roster with mic/camera, self, and host badges;
- confirmed event timeline with timestamps and delivery status;
- filters by event type;
- clear-local-history and CSV export actions; and
- an optional debug mode for raw observations.

Confirmed history is stored in `chrome.storage.local`, bounded to the latest
1,000 entries. Clearing it affects only the local display.

Raw debug observations:

- are disabled by default;
- are never forwarded to the server or WhatsApp;
- are kept only in `chrome.storage.session`;
- are bounded to 250 entries; and
- clearly indicate that they are diagnostic, not confirmed events.

The background service worker broadcasts updates to open extension views.
Views also request a complete current model when opened, so service-worker or
side-panel suspension does not lose display state.

## Source Election

Only one content source may synchronize the locked meeting.

The background elects the first healthy source and records its tab/source ID.
Messages from other tabs remain local and cannot advance sequence or heartbeat.
If the source disappears or is stale for more than 45 seconds, a healthy source
may take over. Takeover creates a new session and therefore a silent baseline.

This prevents duplicate snapshots and state churn when the same meeting is open
in multiple tabs.

## Error and Retry Behavior

There is at most one pending snapshot per session. A newer snapshot supersedes
an unsent older snapshot because it contains the full current state.

Transient network, `408`, `425`, `429`, and `5xx` failures retry with the
existing bounded backoff model. Authentication, origin, validation, and meeting
policy failures are permanent and displayed prominently in the side panel.

Heartbeat retries do not create additional sequence numbers. A snapshot keeps
its sequence and canonical body until accepted or superseded.

The side panel distinguishes:

- content source unavailable;
- API unreachable;
- unauthorized;
- extension origin forbidden;
- invalid snapshot;
- bot/WhatsApp disconnected; and
- healthy/no change.

Tokens and request bodies are never written to console logs or side-panel
history.

## Testing

Extension tests cover:

- canonical snapshot construction;
- source election and takeover;
- session and monotonic sequence persistence;
- immediate change sync and 30-second heartbeat;
- refusal to heartbeat stale content;
- forwarding OFF heartbeat behavior;
- one-pending-snapshot supersession;
- retry identity stability;
- side-panel model, filters, toggle, clear, and CSV;
- bounded confirmed and debug history;
- service-worker restart restoration;
- no raw observation forwarding; and
- existing participant, chat, outbox, and invalidated-context behavior.

Server tests cover:

- schema idempotency and persistence;
- validation and body limits;
- canonical hashing;
- baseline conditions;
- heartbeat-only updates;
- stale, replay, and conflict sequences;
- deterministic diffs and job idempotency;
- automatic event and chat formatting without meeting-code prefixes;
- crash-safe replay ordering;
- forwarding OFF and OFF-to-ON baselines;
- 90-second offline recovery;
- meeting lock, authentication, CORS, and rate limits; and
- concurrent snapshot submissions.

Command tests cover online, offline, never-seen, forwarding state, participant
count, allowlist behavior, and provider errors.

Release verification includes JavaScript syntax, all Node tests, formatting,
`go vet ./...`, `go test ./...`, `go build ./...`, diff checks, and live
two-participant acceptance.

## Live Acceptance

1. Start the bot with `ASG_BOT_MEET_CODE=jpy-ymgz-tdo`.
2. Reload version `1.4.0` of the extension and refresh Meet.
3. Open the side panel and confirm a silent baseline, roster, and healthy
   heartbeat.
4. Wait more than 30 seconds and confirm no WhatsApp heartbeat message.
5. Run `/status` and confirm online status, participant count, and recent age.
6. Perform one join, leave, mic, and camera change; confirm exactly one
   correctly formatted WhatsApp message for each, with no meeting code or
   `Meet <code>:` prefix.
7. Send one Meet chat message and confirm exactly one WhatsApp message with no
   meeting code or `Meet <code>:` prefix.
8. Turn forwarding OFF; confirm heartbeats stay healthy and changes remain
   silent.
9. Turn forwarding ON; confirm a silent baseline, then confirm the next change
   posts once.
10. Stop the extension or close Meet for more than 90 seconds; confirm `/status`
   reports offline.
11. Reconnect with a changed roster; confirm silent recovery and no inferred
    downtime messages.
12. Open a second tab for the same meeting; confirm only one elected source
    advances state.
13. Enable debug mode; confirm raw observations appear locally and never reach
    the API or WhatsApp.
