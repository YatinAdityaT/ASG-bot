# Message Queue API Task 4 Report

## Status

Automated scope is complete. The existing bot process and production
`whatsmeow.db` were not stopped, restarted, opened, or modified.

## Implemented

- Integrated the outbound store, stale `sending` recovery, authenticated API,
  and outbound provider into startup.
- Preserved reminder-only behavior when the API is disabled.
- Enforced startup and shutdown ordering: recover before scheduler, API only
  after WhatsApp authentication, API wait before scheduler wait, then client
  disconnect and database close.
- Made API bind/start failure return an error and clean up without starting the
  scheduler.
- Restricted `ASG_BOT_API_ENABLED` to case-insensitive literal `true` or
  `false`; aliases such as `1`, `0`, `t`, and `f` are rejected.
- Added periodic stale-entry eviction to the per-address rate limiter with an
  injected-clock regression test.
- Documented desktop, Termux, Chrome extension, Cloudflare Tunnel, security,
  retry, status, submission, shutdown, and live-acceptance operation.

## TDD and self-review

Regression tests were observed failing before implementation for strict boolean
parsing, stale limiter eviction, and lifecycle integration. Focused suites and
the complete suite pass after implementation. Self-review found no unresolved
automated-scope defect.

## Verification

All required gates exited successfully:

```text
gofmt -d .
go vet ./...
go test ./... -count=1
go build ./...
git diff --check
```

The full test run passed all nine packages. A supplemental `go test -race` run
was unavailable because this pure-Go environment has CGO disabled; race mode is
not one of the required gates.

## Deferred live acceptance

Live acceptance is intentionally deferred to the parent after review. It must
use a controlled maintenance window: stop the existing process cleanly, reuse
the production database and session, enable the loopback API with a newly
generated 32-or-more-character token and no browser origins initially, then
verify authentication, reminder migration/operation, one immediate job, one
short scheduled job, exactly-once observation, final `sent` statuses, invalid
token rejection, and absence of SQLite lock errors.

## Concerns

- Delivery remains documented best-effort at-least-once; the stable WhatsApp
  message ID reduces but cannot eliminate crash-window duplicates.
- Android may stop Termux background processes despite wake-lock and battery
  configuration, so deployment monitoring remains necessary.

## Commit

Requested commit message: `docs: add message queue API operations`.
