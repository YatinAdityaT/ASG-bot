# ASG Bot Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore ASG Bot as a tested local Windows application that pairs by QR and works with the current WhatsApp protocol.

**Architecture:** Keep the existing Go packages and SQLite store, add environment-backed configuration, and place a narrow outbound-message interface between business logic and `whatsmeow`. Route command capture groups consistently, enforce reminder ownership in SQL, and run due tasks through a cancellable ticker.

**Tech Stack:** Go 1.26, `go.mau.fi/whatsmeow v0.0.0-20261017175727-b2b1e71d45e2`, SQLite, Go standard-library tests

## Global Constraints

- Run locally on Windows with fresh QR pairing.
- Preserve `/ping`, `/echo`, `/help`, `/remind`, `/rlist`, `/rupdate`, `/rdelete`, `/die`, and tone responses.
- Do not store ordinary group messages.
- Process messages only from `ASG_BOT_ALLOWED_GROUPS`.
- Require `ASG_BOT_ADMIN_NUMBERS` and `ASG_BOT_ALLOWED_GROUPS`.
- Default `ASG_BOT_DB_PATH` to `whatsmeow.db` and `ASG_BOT_LOG_LEVEL` to `INFO`.
- Disable birthday scheduling.
- Pin `go.mau.fi/whatsmeow v0.0.0-20261017175727-b2b1e71d45e2`.

---

### Task 1: Configuration and dependency baseline

**Files:**
- Create: `config/config.go`
- Create: `config/config_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `config.Config`, `config.Load(func(string) string) (Config, error)`

- [ ] **Step 1: Write failing configuration tests**

Cover required-value failures, trimming comma-separated values, phone-number validation, group-JID validation, defaults, and accepted log levels.

```go
func TestLoadValid(t *testing.T) {
    env := map[string]string{
        "ASG_BOT_ADMIN_NUMBERS": "111, 222",
        "ASG_BOT_ALLOWED_GROUPS": "one@g.us, two@g.us",
    }
    cfg, err := Load(func(key string) string { return env[key] })
    if err != nil {
        t.Fatal(err)
    }
    if diff := cmp.Diff([]string{"111", "222"}, cfg.AdminNumbers); diff != "" {
        t.Fatal(diff)
    }
    if cfg.DBPath != "whatsmeow.db" || cfg.LogLevel != "INFO" {
        t.Fatalf("unexpected defaults: %#v", cfg)
    }
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./config`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement configuration parsing**

```go
type Config struct {
    AdminNumbers []string
    AllowedGroups []string
    DBPath string
    LogLevel string
}

func Load(getenv func(string) string) (Config, error)
```

Use `strings.Split`, trim every value, reject empty entries, validate admins with `^[0-9]+$`, validate group IDs with `^[0-9]+@g\\.us$`, and accept `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, or `DISABLED`.

- [ ] **Step 4: Upgrade the Go and `whatsmeow` dependency baseline**

Run:

```text
go mod edit -go=1.26.0
go get go.mau.fi/whatsmeow@v0.0.0-20261017175727-b2b1e71d45e2
go mod tidy
```

- [ ] **Step 5: Verify configuration tests**

Run: `go test ./config`

Expected: PASS.

- [ ] **Step 6: Commit**

```text
git add config go.mod go.sum
git commit -m "build: update whatsmeow and add runtime config"
```

### Task 2: Testable messaging and consistent command routing

**Files:**
- Modify: `utils/messaging.go`
- Modify: `cmd/manager.go`
- Modify: `cmd/router.go`
- Modify: `cmd/adapters.go`
- Modify: `cmd/handlers.go`
- Create: `cmd/router_test.go`
- Create: `cmd/handlers_test.go`
- Create: `utils/messaging_test.go`

**Interfaces:**
- Produces: `utils.MessageSender`
- Produces: `utils.ReplyToMessage(...) error`
- Produces: `(*cmd.CommandManager).RouteMessage(*sql.DB, context.Context, *events.Message)`
- Consumes: configuration admin numbers

- [ ] **Step 1: Write failing message and router tests**

Use a fake sender that records `waE2E.Message` values. Verify `/echo hello` replies with exactly `hello`, `/ping` replies with `Pong!`, non-command text is ignored, and send failures are returned by `ReplyToMessage`.

```go
type fakeSender struct {
    messages []*waProto.Message
    err error
}

func (f *fakeSender) SendMessage(
    _ context.Context,
    _ types.JID,
    msg *waProto.Message,
    _ ...whatsmeow.SendRequestExtra,
) (whatsmeow.SendResponse, error) {
    f.messages = append(f.messages, msg)
    return whatsmeow.SendResponse{}, f.err
}
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `go test ./cmd ./utils`

Expected: FAIL because the fake cannot replace the concrete client and reply errors are discarded.

- [ ] **Step 3: Add the messaging interface and return errors**

```go
type MessageSender interface {
    SendMessage(
        context.Context,
        types.JID,
        *waProto.Message,
        ...whatsmeow.SendRequestExtra,
    ) (whatsmeow.SendResponse, error)
}
```

Change `ReplyToMessage` to return the `SendMessage` error. Change command callbacks and the manager to consume `MessageSender`.

- [ ] **Step 4: Normalize regex captures**

In `RouteMessage`, pass `matches[1:]` to every callback. Update handlers so `/echo` and `/remind` consume `args[0]`, delete consumes `args[0]`, and update consumes `args[0]` and `args[1]`.

- [ ] **Step 5: Replace `/die` process exit with injected shutdown**

Add `shutdown context.CancelFunc` to `CommandManager`; after a successful goodbye reply, invoke it. This allows normal cleanup and testing.

- [ ] **Step 6: Verify focused tests**

Run: `go test ./cmd ./utils`

Expected: PASS.

- [ ] **Step 7: Commit**

```text
git add cmd utils
git commit -m "fix: normalize command routing and message errors"
```

### Task 3: Reminder correctness and ownership

**Files:**
- Modify: `db/db.go`
- Modify: `cmd/handlers.go`
- Create: `db/db_test.go`
- Extend: `cmd/handlers_test.go`
- Modify: `utils/time.go`
- Create: `utils/time_test.go`

**Interfaces:**
- Produces: `db.SetupDB(context.Context, string) (*sqlstore.Container, *sql.DB, error)`
- Consumes: normalized command arguments and `utils.MessageSender`

- [ ] **Step 1: Write failing database and reminder tests**

Use `t.TempDir()` databases. Verify schema setup, create/list, sender-owned update/delete, rejection of another sender's update/delete, and parsing invalid reminder times.

```go
func openTestDB(t *testing.T) *sql.DB {
    t.Helper()
    _, conn, err := db.SetupDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { conn.Close() })
    return conn
}
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `go test ./db ./cmd ./utils`

Expected: FAIL on the old fatal-exit setup API, reminder indexes, stray insert, or ownership behavior.

- [ ] **Step 3: Return setup errors instead of exiting**

`SetupDB` will accept a context and path, open `file:<path>?_foreign_keys=on`, run the `whatsmeow` migration and custom schema, and return errors with `%w`.

- [ ] **Step 4: Correct reminder handlers**

Remove the empty `INSERT` from update. Include `to_id = ?` in update and delete filters. Check row scan, rows iteration, SQL execution, and reply errors.

```sql
UPDATE reminders
SET message = ?, remind_at = ?
WHERE id = ? AND chat_id = ? AND to_id = ?
```

```sql
DELETE FROM reminders
WHERE id = ? AND chat_id = ? AND to_id = ?
```

- [ ] **Step 5: Verify focused tests**

Run: `go test ./db ./cmd ./utils`

Expected: PASS.

- [ ] **Step 6: Commit**

```text
git add db cmd/handlers.go cmd/handlers_test.go utils/time.go utils/time_test.go
git commit -m "fix: enforce reminder ownership and database errors"
```

### Task 4: Reliable scheduler

**Files:**
- Modify: `scheduler/task.go`
- Modify: `scheduler/handlers.go`
- Modify: `scheduler/registry.go`
- Modify: `scheduler/scheduler.go`
- Create: `scheduler/scheduler_test.go`
- Create: `scheduler/handlers_test.go`

**Interfaces:**
- Produces: `(*Scheduler).RunOnce(context.Context, time.Time) error`
- Consumes: `utils.MessageSender`

- [ ] **Step 1: Write failing scheduler tests**

Verify a due reminder sends once and is deleted, a failed send retains the row, cleanup failures surface, and `NewRegistry` does not register the birthday provider.

```go
func TestReminderRetainedWhenSendFails(t *testing.T) {
    sender := &fakeSender{err: errors.New("send failed")}
    task := dueReminderTask(t)
    if err := task.Run(context.Background(), sender); err == nil {
        t.Fatal("expected send failure")
    }
    assertReminderExists(t, task.db, task.id)
}
```

- [ ] **Step 2: Run scheduler tests and confirm failure**

Run: `go test ./scheduler`

Expected: FAIL because tasks require a concrete client, cleanup errors are ignored, and birthdays remain active.

- [ ] **Step 3: Implement one-pass scheduling and cancellation**

Use `utils.MessageSender`, make `CleanUp` return an error, add `RunOnce`, and use `time.NewTicker` with `defer ticker.Stop()` in `Start`.

- [ ] **Step 4: Disable birthday scheduling**

Keep compatibility types if useful, but register only `ReminderProvider` in `NewRegistry`.

- [ ] **Step 5: Verify scheduler tests**

Run: `go test ./scheduler`

Expected: PASS with no vet warning.

- [ ] **Step 6: Commit**

```text
git add scheduler
git commit -m "fix: make reminder scheduling reliable"
```

### Task 5: Startup, QR pairing, and graceful shutdown

**Files:**
- Modify: `main.go`
- Create: `main_test.go`
- Modify: `tone/manager.go`
- Create: `tone/loader_test.go`

**Interfaces:**
- Consumes: `config.Load`, `db.SetupDB`, command manager, tone manager, scheduler
- Produces: `run(context.Context, config.Config) error`

- [ ] **Step 1: Write failing allow-list and tone loader tests**

Verify only configured groups are routed and valid tone JSON loads expected entries while malformed JSON and invalid regex return errors.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./...`

Expected: FAIL until startup uses configuration and updated interfaces.

- [ ] **Step 3: Rebuild startup around returned errors**

Load configuration from `os.Getenv`, use `signal.NotifyContext`, defer database close and client disconnect, and construct an allowed-group set. Start the scheduler only after a successful connection.

- [ ] **Step 4: Preserve fresh QR pairing with explicit failure handling**

Display QR events, connect in a goroutine, honor context cancellation, and return an error if the QR channel closes without a stored device or connected client.

- [ ] **Step 5: Verify all tests and build**

Run:

```text
gofmt -w .
go vet ./...
go test ./...
go build ./...
```

Expected: all commands exit zero.

- [ ] **Step 6: Commit**

```text
git add main.go main_test.go tone
git commit -m "feat: add configured startup and graceful shutdown"
```

### Task 6: Operating documentation and final acceptance

**Files:**
- Modify: `README.md`

**Interfaces:**
- Documents all user-facing setup and supported commands.

- [ ] **Step 1: Replace the design-note README with operating documentation**

Document Go 1.26, a C compiler requirement for `go-sqlite3`, exact environment variables, PowerShell examples, `go run .`, QR pairing, database persistence, commands, group allow-listing, Ctrl+C shutdown, tests, and HTTP 405 troubleshooting.

- [ ] **Step 2: Run final static and automated verification**

Run:

```text
gofmt -d .
go vet ./...
go test ./...
go build ./...
git diff --check
git status --short
```

Expected: formatting diff empty; vet, tests, build, and diff check exit zero.

- [ ] **Step 3: Commit documentation**

```text
git add README.md
git commit -m "docs: add local setup and recovery guide"
```

- [ ] **Step 4: Perform the live WhatsApp acceptance test**

Set the required environment variables, use a fresh temporary database path, run `go run .`, scan the QR code, test all documented command flows in the configured test group, confirm one reminder delivery, and stop with Ctrl+C.

- [ ] **Step 5: Record final repository state**

Run:

```text
git status --short --branch
git log --oneline --decorate -8
```

Expected: clean `revive-asg-bot` branch with recovery commits above `e7c5660`.
