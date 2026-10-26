# Message Queue API Design

## Goal

Accept authenticated message jobs from a Chrome extension running on any machine, persist them, and send them through the paired WhatsApp bot to one server-configured group.

## Deployment

The first deployment runs the Go service in Android Termux. The API binds only to `127.0.0.1`; Cloudflare Tunnel provides public HTTPS. The phone uses `termux-wake-lock`, disabled battery optimization, and Termux:Boot to improve uptime.

The Chrome extension never receives WhatsApp session data, SQLite access, or a destination JID.

## Configuration

The API is disabled unless `ASG_BOT_API_ENABLED=true`.

When enabled:

- `ASG_BOT_API_ADDR` defaults to `127.0.0.1:8080`.
- `ASG_BOT_API_TOKEN` is required and must contain at least 32 characters.
- `ASG_BOT_API_TARGET_GROUP` is required, must end in `@g.us`, and must also appear in `ASG_BOT_ALLOWED_GROUPS`.
- `ASG_BOT_API_ALLOWED_ORIGINS` is an optional comma-separated list of exact Chrome extension origins. An empty list denies browser cross-origin requests but still permits non-browser clients without an `Origin` header.

The initial target is `120363418911552290@g.us`, “Hello 1,2,3 Bot testing.”

## HTTP API

### Submit

`POST /v1/messages`

Headers:

```text
Authorization: Bearer <ASG_BOT_API_TOKEN>
Content-Type: application/json
```

Body:

```json
{
  "text": "Message to send",
  "sendAt": "2026-10-27T09:30:00+05:30",
  "idempotencyKey": "extension-generated-uuid"
}
```

`text` and `idempotencyKey` are required. `sendAt` is optional and must be RFC 3339; omission means immediate delivery. The body is limited to 16 KiB and message text to 4,096 Unicode code points. Scheduled times must be in the future and no more than one year away.

The destination is never accepted from the request.

Response: `202 Accepted`

```json
{
  "id": "server-job-id",
  "status": "pending",
  "sendAt": "2026-10-27T09:30:00+05:30"
}
```

Reusing an idempotency key with the same payload returns the existing job. Reusing it with a different payload returns `409 Conflict`.

### Status

`GET /v1/messages/{id}` uses the same bearer token and returns:

```json
{
  "id": "server-job-id",
  "status": "pending",
  "attempts": 0,
  "sendAt": "2026-10-27T09:30:00+05:30",
  "sentAt": null,
  "lastError": null
}
```

Statuses are `pending`, `sending`, `sent`, or `failed`.

### Health

`GET /healthz` requires authentication and reports API readiness without exposing account or message details.

## Authentication, CORS, and Abuse Controls

- Bearer-token comparison uses constant-time comparison.
- Authentication failures return a generic `401`.
- Request bodies and tokens are never logged.
- CORS allows only exact configured origins and handles preflight requests.
- Requests without an `Origin` header are allowed for curl, tests, Cloudflare health checks, and server-to-server use.
- The API applies an in-memory limit of 30 requests per minute per remote address.
- Unsupported methods and content types are rejected.

Cloudflare Access is recommended as an additional outer layer but does not replace application authentication.

## Persistence

SQLite adds an `outbound_jobs` table:

- `id`
- `idempotency_key` (unique)
- `chat_id`
- `message`
- `message_id` (stable WhatsApp message ID)
- `send_at`
- `next_attempt_at`
- `status`
- `attempts`
- `last_error`
- `created_at`
- `sent_at`

Schema creation is idempotent and uses the existing serialized database connection.

## Worker

An outbound-job provider joins the existing scheduler registry. It queries pending jobs whose `next_attempt_at` is due.

For each job:

1. Atomically transition `pending` to `sending`.
2. Send a WhatsApp conversation message to the configured group using the stored stable `message_id`.
3. On success, mark `sent` and set `sent_at`.
4. On failure, increment attempts and either return to `pending` with backoff or mark `failed` after five attempts.

Backoff intervals are 5 seconds, 30 seconds, 2 minutes, 10 minutes, and 1 hour.

On startup, stale `sending` rows return to `pending`. Reusing the stable WhatsApp message ID reduces duplicate-delivery risk if the process stops after sending but before marking the row sent. Delivery remains best-effort at-least-once because WhatsApp does not provide transactional coupling with SQLite.

## Lifecycle

The HTTP server starts only after WhatsApp authentication succeeds. Shutdown order is:

1. Stop accepting API requests.
2. Wait for the HTTP server to exit.
3. Cancel and wait for scheduler work.
4. Disconnect WhatsApp.
5. Close SQLite.

Pending jobs survive restart.

## Testing

Tests cover configuration, migrations, authentication, CORS, body limits, validation, idempotency replay/conflict, fixed destination, status lookup, rate limiting, stable message IDs, success/failure/retry transitions, startup recovery, lifecycle shutdown, and full automated gates.

Live acceptance submits an immediate job and a short scheduled job through the API and verifies both appear exactly once in “Hello 1,2,3 Bot testing,” with no SQLite lock errors.
