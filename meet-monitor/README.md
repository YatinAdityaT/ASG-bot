# Meet Monitor Chrome Extension

Meet Monitor `v1.6.0` is an unpacked Chrome extension for Google Meet. It
tracks participant joins and leaves, camera and microphone changes, and chat
messages for the locked meeting code `jpy-ymgz-tdo`.

## Operator setup

1. Run the bot with `ASG_BOT_MEET_CODE=jpy-ymgz-tdo`.
2. In Chrome, open `chrome://extensions`, enable **Developer mode**, and load
   the `meet-monitor` folder as an unpacked extension.
3. Open extension options from the popup or the extension details page.
4. Set the Bot API URL to the server endpoint ending in `/v1/meet-events`.
   Meet Monitor stays configured against `/v1/meet-events` and derives
   `/v1/meet-state` and `/v1/meet-command` automatically on the same origin.
5. Paste the bot bearer token, save, approve the exact origin permission
   request, then reload the unpacked extension and refresh every open Meet tab.

Chrome does not hot-reload unpacked extensions. After every code or settings
change that affects forwarding or content scripts, reload the extension and
refresh all open Meet tabs.

## Daily use

- Use the popup for the quick roster view, recent event feed, CSV export, and
  the **Sending ON/OFF** toggle.
- Use **Open Monitor** in the popup to open the side panel for continuous
  health, roster, and timeline monitoring.
- The sending toggle controls webhook and WhatsApp event delivery only. Even
  with **Sending OFF**, the extension continues to send Meet state heartbeats
  so bot `/status` remains current.
- The extension only forwards outbound traffic for meeting `jpy-ymgz-tdo`.

## Health timing and reconnect behavior

- The extension sends a Meet state heartbeat every `30s`.
- Content is considered fresh only if the elected Meet tab has observed the
  meeting within `45s`.
- The server reports the monitor as offline after `90s` without an accepted
  heartbeat.
- After a stale tab takeover, extension restart, server restart, or other
  reconnect, the next accepted snapshot becomes a silent baseline. Operators
  should expect no join or leave replay flood from that reconnect alone.

## `/status` and WhatsApp output

Use `/status` in the configured WhatsApp group to check bot, WhatsApp, and Meet
monitor health. The reply includes bot online state, WhatsApp connected state,
monitor online or offline state, last heartbeat age, meeting code, participant
count, forwarding ON or OFF, and extension version.

Use `/call` to list everyone currently detected in the call with their camera
and microphone state. Use
`/meet <message>` to post a message into the Google Meet chat through the
monitor extension. Meet receives it as `"WhatsApp name" said "message"`, and
the monitor suppresses its own `You` chat events to prevent WhatsApp echoes.
Ordinary Meet chat remains local. A Meet participant can send `/w <message>`
to forward only that message to WhatsApp. Enabling Debug mode in the side
panel temporarily restores reflection of all Meet chat for troubleshooting.

WhatsApp event messages are prefix-free and look like:

- `🎥 Alex joined the call.`
- `🎥 Alex left the call.`
- `🎙️ Alex muted their mic.`
- `🎙️ Alex unmuted their mic.`
- `📷 Alex turned their camera on.`
- `📷 Alex turned their camera off.`
- `💬 Alex said: Hello`

## Troubleshooting

- No live data after an update: reload the unpacked extension, then refresh all
  open Meet tabs.
- Side panel shows content offline: keep one `jpy-ymgz-tdo` Meet tab open and
  active enough to produce observations. A tab becomes stale after `45s`.
- Side panel shows API error or `/status` shows monitor offline: confirm the
  bot is running, the URL still ends exactly in `/v1/meet-events`, the token is
  correct, and the server can accept `/v1/meet-state`.
- WhatsApp forwarding missing while `/status` stays healthy: check whether
  **Sending** is OFF. Heartbeats continue while event forwarding is disabled.
- Browser permission issues: confirm `ASG_BOT_API_ALLOWED_ORIGINS` contains the
  exact `chrome-extension://<extension-id>` origin, restart the bot, save the
  settings again, then reload the extension and refresh Meet tabs.
- If needed, inspect the extension service-worker console from
  `chrome://extensions` for retry and client-error warnings.

## Continuous-running requirements

For continuous monitoring and `/status` freshness, keep all of these running:

- the bot process
- Chrome with the unpacked extension loaded
- at least one refreshed `jpy-ymgz-tdo` Meet tab

Closing Chrome, disabling the extension, or leaving only stale Meet tabs stops
fresh observations. Restarted components recover from a silent baseline rather
than replaying old participant changes.
