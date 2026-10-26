# Message Queue API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an authenticated HTTP API that persists immediate or scheduled text jobs and sends them to one configured WhatsApp group.

**Architecture:** Add an `outbound` package for queue persistence and state transitions, a `messageapi` package for authenticated HTTP handling, and an outbound task provider in the existing scheduler. Integrate both into the current cancellable startup lifecycle after WhatsApp authentication.

**Tech Stack:** Go 1.26 standard `net/http`, SQLite, whatsmeow, existing scheduler

## Global Constraints

- API destination is server-configured; requests cannot supply a JID.
- Initial target is `120363418911552290@g.us`.
- API binds to localhost and requires a bearer token of at least 32 characters.
- API is disabled by default.
- Immediate and RFC3339 scheduled jobs are persistent.
- Idempotency replay returns the existing job; mismatched reuse returns conflict.
- Stable WhatsApp message IDs are reused across retries.
- Five attempts use 5s, 30s, 2m, 10m, and 1h backoff.
- Request bodies/tokens are never logged.
- Existing reminders, QR pairing, SQLite serialization, and graceful shutdown remain intact.

---

### Task 1: API configuration and outbound queue

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`
- Modify: `db/db.go`
- Modify: `db/db_test.go`
- Create: `outbound/job.go`
- Create: `outbound/store.go`
- Create: `outbound/store_test.go`

**Interfaces:**
- Produce: API fields on `config.Config`
- Produce: `outbound.Job`, `outbound.CreateRequest`, `outbound.Store`
- Produce: `Create`, `Get`, `Due`, `Claim`, `MarkSent`, `MarkFailed`, and `RecoverSending`

- [ ] **Step 1: Write failing config tests**

Cover disabled defaults, valid enabled config, short/missing token, malformed address/target/origins, and target absent from allowed groups.

- [ ] **Step 2: Write failing migration/store tests**

Cover idempotent schema creation, creation and lookup, same-key replay, conflicting key reuse, fixed chat ID, stable 20-character uppercase hexadecimal message ID, due queries, atomic claims, sent transitions, retry transitions, maximum attempts, and recovery of `sending` rows.

- [ ] **Step 3: Confirm RED**

Run: `go test ./config ./db ./outbound`

- [ ] **Step 4: Implement configuration**

Add:

```go
APIEnabled        bool
APIAddr           string
APIToken          string
APITargetGroup    string
APIAllowedOrigins []string
```

Validate enabled-only requirements exactly as specified in the design.

- [ ] **Step 5: Implement schema and store**

Create `outbound_jobs` and due/status indexes in `db.SetupDB`. Keep one open/idle connection. Use transactions for idempotency and claims.

- [ ] **Step 6: Verify and commit**

Run focused and full tests, vet, build, formatting, and diff checks.

Commit: `feat: add persistent outbound queue`

### Task 2: Authenticated HTTP API

**Files:**
- Create: `messageapi/server.go`
- Create: `messageapi/auth.go`
- Create: `messageapi/rate.go`
- Create: `messageapi/server_test.go`

**Interfaces:**
- Consume: `outbound.Store`
- Produce: `messageapi.New(config, store, now) *Server`
- Produce: `(*Server).Handler() http.Handler`
- Produce: `(*Server).Start(context.Context) <-chan error`

- [ ] **Step 1: Write failing handler tests**

Cover:

- Missing/wrong/correct bearer token
- Constant generic unauthorized response
- Allowed, denied, and absent origins
- Preflight
- POST content type and 16 KiB limit
- Empty/oversized text
- Missing/oversized idempotency key
- Invalid, past, and over-one-year `sendAt`
- Immediate submission
- Idempotent replay and conflict
- GET found/not-found
- Health authentication
- Unsupported methods
- 30/minute rate limit
- Destination never accepted from JSON

- [ ] **Step 2: Confirm RED**

Run: `go test ./messageapi`

- [ ] **Step 3: Implement middleware and handlers**

Use `http.MaxBytesReader`, `json.Decoder.DisallowUnknownFields`, `subtle.ConstantTimeCompare`, exact CORS origins, JSON error envelopes, and an in-memory fixed-window limiter keyed by remote address.

- [ ] **Step 4: Implement server lifecycle**

Bind configured localhost address, serve in a goroutine, call `Shutdown` on context cancellation, and return completion/error through one channel.

- [ ] **Step 5: Verify and commit**

Run focused/full tests and all gates.

Commit: `feat: add authenticated message API`

### Task 3: WhatsApp outbound worker

**Files:**
- Create: `scheduler/outbound.go`
- Create: `scheduler/outbound_test.go`
- Modify: `scheduler/registry.go`
- Modify: `scheduler/registry_test.go` if present

**Interfaces:**
- Consume: `outbound.Store`
- Produce: `OutboundProvider` and `OutboundTask`
- Preserve: existing reminder task behavior

- [ ] **Step 1: Write failing worker tests**

Verify due jobs register alongside reminders, atomically claim, send exact conversation text to the stored chat, pass stable `SendRequestExtra.ID`, mark sent once, apply each backoff, mark failed after five attempts, retain errors, and recover stale sending rows.

- [ ] **Step 2: Confirm RED**

Run: `go test ./scheduler`

- [ ] **Step 3: Implement provider/task**

Add the provider only when an outbound store is supplied. Keep reminder-only registry construction available to existing tests or update constructors explicitly.

- [ ] **Step 4: Verify and commit**

Run focused/full tests and all gates.

Commit: `feat: deliver queued WhatsApp messages`

### Task 4: Startup integration, documentation, and acceptance

**Files:**
- Modify: `main.go`
- Modify: `main_test.go`
- Modify: `README.md`

**Interfaces:**
- Consume: API config, outbound store, API server, outbound scheduler provider
- Preserve: lifecycle cleanup ordering

- [ ] **Step 1: Write failing lifecycle tests**

Verify:

- Disabled API does not start.
- Enabled API starts only after WhatsApp authentication.
- API shutdown completes before scheduler/client/database cleanup.
- API startup failure returns an error and cleans up.
- Outbound stale jobs recover before scheduler starts.

- [ ] **Step 2: Integrate startup**

Construct the store after DB setup. Recover stale jobs. After WhatsApp connects, create registry with reminders and outbound jobs, then start API and scheduler. On cancellation, stop API, wait for it, then wait for scheduler before deferred client/database cleanup.

- [ ] **Step 3: Document desktop and Termux operation**

Document all API variables, curl submission/status examples, Cloudflare Tunnel boundary, Chrome extension origin, Termux wake lock/boot/battery guidance, retry semantics, and security warnings.

- [ ] **Step 4: Run automated gates**

```text
gofmt -d .
go vet ./...
go test ./...
go build ./...
git diff --check
```

- [ ] **Step 5: Commit**

Commit: `docs: add message queue API operations`

- [ ] **Step 6: Restart and live-test**

Stop the old process. Start with API enabled, a generated 32+ character token, localhost binding, the test-group target, and no browser origins initially. Verify:

- Existing WhatsApp session authenticates.
- Mentioned reminders migrate and work.
- Immediate API job appears exactly once.
- Short scheduled API job appears exactly once.
- Status changes to `sent`.
- Invalid token is rejected.
- No SQLite lock errors occur.
