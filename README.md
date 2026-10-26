# ASG Bot

ASG Bot is a local Windows WhatsApp companion. It responds only in explicitly
allowed groups, provides commands and reminders, and explains recognized tone
indicators. Ordinary group messages are not stored. The SQLite database stores
the linked-device session, WhatsApp client state, and reminders.

## Prerequisites

- Windows PowerShell.
- Go 1.26 (`go version` must report `go1.26.x`).
- A WhatsApp account and phone able to scan a linked-device QR code.

The current build uses the pure-Go `modernc.org/sqlite` driver. A C compiler and
CGO are not required. `github.com/mattn/go-sqlite3` may appear in `go.sum` as a
transitive module, but it is not the database driver imported by this program.

Run all commands below from the repository root. Download dependencies once:

```powershell
go mod download
```

## Configuration

The process reads configuration from environment variables at startup.

| Variable | Required | Format and default |
| --- | --- | --- |
| `ASG_BOT_ADMIN_NUMBERS` | Yes | Comma-separated WhatsApp phone numbers containing digits only, including country code but no `+`, spaces, or punctuation. These users may run `/die`. |
| `ASG_BOT_ALLOWED_GROUPS` | Yes | Comma-separated numeric WhatsApp group JIDs in `<digits>@g.us` form. Messages from every other chat are ignored. Display names are not accepted. |
| `ASG_BOT_DB_PATH` | No | SQLite file path. Defaults to `whatsmeow.db` in the current directory. |
| `ASG_BOT_LOG_LEVEL` | No | One of `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, or `DISABLED`. Defaults to `INFO`; values are case-sensitive. |

Whitespace around comma-separated entries is removed, but empty entries are
invalid. Obtain the numeric group JID from trusted WhatsApp/whatsmeow diagnostic
output before starting the bot; do not substitute the group's display name.

Example for the current PowerShell session:

```powershell
$env:ASG_BOT_ADMIN_NUMBERS = "15551234567,15557654321"
$env:ASG_BOT_ALLOWED_GROUPS = "120363012345678901@g.us"
$env:ASG_BOT_DB_PATH = (Join-Path $PWD "whatsmeow.db")
$env:ASG_BOT_LOG_LEVEL = "INFO"
```

For an isolated acceptance run, use a new temporary database:

```powershell
$env:ASG_BOT_DB_PATH = Join-Path $env:TEMP "asg-bot-$([guid]::NewGuid().ToString('N')).db"
```

Environment variables set this way last only for the current PowerShell
process. Do not commit phone numbers, group JIDs, or database files.

## Start and pair

Start the bot:

```powershell
go run .
```

With a new database, the terminal prints `No existing session found` and a QR
code. On the phone, open WhatsApp, go to **Settings > Linked devices > Link a
device**, and scan the terminal QR code. Keep the terminal wide enough for the
code to render cleanly. Successful startup prints:

```text
WhatsApp client connected successfully!
Bot is running. Press Ctrl+C to exit.
```

The configured database persists both pairing state and reminders. Reusing its
path reconnects the existing session without another QR scan. To force fresh
pairing, stop the bot and select a new database path. Deleting the old database
also deletes its reminders and stored session.

Only exact group JIDs in `ASG_BOT_ALLOWED_GROUPS` are processed. Direct
messages, unlisted groups, and unknown slash commands receive no response.

## Supported behavior

Send these exact commands in an allowed group:

- `/ping` replies `Pong!`.
- `/echo <text>` repeats the supplied text.
- `/help` lists non-admin commands.
- `/remind <request>` creates a reminder from natural-language time and
  message text, for example `/remind me to stretch in 2 minutes` or
  `/remind me to call Mom tomorrow at 3 pm`.
- `/rlist` lists the sender's reminders and their numeric IDs in this group.
- `/rupdate <id> <new request>` updates one owned reminder, for example
  `/rupdate 3 tomorrow at 4 pm call Mom`.
- `/rdelete <id>` deletes one owned reminder.
- `/die` replies and shuts down the bot, but only when sent by a number in
  `ASG_BOT_ADMIN_NUMBERS`.

Reminder ownership includes both sender and group: another user or another
group cannot list, update, or delete a reminder. Due reminders are posted back
to the group and mention their owner.

Messages containing a tone indicator defined in `tone/tone_commands.json`
(for example, `That was easy /s`) receive an automatic explanation. This
behavior, like commands, is limited to allowed groups.

## Stop

Press **Ctrl+C** in the running PowerShell window. The bot cancels its scheduler,
disconnects WhatsApp, closes SQLite, and prints `Shutting down bot...`. The
admin-only `/die` command performs the same application shutdown after its
reply is sent.

## Verify

Run the automated gates from the repository root:

```powershell
gofmt -d .
go vet ./...
go test ./...
go build ./...
git diff --check
git status --short
```

`gofmt -d .` should print nothing. The vet, test, build, and diff-check commands
should exit successfully. `git status --short` is informational and should show
only changes you intentionally made.

## Live acceptance

Automated tests do not authenticate with WhatsApp. Complete this check manually:

1. Set valid admin numbers and the test group's numeric JID.
2. Set `ASG_BOT_DB_PATH` to a new temporary path as shown above.
3. Run `go run .`, scan the QR code, and confirm the connected/running messages.
4. In the allowed test group, verify `/ping`, `/echo acceptance`, and `/help`.
5. Create a reminder due in a few minutes, use `/rlist` to get its ID, update
   it with `/rupdate`, and confirm the updated reminder is delivered.
6. Create another reminder and delete it with `/rdelete`; confirm it disappears
   from `/rlist`.
7. Send a tone indicator such as `Acceptance test /s` and confirm the automatic
   explanation.
8. Confirm the same commands are ignored in an unlisted group.
9. Press Ctrl+C and confirm clean shutdown.

## Troubleshooting

### HTTP 405 or “client outdated”

WhatsApp rejects stale client protocols with HTTP 405. Confirm the selected
whatsmeow version and update it before rebuilding:

```powershell
go list -m go.mau.fi/whatsmeow
go get go.mau.fi/whatsmeow@latest
go mod tidy
go test ./...
go build ./...
```

Review and commit the resulting `go.mod` and `go.sum` changes separately. If
the error remains, use a new temporary database to force QR pairing. Do not
delete the normal database unless losing its stored session and reminders is
acceptable.

### Configuration error

Startup reports the exact invalid variable. Admin entries must be digits only;
group entries must be numeric JIDs ending in `@g.us`; the log level must use one
of the uppercase values above. Set both required variables before retrying.

### QR code cannot be scanned

Enlarge the terminal, use a standard dark-on-light terminal theme, and retry
with a fresh database path. Ensure the phone has network access and that the
terminal stays open while pairing.

### Database cannot be opened

Use an absolute path in a writable directory, ensure its parent directory
exists, and make sure another process is not holding the database. Preserve the
same path across normal restarts if pairing and reminders must persist.