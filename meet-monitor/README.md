# Meet Monitor Chrome Extension

Meet Monitor is an unpacked Chrome extension for Google Meet. It tracks
participant join/leave events, camera and microphone state changes, renames,
and visible chat messages. It provides a live popup, desktop chat
notifications, CSV export, an optional webhook, and an in-call monitoring
banner.

Google Meet does not provide stable public DOM APIs for these features. The
selectors can stop working after a Meet update; update `src/selectors.js` when
that happens.

Meet can temporarily remove participant tiles during layout changes and
virtualization, so tile disappearance alone is never reported as a leave.
Leave events use Meet's English accessibility announcement (`Name left the
call`) instead. If Meet changes or localizes that announcement, leave detection
must be updated in `src/state.js`.

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

1. Open `chrome://extensions`.
2. Find **Meet Monitor**.
3. Click its **Reload** button.
4. Refresh any open Google Meet tabs.

Chrome does not automatically reload unpacked extensions when files change.

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
JID. The outbox holds at most 500 events; a desktop notification warns if it
fills. Non-retryable request errors and events that exhaust 10 transient
attempts are removed so one bad record cannot block later activity. Clearing
the Bot API URL and token also clears the local outbox.

The ASG bot forwarding and generic webhook are independent; configuring both
sends each raw event to both endpoints.

## Export CSV

While a Meet tab is active, open the popup and click **Export CSV**. The export
contains the latest events retained by the content script, up to 2,000 events.
Reloading or closing the Meet page clears its in-memory history.

## Permissions

- `storage`: saves endpoint settings, the local API token, and banner
  preference.
- `notifications`: shows desktop notifications for visible Meet chat messages.
- `https://meet.google.com/*`: runs the monitor in Meet tabs.
- `https://www.gstatic.com/*`: loads the notification icon.
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

### Desktop notifications do not appear

- Allow Chrome notifications in the operating system.
- Confirm the extension has the `notifications` permission.
- Check the extension service worker console for errors.

## Local validation

From the repository root:

```powershell
node -e "JSON.parse(require('fs').readFileSync('meet-monitor/manifest.json','utf8'))"
node --check meet-monitor/src/selectors.js
node --check meet-monitor/src/content.js
node --check meet-monitor/src/delivery.js
node --check meet-monitor/src/background.js
node --check meet-monitor/src/state.js
node --check meet-monitor/src/csv.js
node --check meet-monitor/src/popup.js
node --check meet-monitor/src/options.js
node --test meet-monitor/test/*.test.js
```

These commands validate JSON and JavaScript syntax. They do not replace a live
Google Meet test because Meet's DOM is generated dynamically.
