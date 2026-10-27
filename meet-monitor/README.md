# Meet Monitor Chrome Extension

Meet Monitor is an unpacked Chrome extension for Google Meet. It tracks
participant join/leave events, confirmed camera and microphone state changes,
and visible chat messages. It provides a live popup, CSV export, optional
webhook and bot API forwarding, and an in-call monitoring banner. Rename events
are intentionally absent because Meet's rendered names can fluctuate without a
participant actually renaming themselves. Desktop/browser chat notifications
have also been removed.

Google Meet does not provide stable public DOM APIs for these features. The
selectors can stop working after a Meet update; update `src/selectors.js` when
that happens.

Meet can temporarily remove participant tiles during layout changes and
virtualization, so tile disappearance alone is never reported as a leave.
Leave events use Meet's English accessibility announcement (`Name left the
call`) instead. If Meet changes or localizes that announcement, leave detection
must be updated in `src/state.js`.

Participant names are extracted by a dedicated clean-name selector helper that
normalizes whitespace, removes duplicate rendered lines, and preserves the
first established non-empty name for each participant. Camera and microphone
changes must remain stable for two seconds before an event is emitted, reducing
false state changes during Meet layout updates.

For the authenticated bot API, chat events use Meet's stable message IDs as
idempotency keys. The server persists that identity, preventing duplicate bot
API delivery across extension retries, page refreshes, multiple tabs, and
service-worker restarts. The optional generic webhook has only the extension
background's five-second in-memory cross-context guard; it may receive a
repeated historical event after an extension or service-worker restart.

## Install in Chrome

1. Clone or download this repository.
2. Open `chrome://extensions` in Chrome.
3. Enable **Developer mode** in the upper-right corner.
4. Click **Load unpacked**.
5. Select the `meet-monitor` folder containing `manifest.json`.
6. Pin **Meet Monitor** from Chrome's Extensions menu if desired.
7. Open or refresh a `https://meet.google.com/` meeting tab.
8. Click the extension icon to view the roster and event feed.

Select the folder itself, not `src/` and not the repository root.

## Update after code changes

1. Pull the updated branch in the repository.
2. Open `chrome://extensions`.
3. Find **Meet Monitor** and click its **Reload** button.
4. Refresh every open Google Meet tab.

Chrome does not automatically reload unpacked extensions when files change.
An already-open Meet tab continues running its old content script until that
tab is refreshed.

## Configure options

Open the extension's **Details** page and choose **Extension options**, or click
**Options** in the popup.

### Monitoring banner

The banner is enabled by default and appears only in your local Meet page. It
does not prove that every meeting participant has consented. Follow applicable
privacy rules and meeting policies before recording or forwarding participant
or chat activity.

### Webhook

Enter an `http://` or `https://` URL to POST every event as JSON. On save,
Chrome asks for permission to contact that URL's origin. The webhook remains
disabled if permission is denied or the field is blank.

Example event:

```json
{
  "type": "chat_message",
  "id": "message-id",
  "meeting": "abc-defg-hij",
  "ts": "2026-10-27T07:30:00.000Z",
  "name": "Participant",
  "cam": null,
  "mic": null,
  "self": false,
  "host": false,
  "text": "Hello"
}
```

The extension forwards webhook requests from its background service worker.
Use an authenticated HTTPS endpoint for sensitive meeting data. Do not embed
long-lived secrets in this source tree.

### ASG Bot WhatsApp forwarding

To forward Meet activity to the configured WhatsApp test group:

1. Run the ASG bot with its authenticated message API enabled.
2. If Chrome and the bot are on different devices, expose the loopback API
   through an authenticated HTTPS tunnel; never expose port 8080 directly.
3. Copy the extension ID from `chrome://extensions`.
4. Add `chrome-extension://<extension-id>` to
   `ASG_BOT_API_ALLOWED_ORIGINS` and restart the bot.
5. In extension options, enter the public URL ending in
   `/v1/meet-events` and the bot's API token.
6. Save, approve Chrome's exact-origin permission request, reload the
   extension, and refresh the Meet tab.

The token is stored in `chrome.storage.local`, not Chrome synced storage. The
background service worker restricts local storage to trusted extension
contexts so Meet content scripts cannot read it. The bot URL and banner
preference are synced. Use HTTPS except for loopback-only local development.

Events awaiting API acceptance are retained in a local extension outbox and
retried sequentially with backoff, including after Chrome restarts. Every
accepted event then enters the bot's persistent outbound queue and is sent only
to the server-configured group. The extension cannot provide a destination
JID. The outbox holds at most 500 events; a service-worker warning is logged if
it fills. Non-retryable request errors and events that exhaust 10 transient
attempts are removed so one bad record cannot block later activity. Clearing
the Bot API URL and token also clears the local outbox.

The ASG bot forwarding and generic webhook are independent; configuring both
sends each raw event to both endpoints.

### Outbound sending control

Outbound sending defaults to **ON** and is controlled by the **Sending ON/OFF**
checkbox in the main extension popup. This single control governs both the
generic webhook and the bot API. Outbound delivery is locked to meeting
`jpy-ymgz-tdo`; all other meetings remain local-only even when sending is on.

## Export CSV

While a Meet tab is active, open the popup and click **Export CSV**. The export
contains at most the latest 500 events because the popup's `getState` response
exposes only that portion of the content script's history. The content script
retains up to 2,000 events in memory, but older retained events are not currently
available to popup export. Reloading or closing the Meet page clears its
in-memory history.

## Permissions

- `storage`: saves endpoint settings, the local API token, and banner
  preference.
- `alarms`: schedules bot API outbox retries.
- `https://meet.google.com/*`: runs the monitor in Meet tabs.
- Optional HTTP(S) origins: granted only when you save a webhook or bot API
  URL.

## Troubleshooting

### “No participants detected”

- Confirm the extension is enabled at `chrome://extensions`.
- Reload the extension, then refresh the Meet tab.
- Open Meet's participant panel to expose additional participant DOM nodes.
- If Meet changed its markup, inspect and update `src/selectors.js`.

### Chat messages do not appear

- Open the Meet chat panel so messages exist in the page DOM.
- Reload the extension and Meet tab.
- Check `src/selectors.js` chat selectors against the current Meet markup.

### An old Meet tab behaves differently after an update

Reloading the extension does not replace a content script that is already
running in an open Meet tab. Refresh that tab after the extension reload. If
the old content script encounters an invalidated extension context, it now
shuts down its observers and timers instead of surfacing an uncaught
invalidated-context error.

### Webhook receives nothing

- Re-save the webhook URL and approve Chrome's origin permission prompt.
- Open the extension service worker inspector from `chrome://extensions` and
  check its console.
- Verify the endpoint supports `POST`, accepts JSON, and permits the request.

### WhatsApp forwarding receives nothing

- Confirm the Bot API URL ends exactly in `/v1/meet-events`.
- Confirm the API token matches `ASG_BOT_API_TOKEN`.
- Confirm `ASG_BOT_API_ALLOWED_ORIGINS` contains the exact
  `chrome-extension://<extension-id>` origin, then restart the bot.
- Check the extension service-worker console for the generic delivery failure
  warning; tokens and event bodies are intentionally not logged.
- Submit a synthetic event with `curl` to isolate extension configuration from
  server or WhatsApp delivery problems.

## Local validation

From the repository root:

```powershell
node -e "JSON.parse(require('fs').readFileSync('meet-monitor/manifest.json','utf8'))"
node --check meet-monitor/src/selectors.js
node --check meet-monitor/src/state.js
node --check meet-monitor/src/content.js
node --check meet-monitor/src/delivery.js
node --check meet-monitor/src/background.js
node --check meet-monitor/src/popup.js
node --test meet-monitor/test/*.test.js
gofmt -d .
go vet ./...
go test ./... -count=1
go build ./...
git diff --check
git status --short
```

These commands validate release metadata, JavaScript syntax and tests, Go
formatting, vetting, tests, builds, and diff hygiene. They do not replace a live
Google Meet test because Meet's DOM is generated dynamically.
