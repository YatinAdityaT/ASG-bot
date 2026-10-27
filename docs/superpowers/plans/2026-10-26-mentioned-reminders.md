# Mentioned Reminders Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support flexible self and mentioned-recipient reminder commands with requester attribution and safe ownership.

**Architecture:** Add a pure reminder parser that combines preserved command text, `when.Result` match metadata, and trusted WhatsApp mention JIDs. Extend the reminder schema with `requested_by`, pass a focused command context into handlers, and keep the scheduler's existing `to_id` delivery target.

**Tech Stack:** Go 1.26, whatsmeow message metadata, `github.com/olebedev/when`, modernc SQLite

## Global Constraints

- Accept all five syntax examples from the approved design.
- Use actual WhatsApp mention metadata and support at most one recipient.
- Preserve reminder capitalization.
- No mention means a self-reminder.
- Delivery must mention the recipient and attribute the requester.
- Requester owns list/update/delete; update preserves recipient.
- Existing reminders migrate with `requested_by = to_id`.
- Do not add an LLM or external message-processing service.
- Keep the bot restricted to `120363418911552290@g.us` during live testing.

---

### Task 1: Reminder parsing and preserved command text

**Files:**
- Create: `utils/reminder.go`
- Create: `utils/reminder_test.go`
- Modify: `utils/messaging.go`
- Modify: `utils/messaging_test.go`
- Modify: `cmd/manager.go`
- Modify: `cmd/router.go`
- Modify: `cmd/adapters.go`
- Modify: `cmd/router_test.go`

**Interfaces:**
- Produce: `utils.ExtractText(*events.Message) string`
- Produce: `utils.ReminderRequest`
- Produce: `utils.ParseReminder(string, []types.JID, types.JID, string, time.Time) (ReminderRequest, error)`
- Produce: `cmd.CommandContext` containing `types.MessageInfo` and trusted mentions

- [ ] **Step 1: Write failing parser tests**

Test all approved syntaxes, a fixed reference time, capitalization, quoted/unquoted bodies, filler removal, self target, mentioned target, fake visible mention, multiple mentions, malformed quotes, missing body, and missing time.

```go
type ReminderRequest struct {
    Target        types.JID
    Requester     types.JID
    RequesterName string
    Body          string
    RemindAt      time.Time
}
```

- [ ] **Step 2: Confirm RED**

Run: `go test ./utils ./cmd`

Expected: FAIL because preserved extraction, parser, and command context do not exist.

- [ ] **Step 3: Implement preserved extraction and pure parser**

Use `when.Parse` result `Index`, `Text`, and `Time` to remove only the detected time phrase. Prefer one balanced quoted segment for the body. Validate trusted mentions and normalize JIDs with `ToNonAD`.

- [ ] **Step 4: Route raw command captures**

Match command names case-insensitively against preserved text. Pass `CommandContext{Info, Mentions}` into callbacks. Keep tone extraction lowercase through `ExtractCleanText`.

- [ ] **Step 5: Verify focused tests and commit**

Run:

```text
go test ./utils ./cmd
go test ./...
```

Commit: `feat: parse flexible reminder commands`

### Task 2: Schema migration and requester ownership

**Files:**
- Modify: `db/db.go`
- Modify: `db/db_test.go`
- Modify: `cmd/handlers.go`
- Modify: `cmd/handlers_test.go`

**Interfaces:**
- Consume: `utils.ReminderRequest`
- Preserve: existing `db.SetupDB` connection serialization
- Produce: migrated `reminders.requested_by`

- [ ] **Step 1: Write failing migration tests**

Create a legacy reminders table without `requested_by`, insert a row, run application schema migration, and verify the column exists and is backfilled from `to_id`. Verify new databases include the column.

- [ ] **Step 2: Write failing ownership and delivery tests**

Test another-recipient creation, self creation, requester-owned list/update/delete, recipient denial, cross-chat denial, preserved recipient during update, push-name fallback, and reply/send errors.

- [ ] **Step 3: Confirm RED**

Run: `go test ./db ./cmd`

Expected: FAIL on absent migration and old self-only handler behavior.

- [ ] **Step 4: Implement idempotent migration**

Create `requested_by` in new schema. For legacy schema, inspect `pragma_table_info('reminders')`, add the nullable column only if absent, backfill null values from `to_id`, and index requester/chat lookups.

- [ ] **Step 5: Implement mentioned reminder handlers**

Store:

```text
chat_id = current chat
to_id = delivery recipient
requested_by = requesting sender
message = final attributed delivery text
remind_at = parsed time
```

Filter list/update/delete by `requested_by` and chat. Query `to_id` before update so delivery target is unchanged.

- [ ] **Step 6: Verify and commit**

Run:

```text
go test ./db ./cmd
go test ./...
go vet ./...
```

Commit: `feat: add recipient reminder ownership`

### Task 3: Delivery, help, documentation, and live acceptance

**Files:**
- Modify: `scheduler/handlers_test.go`
- Modify: `cmd/handlers_test.go`
- Modify: `README.md`

**Interfaces:**
- Preserve: scheduler sends stored message and mentions `to_id`
- Document: new reminder forms and ownership behavior

- [ ] **Step 1: Add delivery regression**

Verify a stored mentioned reminder sends the exact attribution text with the recipient in `MentionedJID`, then is deleted once.

- [ ] **Step 2: Update help and README**

Document all supported forms, one-recipient limit, self fallback, ownership, examples, and ambiguity errors.

- [ ] **Step 3: Run all automated gates**

Run:

```text
gofmt -d .
go vet ./...
go test ./...
go build ./...
git diff --check
```

Expected: empty formatting/diff output and zero exits.

- [ ] **Step 4: Commit**

Commit: `docs: document mentioned reminders`

- [ ] **Step 5: Restart live bot**

Stop the running process, then start with:

```powershell
$env:ASG_BOT_ADMIN_NUMBERS='916355442207'
$env:ASG_BOT_ALLOWED_GROUPS='120363418911552290@g.us'
$env:ASG_BOT_DB_PATH='whatsmeow.db'
$env:ASG_BOT_LOG_LEVEL='INFO'
go run .
```

- [ ] **Step 6: Live acceptance**

In “Hello 1,2,3 Bot testing,” verify one self-reminder and one mentioned-recipient reminder, including confirmation, `/rlist`, exact delivery mention/attribution, update/delete ownership, and no SQLite lock errors.
