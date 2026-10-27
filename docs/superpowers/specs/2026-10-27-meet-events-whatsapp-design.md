# Meet Events to WhatsApp Design

## Goal

Forward Google Meet activity captured by the Meet Monitor extension to the
bot's configured WhatsApp group as readable, durable outbound messages.

## Architecture

The extension sends its existing event record to a dedicated authenticated
`POST /v1/meet-events` route. The Go service validates and formats the event,
derives a stable idempotency key, and inserts the resulting text into the
existing persistent outbound queue. The existing scheduler and WhatsApp sender
deliver it to `ASG_BOT_API_TARGET_GROUP`; clients cannot choose a destination.

The existing generic webhook remains available. Bot API credentials are
configured separately so Google Apps Script or another webhook can still
receive the raw event.

## HTTP Contract

`POST /v1/meet-events` uses the same bearer authentication, exact-origin CORS,
rate limiting, 16 KiB request limit, and JSON-only content policy as
`POST /v1/messages`.

Request:

```json
{
  "type": "chat_message",
  "id": "meet-message-id",
  "meeting": "abc-defg-hij",
  "ts": "2026-10-27T08:00:00.000Z",
  "name": "Alex",
  "cam": null,
  "mic": null,
  "self": false,
  "host": false,
  "text": "Hello"
}
```

Accepted event types are `join`, `leave`, `cam_on`, `cam_off`, `mic_muted`,
`mic_unmuted`, `rename`, and `chat_message`. `id`, `meeting`, `ts`, and `name`
are required nonblank strings. `ts` must be RFC 3339. `text` is required and
nonblank only for `chat_message`; it must be absent or blank for other types.
Unknown fields and unsupported event types are rejected. Names are limited to
200 Unicode code points, meeting and event IDs to 255, and chat text to 3,500
so formatted messages remain within WhatsApp's 4,096-code-point limit.

Successful submission returns `202 Accepted` using the existing message job
response (`id`, `status`, `sendAt`). Replaying the identical event returns the
same job.

## Formatting

Messages use these forms:

- `🎥 Meet abc-defg-hij: Alex joined the call.`
- `🎥 Meet abc-defg-hij: Alex left the call.`
- `🎙️ Meet abc-defg-hij: Alex muted their mic.`
- `🎙️ Meet abc-defg-hij: Alex unmuted their mic.`
- `📷 Meet abc-defg-hij: Alex turned their camera on.`
- `📷 Meet abc-defg-hij: Alex turned their camera off.`
- `✏️ Meet abc-defg-hij: A participant is now named Alex.`
- `💬 Meet abc-defg-hij: Alex said: Hello`

Names, meeting codes, and chat text are treated only as plain message content.
They never affect the destination or authentication. Control characters,
line breaks, and bidirectional formatting controls are removed before
formatting so one event cannot visually impersonate multiple bot messages.

## Idempotency and Delivery

The server hashes the normalized event identity (`type`, `id`, `meeting`, and
`ts`) into a stable `meet-<sha256>` idempotency key. Equivalent
representations of ignored state therefore cannot create duplicate WhatsApp
messages. Browser or network retries return the existing outbound job. Once
accepted, events inherit the outbound queue's persistence, retry schedule,
stable WhatsApp message ID, crash recovery, and best-effort at-least-once
semantics.

## Extension Configuration

Options add:

- Bot API URL, ending in `/v1/meet-events`.
- Bot API token, stored in `chrome.storage.local` rather than synced storage.

The URL and banner preference remain in synced storage. Saving requests the
exact origin permission. Remote Bot API URLs require HTTPS; HTTP is allowed
only for loopback development. The service worker restricts local storage to
trusted extension contexts, adds
`Authorization: Bearer <token>` only to the configured bot API request. It
continues sending unauthenticated raw events to the optional generic webhook.
Secrets are never committed or displayed outside the password input.

Before sending, the service worker persists events in a local outbox keyed by
Meet event identity. It submits one event at a time, removes it only after a
successful or already-conflicted response, and retries network, rate-limit,
authentication, and server failures with delays of 5 seconds, 30 seconds, 2
minutes, 10 minutes, then 1 hour. A Chrome alarm resumes pending delivery
after service-worker or browser restarts.

## Errors

Validation errors return the existing generic JSON error shape. Queue failures
return `500`. The extension logs generic deferred-delivery warnings without
placing tokens or event bodies in the log. A failed browser request remains in
the local outbox and is not claimed as accepted.

## Testing and Acceptance

Go tests cover authentication, validation, all event formats, fixed
destination, idempotent replay, and CORS preflight. Node tests cover extension
headers, endpoint separation, and missing-configuration behavior. Full Go and
extension gates must pass.

Live acceptance restarts the integrated bot, submits a synthetic Meet event to
the local authenticated endpoint, polls its outbound job to `sent`, and
confirms the API remains healthy. The target remains the configured
“Hello 1,2,3 Bot testing” group.
