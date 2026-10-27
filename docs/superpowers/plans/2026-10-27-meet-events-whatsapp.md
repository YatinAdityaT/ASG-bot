# Meet Events to WhatsApp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert authenticated Meet Monitor events into persistent WhatsApp jobs for the one configured test group.

**Architecture:** Add a focused Meet-event parser/formatter beside the existing HTTP server, then enqueue formatted text through `outbound.Store`. Configure the extension with a separate bot API URL and locally stored bearer token while retaining its generic webhook.

**Tech Stack:** Go `net/http`, SQLite outbound queue, Chrome Manifest V3 JavaScript, Node test runner.

## Global Constraints

- The destination is always `ASG_BOT_API_TARGET_GROUP`; request bodies never contain a JID.
- All routes use the existing bearer authentication, exact-origin CORS, rate limit, and JSON error shape.
- The API token is stored in `chrome.storage.local` and never committed or logged.
- Supported events are `join`, `leave`, `cam_on`, `cam_off`, `mic_muted`, `mic_unmuted`, `rename`, and `chat_message`.
- Tests are written and observed failing before production changes.

---

### Task 1: Meet event validation, formatting, and queueing

**Files:**
- Create: `messageapi/meet.go`
- Create: `messageapi/meet_test.go`
- Modify: `messageapi/server.go`
- Modify: `messageapi/server_test.go`

**Interfaces:**
- Consumes: `(*outbound.Store).Create(context.Context, outbound.CreateRequest)`
- Produces: `POST /v1/meet-events` and `formatMeetEvent(meetEvent) (string, error)`

- [ ] **Step 1: Write failing handler tests**

Add table-driven tests that submit each supported event, assert `202`, fetch
the stored job, and compare the exact formatted message. Add rejection tests
for unknown fields/types, blank required fields, malformed timestamps,
invalid text rules, oversized strings, wrong content type, and bodies over
16 KiB. Add a replay test asserting the same event returns the same job.

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./messageapi -run Meet -count=1`

Expected: failures because `/v1/meet-events` returns `404`.

- [ ] **Step 3: Implement the endpoint**

Define the exact JSON struct and limits from the design in `meet.go`. Decode
with `http.MaxBytesReader`, `json.Decoder.DisallowUnknownFields`, and
`requireJSONEnd`. Format each event with a closed switch. Derive
`meet-` plus a SHA-256 hex digest of the normalized struct, then call:

```go
server.store.Create(request.Context(), outbound.CreateRequest{
    IdempotencyKey: key,
    Text:           formatted,
})
```

Route only `POST /v1/meet-events` from `serveHTTP` and return the existing
`submitResponse` with `202 Accepted`.

- [ ] **Step 4: Verify the endpoint**

Run: `go test ./messageapi -run Meet -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add messageapi
git commit -m "feat: ingest Google Meet events"
```

### Task 2: Extension bot API configuration and forwarding

**Files:**
- Create: `meet-monitor/src/delivery.js`
- Create: `meet-monitor/test/delivery.test.js`
- Modify: `meet-monitor/src/background.js`
- Modify: `meet-monitor/src/options.html`
- Modify: `meet-monitor/src/options.js`
- Modify: `meet-monitor/manifest.json`

**Interfaces:**
- Consumes: raw event records from `content.js`
- Produces: `MMDelivery.requestFor(event, apiURL, token)` returning fetch URL/options or `null`

- [ ] **Step 1: Write failing delivery tests**

Test that complete configuration yields a JSON `POST` with
`Content-Type: application/json` and `Authorization: Bearer <token>`, while a
missing URL or token returns `null`. Assert the URL is unchanged and the event
body is serialized exactly.

- [ ] **Step 2: Verify the tests fail**

Run: `node --test meet-monitor/test/delivery.test.js`

Expected: module-not-found failure for `src/delivery.js`.

- [ ] **Step 3: Implement extension delivery**

Create a browser/Node-compatible `MMDelivery` helper. Load it before
`background.js` in the service worker using `importScripts`. Read `botAPIURL`
from sync storage and `botAPIToken` from local storage, then send authenticated
events to the bot API. Restrict local storage to trusted extension contexts.
Persist configured events in a local outbox, submit them sequentially, remove
them only after acceptance, and use Chrome alarms plus 30-second, 1-minute,
2-minute, 10-minute, and 1-hour retry delays. Cap the queue and attempt count,
discard non-retryable client errors, and drain bounded batches with rate-safe
spacing. Keep `forwardWebhook` unchanged and independent.

Add URL and password inputs to options. Validate HTTP(S), request exact origin
permission, require HTTPS except for loopback HTTP, save the URL in sync
storage and token in local storage, and never render the saved token back into
page text.

- [ ] **Step 4: Verify extension behavior**

Run:

```powershell
node --test meet-monitor/test/*.test.js
node --check meet-monitor/src/delivery.js
node --check meet-monitor/src/background.js
node --check meet-monitor/src/options.js
```

Expected: all tests and syntax checks pass.

- [ ] **Step 5: Commit**

```powershell
git add meet-monitor
git commit -m "feat: forward Meet events to bot API"
```

### Task 3: Documentation, live acceptance, and handoff

**Files:**
- Modify: `README.md`
- Modify: `meet-monitor/README.md`

**Interfaces:**
- Consumes: deployed `/v1/meet-events` route and extension options
- Produces: installation/configuration and acceptance instructions

- [ ] **Step 1: Document setup**

Document the endpoint contract, exact message formats, extension ID origin
configuration, Bot API URL/token fields, token storage behavior, Chrome reload
steps, and Cloudflare HTTPS requirement for remote browsers.

- [ ] **Step 2: Run all automated gates**

Run:

```powershell
gofmt -d .
go vet ./...
go test ./...
go build ./...
git diff --check
node -e "JSON.parse(require('fs').readFileSync('meet-monitor/manifest.json','utf8'))"
node --check meet-monitor/src/selectors.js
node --check meet-monitor/src/state.js
node --check meet-monitor/src/content.js
node --check meet-monitor/src/delivery.js
node --check meet-monitor/src/background.js
node --check meet-monitor/src/csv.js
node --check meet-monitor/src/popup.js
node --check meet-monitor/src/options.js
node --test meet-monitor/test/*.test.js
```

Expected: all commands exit zero; Go and Node test suites report no failures.

- [ ] **Step 3: Perform live acceptance**

Restart the integrated service with the existing private runtime
configuration. Submit a synthetic `join` event to `/v1/meet-events`, retain
the returned job ID, poll `/v1/messages/{id}`, and require status `sent`.
Check `/healthz` and runtime output for SQLite, panic, or delivery errors.

- [ ] **Step 4: Commit and push**

```powershell
git add README.md meet-monitor/README.md
git commit -m "docs: operate Meet event forwarding"
git push
```

Verify the existing pull request head matches the local branch and remains
open against `master`.
