# Meet Monitor Chrome Extension

Meet Monitor `v1.11.1` is an unpacked Chrome extension for Google Meet. It
tracks participant joins and leaves, camera and microphone changes, and chat
messages for configured Google Meet codes.

## Operator setup

1. Run the bot with `ASG_BOT_MEET_CODE=jpy-ymgz-tdo`.
2. In Chrome, open `chrome://extensions`, enable **Developer mode**, and load
   the `meet-monitor` folder as an unpacked extension.
3. Open extension options from the extension details page.
4. Add the comma-separated Google Meet codes that may be monitored. The first
   code is the primary call used by `/status`, `/incall`, and `/meet`.
5. When using the managed auto-deploy checkout, the Bot API URL and token are
   generated from `config.local.json` and loaded automatically. Manual
   installations can still enter both values in Options.
6. Configure the optional daily schedule. It defaults to leaving the primary
   call at midnight and rejoining at 5:00 AM in the bot laptop's local time.

Chrome does not hot-reload unpacked extensions by itself. Without the
auto-deploy setup, reload the extension and refresh all open Meet tabs after
every code change that affects forwarding or content scripts.

When loaded from the dedicated auto-deploy checkout, Meet Monitor checks
GitHub once a minute. A newer manifest version reloads the extension after the
checkout updates, then reinjects monitoring without reloading open Meet tabs.
Every extension release must increment `manifest.json`.

## Daily use

- Click the Meet Monitor toolbar button to open its popup. Use **Open Monitor**
  in the popup for the persistent side panel.
- The toolbar popup remains available so options can always be reached. The
  side panel and monitoring activate only on configured Meet codes.
- The primary call follows the configured daily leave/rejoin schedule, and its
  chat panel opens automatically after joining.
- Use **Monitoring this tab** to enable or disable each Meet tab independently.
  Enabled configured tabs can monitor simultaneously.
- The first configured code is the primary call. Other configured codes
  forward their events without replacing the primary `/status` and `/incall`
  snapshot.
- Health labels follow the browser theme and are bold, uppercase, and
  color-coded: green is online/connected, red is offline/disconnected, and
  amber is unknown. API and WhatsApp stay unknown until a recent API response
  provides evidence.
- The roster is a Name/Cam/Mic table and omits the monitor's own
  `ADHD SUPPORT GROUP` participant.
- Use **Reload extension** after installing updated extension files, then
  refresh each open Meet tab. Reloading clears extension runtime state but
  preserves saved options.
- Use **Copy identity probe** while the Meet People panel is open to copy the
  participant IDs, identity-related DOM attributes, and any email addresses
  that Google Meet has actually rendered. The probe stays local until pasted.

## Health timing and reconnect behavior

- The extension sends a Meet state heartbeat every `30s`.
- Content is considered fresh only if the elected Meet tab has observed the
  meeting within `45s`.
- API and WhatsApp status evidence is considered fresh for `45s`; stale or
  missing evidence is shown as unknown instead of disconnected.
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

Use `/incall` to list everyone currently detected in the call, showing only
camera-off and microphone-on exceptions. Use `/pingcall` to mention every member of the current
WhatsApp group and include the current call roster. Use
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
- Side panel shows content offline: keep the primary configured Meet tab open,
  enabled, and active enough to produce observations. A tab becomes stale
  after `45s`.
- Open Monitor reports an unconfigured meeting: add its Meet code in options,
  save, and refresh that tab.
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
- at least one refreshed primary Meet tab

Closing Chrome, disabling the extension, or leaving only stale Meet tabs stops
fresh observations. Restarted components recover from a silent baseline rather
than replaying old participant changes.
