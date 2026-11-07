# WhatsApp Group Exporter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and run a standalone QR-authenticated utility that exports members of `[Focused] Accountability subscribers` to CSV.

**Architecture:** A single command owns an isolated whatsmeow SQLite session, connects without registering message handlers, finds one exact group name, resolves cached contact names, and writes a deterministic CSV. It does not use or alter the bot's session.

**Tech Stack:** Go 1.26, whatsmeow, modernc SQLite, qrterminal, encoding/csv.

## Global Constraints

- Session database: `.group-exporter/session.db`.
- Output file: `focused-accountability-subscribers.csv`.
- Exact group name: `[Focused] Accountability subscribers`.
- CSV columns: `name`, `phone_number`, `whatsapp_jid`.
- Never send messages or modify group membership.
- Per the user's prior instruction, do not add or run automated tests; verify by building and running the utility.

---

### Task 1: Standalone group exporter

**Files:**
- Create: `tools/group-exporter/main.go`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: `sqlstore.New`, `whatsmeow.NewClient`, `Client.GetJoinedGroups`, `Client.GetGroupInfo`, and `Device.Contacts.GetContact`.
- Produces: executable command `go run ./tools/group-exporter` and CSV `focused-accountability-subscribers.csv`.

- [ ] **Step 1: Ignore private runtime artifacts**

Add these exact entries to `.gitignore` before creating the session or export:

```gitignore
.group-exporter/
focused-accountability-subscribers.csv
```

- [ ] **Step 2: Implement isolated login and group selection**

Create package `main` with constants for the exact group name, session path, and output path. In `run`, create `.group-exporter`, open `file:.group-exporter/session.db?_foreign_keys=on` through `sqlstore.New`, get the first device, and connect a whatsmeow client. If `device.ID` is nil, obtain the QR channel before connecting and render each `code` event through `qrterminal.GenerateHalfBlock`; otherwise connect the saved session directly.

After authentication, call `GetJoinedGroups`, retain only groups whose `Name` exactly equals `[Focused] Accountability subscribers`, reject zero matches, and reject more than one match. Refresh the selected group through `GetGroupInfo`.

- [ ] **Step 3: Implement member extraction and CSV output**

For every `types.GroupParticipant`, prefer `PhoneNumber`, falling back to `JID` only when it is a phone-number JID. Resolve the name from `Device.Contacts.GetContact` in this order: `FullName`, `FirstName`, `PushName`, `BusinessName`, then `GroupParticipant.DisplayName`. Preserve a row with a blank phone number if WhatsApp exposes only an LID, because the `whatsapp_jid` column still records the available identity.

Sort rows case-insensitively by name and then by phone number. Create `focused-accountability-subscribers.csv`, write the header `name,phone_number,whatsapp_jid` and all rows, flush, check the writer error, and close the file. Print the group name, exported row count, and absolute output path.

- [ ] **Step 4: Build the utility**

Run:

```powershell
go build -o .group-exporter/group-exporter.exe ./tools/group-exporter
```

Expected: exit code 0 and no compiler errors.

- [ ] **Step 5: Commit the utility**

```powershell
git add -- .gitignore tools/group-exporter/main.go
git commit -m "feat: add WhatsApp group member exporter"
```

Expected: one commit containing only the exporter implementation.

### Task 2: Interactive export

**Files:**
- Runtime create: `.group-exporter/session.db`
- Runtime create: `focused-accountability-subscribers.csv`

**Interfaces:**
- Consumes: the command from Task 1 and a QR scan from the user.
- Produces: a populated CSV for the exact target group.

- [ ] **Step 1: Run the exporter**

Run:

```powershell
go run ./tools/group-exporter
```

Expected: a terminal QR appears for a new session. After the user scans it, the command reports the selected group, member count, and CSV path.

- [ ] **Step 2: Verify the artifact without exposing its contents**

Confirm that `focused-accountability-subscribers.csv` exists, its first row is exactly `name,phone_number,whatsapp_jid`, and the number of data rows matches the count printed by the exporter. Do not print member data into chat.

