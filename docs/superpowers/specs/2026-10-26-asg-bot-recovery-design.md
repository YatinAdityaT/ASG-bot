# ASG Bot Recovery Design

## Goal

Restore ASG Bot as a reliable local Windows process using the latest `whatsmeow`, fresh QR pairing, and the existing command, reminder, and tone-indicator feature set.

## Scope

The recovery includes:

- `/ping`, `/echo`, `/help`, `/remind`, `/rlist`, `/rupdate`, `/rdelete`, and `/die`
- Automatic tone-indicator responses
- SQLite-backed WhatsApp session and reminder storage
- Fresh QR-code authentication
- Explicit allow-listing of WhatsApp groups
- Automated tests, startup documentation, and a live WhatsApp smoke test

The recovery does not include new product features, birthday reminders, chat-history persistence, Docker deployment, or migration to Meta's official WhatsApp Business API.

## Current-State Findings

- The repository is at its latest `master` commit, `7a23c10` from August 4, 2025.
- The pinned `go.mau.fi/whatsmeow` revision is from August 1, 2025 and is 299 commits behind the October 17, 2026 upstream revision.
- Other `whatsmeow` consumers report that old client revisions are rejected by WhatsApp with HTTP 405 "client outdated."
- Current upstream `whatsmeow` requires Go 1.26. The installed Go 1.25.6 toolchain can automatically acquire Go 1.26.
- `go build ./...` succeeds with the old dependency, but `go test ./...` and `go vet ./...` fail on a malformed formatted-log call.
- The repository has no automated tests or operating instructions.
- Command handlers inconsistently consume regex matches, reminder update performs a stray invalid insert, and reminder update/delete operations do not enforce sender ownership.
- The birthday provider would emit the same birthday every scheduler interval for an entire day.
- Message-send, row-scan, and cleanup errors are ignored in several paths.
- Admin number, allowed groups, database path, and logging behavior are hardcoded.

## Chosen Approach

Upgrade to `go.mau.fi/whatsmeow v0.0.0-20261017175727-b2b1e71d45e2` and Go 1.26, adapt the affected APIs, and make targeted reliability fixes in the existing architecture.

An intermediate 2026 dependency pin was rejected because it would remain stale and could be rejected by WhatsApp again. Replacing `whatsmeow` with Meta's official API was rejected for this pass because it would require hosted webhooks, Meta account setup, and a substantial architectural rewrite.

## Architecture

Keep the current Go package structure and SQLite-backed `whatsmeow` device store. Add a small configuration boundary and narrow interfaces around outbound messaging so that command and scheduler behavior can be tested without a live WhatsApp connection.

The process will:

1. Load and validate environment-backed configuration.
2. Open and migrate the SQLite database.
3. Construct the `whatsmeow` client, command manager, tone manager, and scheduler.
4. Pair through a terminal QR code when no session exists.
5. Route messages only from configured group IDs.
6. Stop the scheduler and disconnect the client through a cancellable context on shutdown.

## Configuration

The following values will be supplied through environment variables and validated at startup:

- `ASG_BOT_ADMIN_NUMBERS`: required comma-separated phone numbers without `+`
- `ASG_BOT_ALLOWED_GROUPS`: required comma-separated group JIDs ending in `@g.us`
- `ASG_BOT_DB_PATH`: optional SQLite path, defaulting to `whatsmeow.db`
- `ASG_BOT_LOG_LEVEL`: optional `whatsmeow` log level, defaulting to `INFO`

Whitespace around comma-separated values will be ignored, empty entries will be rejected, and at least one administrator and allowed group will be required. The README will provide formats, examples, and PowerShell commands. Sensitive or deployment-specific values will not remain embedded in source.

## Command and Message Behavior

The router will pass command capture groups to handlers through one documented convention. This removes the current ambiguity in which some handlers expect the full match at index zero while others expect only arguments.

Supported behavior remains:

- `/ping` replies with `Pong!`.
- `/echo <text>` replies with `<text>`.
- `/help` lists non-admin commands.
- `/remind <description and time>` creates a reminder owned by the sender in the current chat.
- `/rlist` lists that sender's reminders in the current chat.
- `/rupdate <id> <description and time>` updates only that sender's reminder in the current chat.
- `/rdelete <id>` deletes only that sender's reminder in the current chat.
- `/die` shuts down the bot only for a configured administrator.
- Known tone indicators trigger one explanatory reply.

Unknown commands remain ignored. The bot continues to process messages only in allowed groups and does not persist ordinary group messages.

## Reminder Scheduling

The scheduler will use a ticker tied to the process context. On each tick it will query due reminders, send each reminder, and delete it only after a successful send.

Reminder queries and mutations will enforce both chat and sender ownership. Row-scan, send, and cleanup errors will be returned or logged with enough context to diagnose the affected task.

Birthday scheduling will be removed from the active registry. The existing birthday table may remain for database compatibility, but no birthday task will run in this recovery.

## Error Handling and Shutdown

Startup failures will return clear errors instead of terminating from deep package code. Outbound message operations will surface failures rather than discard them. Database cleanup failures will not be treated as successful delivery.

The root context will be cancellable. Signal handling will cancel background work, disconnect the WhatsApp client, and close the database before process exit. QR-channel completion without a successful connection will be treated as a startup failure.

## Testing

Automated tests will cover:

- Configuration parsing and validation
- Command matching and captured arguments
- Message text extraction
- Time parsing behavior
- Tone-indicator JSON loading and matching
- Reminder creation, listing, ownership-aware update/delete, due-task lookup, and cleanup
- Scheduler success and send-failure behavior through a fake sender

The automated acceptance gates are:

```text
gofmt produces no diff
go vet ./...
go test ./...
go build ./...
```

## Documentation and Live Acceptance

The README will document prerequisites, Go toolchain behavior, environment configuration, fresh QR pairing, database location, supported commands, startup, graceful shutdown, and common failures.

The live Windows acceptance test will:

1. Start with a fresh database.
2. Display and scan the QR code.
3. Connect without HTTP 405 or an outdated-client rejection.
4. Verify `/ping`, `/echo`, and `/help` in the configured test group.
5. Create, list, update, and delete a reminder.
6. Create a short reminder and verify exactly one scheduled delivery.
7. Stop the bot cleanly with Ctrl+C.

## Risks

`whatsmeow` is an unofficial WhatsApp client and can break when WhatsApp changes its protocol. Pinning the tested revision, documenting the upgrade path, and surfacing connection errors reduce operational uncertainty but do not eliminate this external risk. Use may also be subject to WhatsApp account restrictions or terms; the recovery does not change that constraint.
