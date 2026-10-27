# ASG Bot

ASG Bot is a local WhatsApp companion for Windows or Android Termux. It responds
only in explicitly allowed groups, provides commands and reminders, explains
recognized tone indicators, and can optionally accept queued messages through
an authenticated HTTP API. Ordinary group messages are not stored. The SQLite
database stores the linked-device session, WhatsApp client state, reminders,
and outbound API jobs.

## Meet Monitor browser extension

The companion Google Meet extension is in [`meet-monitor/`](meet-monitor/).
See [`meet-monitor/README.md`](meet-monitor/README.md) for Chrome installation,
webhook configuration, permissions, updates, and troubleshooting.

## Prerequisites

- Windows PowerShell, or Termux on Android.
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
| `ASG_BOT_API_ENABLED` | No | Case-insensitive literal `true` or `false`; no other aliases are accepted. Defaults to `false`. |
| `ASG_BOT_API_ADDR` | No | Listen address used when the API is enabled. Defaults to `127.0.0.1:8080`. Keep the loopback host when using a local tunnel. |
| `ASG_BOT_API_TOKEN` | When API enabled | Bearer token containing at least 32 characters. Generate a random secret; never commit or expose it in extension source. |
| `ASG_BOT_API_TARGET_GROUP` | When API enabled | The one destination group JID. It must also be present in `ASG_BOT_ALLOWED_GROUPS`. Clients cannot override it. |
| `ASG_BOT_API_ALLOWED_ORIGINS` | No | Comma-separated exact `chrome-extension://<extension-id>` origins. Empty denies browser cross-origin requests while allowing clients that send no `Origin`, such as curl. |

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

Enable the API for a desktop session with a randomly generated token:

```powershell
$bytes = New-Object byte[] 48
[Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$env:ASG_BOT_API_TOKEN = [Convert]::ToBase64String($bytes)
$env:ASG_BOT_API_ENABLED = "true"
$env:ASG_BOT_API_ADDR = "127.0.0.1:8080"
$env:ASG_BOT_API_TARGET_GROUP = "120363012345678901@g.us"
$env:ASG_BOT_API_ALLOWED_ORIGINS = "chrome-extension://abcdefghijklmnop"
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
messages and unlisted groups receive no response. Unknown slash commands are
ignored by command routing, but recognized tone indicators such as `/s` can
still trigger an automatic tone response.

## Supported behavior

Send these exact commands in an allowed group:

- `/ping` replies `Pong!`.
- `/echo <text>` repeats the supplied text.
- `/help` lists non-admin commands.
- `/remind <request>` creates a reminder from natural-language time and
  message text. Supported forms include:
  - `/remind @Alex "submit the form" tomorrow at 3pm`
  - `/remind tomorrow at 3pm @Alex "submit the form"`
  - `/remind @Alex in 20 minutes to join the call`
  - `/remind me to stretch in 30 minutes`
  - `/remind in 30 minutes "stretch"`
- `/rlist` lists the sender's reminders and their numeric IDs in this group.
- `/rupdate <id> <new request>` updates one owned reminder, for example
  `/rupdate 3 tomorrow at 4 pm call Mom`.
- `/rdelete <id>` deletes one owned reminder.
- `/die` replies and shuts down the bot, but only when sent by a number in
  `ASG_BOT_ADMIN_NUMBERS`.

Use WhatsApp's mention picker for recipient reminders; typed `@name` text
without mention metadata is not accepted. Each reminder may have at most one
mentioned recipient. With no recipient mention—including the `me` form—the
requester is the delivery target. Ambiguous input, multiple recipients,
malformed quotes, missing times, and empty reminder bodies are rejected with
usage examples rather than guessed.

The requester owns the reminder even when someone else receives it. Only the
requester can list, update, or delete it, and ownership is also scoped to the
group. Updating a reminder preserves its original recipient. At delivery, the
recipient is mentioned and the message identifies the requester, for example:

```text
@Alex Yatin asked me to remind you to "submit the form"
```

Self-reminders use:

```text
@Yatin you asked me to remind you to "stretch"
```

Messages containing a tone indicator defined in `tone/tone_commands.json`
(for example, `That was easy /s`) receive an automatic explanation. This
behavior, like commands, is limited to allowed groups.

## Message queue API

The API starts only after WhatsApp authentication succeeds. Submitted jobs are
stored in SQLite, survive restarts, and are always sent to
`ASG_BOT_API_TARGET_GROUP`. An omitted `sendAt` means immediate delivery; a
scheduled value must be a future RFC 3339 timestamp no more than one year away.
Message text is limited to 4,096 Unicode code points and request bodies to
16 KiB.

Submit from PowerShell with `curl.exe` (PowerShell's `curl` alias has different
argument handling):

```powershell
$body = @{
  text = "Queued from the desktop"
  idempotencyKey = [guid]::NewGuid().ToString()
} | ConvertTo-Json -Compress

$job = curl.exe --fail-with-body -sS `
  -H "Authorization: Bearer $env:ASG_BOT_API_TOKEN" `
  -H "Content-Type: application/json" `
  --data-binary $body `
  http://127.0.0.1:8080/v1/messages | ConvertFrom-Json

curl.exe --fail-with-body -sS `
  -H "Authorization: Bearer $env:ASG_BOT_API_TOKEN" `
  "http://127.0.0.1:8080/v1/messages/$($job.id)"
```

For a scheduled request, include a timestamp such as
`"sendAt":"2026-10-27T09:30:00+05:30"`. Submission returns `202 Accepted`.
Status is one of `pending`, `sending`, `sent`, or `failed`.

The `idempotencyKey` must be stable across client retries. Repeating the same
key and payload returns the existing job; reusing a key with different text or
schedule returns `409 Conflict`. Delivery is best-effort at-least-once. A
stable WhatsApp message ID reduces duplicates after a crash, but cannot
transactionally couple WhatsApp delivery to SQLite. Send failures retry up to
five times with delays of 5 seconds, 30 seconds, 2 minutes, and 10 minutes;
the fifth failed attempt becomes `failed`.

All routes, including `/healthz`, require the bearer token. The service also
limits each remote address to 30 requests per minute. Treat the token, database,
group JIDs, and API responses as secrets. Do not log request bodies or tokens,
put tokens in URLs, commit them, or embed them in a distributed extension.
Use a per-install secret storage mechanism for the extension.

### Chrome extension and Cloudflare Tunnel

Copy the installed extension ID from `chrome://extensions` and configure its
exact origin as `chrome-extension://<extension-id>`. Keep
`ASG_BOT_API_ALLOWED_ORIGINS` empty until browser access is required. CORS is a
browser boundary, not authentication; every extension request still needs the
bearer token.

Keep the bot bound to `127.0.0.1`. Configure Cloudflare Tunnel to forward one
HTTPS hostname to `http://127.0.0.1:8080`; do not expose port 8080 directly on
the LAN or Internet. TLS terminates at Cloudflare and the loopback hop remains
local to the device. Cloudflare Access is recommended as a second
authentication layer, but it does not replace the application bearer token.
Restrict the tunnel hostname to this service and configure a catch-all
not-found rule. Consult current Cloudflare documentation for tunnel
installation, credentials, DNS, and Access policy because those commands and
dashboards can change.

### Termux operation

Install Termux from F-Droid or GitHub, then install the required packages,
clone the repository, create the private runtime directory, and build:

```sh
pkg update
pkg install git golang openssl termux-api
git clone https://github.com/YatinAdityaT/ASG-bot.git "$HOME/asg-bot-src"
cd "$HOME/asg-bot-src"
mkdir -p "$HOME/asg-bot"
go build -o "$HOME/asg-bot/asg-bot" .
```

Keep the database in Termux's private home directory rather than shared
storage. Export the same variables shown above using shell syntax:

```sh
export ASG_BOT_ADMIN_NUMBERS='15551234567'
export ASG_BOT_ALLOWED_GROUPS='120363012345678901@g.us'
export ASG_BOT_DB_PATH="$HOME/asg-bot/whatsmeow.db"
export ASG_BOT_LOG_LEVEL='INFO'
export ASG_BOT_API_ENABLED='true'
export ASG_BOT_API_ADDR='127.0.0.1:8080'
export ASG_BOT_API_TOKEN="$(openssl rand -base64 48)"
export ASG_BOT_API_TARGET_GROUP='120363012345678901@g.us'
export ASG_BOT_API_ALLOWED_ORIGINS=''
```

Start the built bot with:

```sh
cd "$HOME/asg-bot"
./asg-bot
```

Persist the generated token in a private, mode-`600` environment file instead
of generating a new value at every boot. Do not place secrets directly in a
world-readable Termux:Boot script. Install the official Termux:API companion
and its Termux package from the same trusted source as Termux. Before starting
the bot, run `termux-wake-lock`; release it with `termux-wake-unlock` only after
shutdown. In Android settings, disable battery optimization for Termux and
allow background activity. Android can still stop background processes, so
monitor the service and expect queued jobs to resume after restart.

For boot startup, install the official Termux:Boot companion from the same
source as Termux, open it once, and place an executable launcher under
`~/.termux/boot/`. The launcher should acquire the wake lock, source the
private environment file, change to the repository or binary directory, and
start the bot and tunnel with logs redirected to private files. Test the
launcher interactively before relying on reboot behavior.

## Stop

Press **Ctrl+C** in the running PowerShell window. The bot cancels its scheduler,
stops and waits for the API, waits for scheduler work, disconnects WhatsApp,
closes SQLite, and prints `Shutting down bot...`. The admin-only `/die` command
performs the same application shutdown after its reply is sent.

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
5. Create a self-reminder due in a few minutes, use `/rlist` to get its ID,
   update it with `/rupdate`, and confirm the updated reminder mentions you at
   delivery.
6. Create a reminder for one mentioned participant. Confirm its acknowledgement,
   requester-only visibility in `/rlist`, exact recipient mention and requester
   attribution at delivery, and that the recipient cannot update or delete it.
7. Create another reminder and delete it with `/rdelete`; confirm it disappears
   from `/rlist`.
8. Confirm ambiguous input and a request with multiple mentions return usage
   examples without creating reminders.
9. Send a tone indicator such as `Acceptance test /s` and confirm the automatic
   explanation.
10. Confirm the same commands are ignored in an unlisted group.
11. Confirm the bot log contains no SQLite lock errors.
12. Press Ctrl+C and confirm clean shutdown.

For message-queue release acceptance, use a controlled maintenance window.
Never start a second process against the active production database. Stop the
old process cleanly, preserve and reuse its database, enable the API with a new
32-or-more-character random token and loopback address, and initially leave
browser origins empty. Confirm the existing WhatsApp session authenticates and
existing reminders still work. Then:

1. Verify an invalid bearer token returns `401`.
2. Submit one immediate job with a unique idempotency key and verify it appears
   exactly once in the configured target group.
3. Submit one job scheduled a few minutes ahead and verify it appears exactly
   once at the expected time.
4. Poll both status URLs until each reports `sent`.
5. Confirm the logs contain no SQLite lock errors.
6. Stop the process cleanly before restoring normal service configuration.

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