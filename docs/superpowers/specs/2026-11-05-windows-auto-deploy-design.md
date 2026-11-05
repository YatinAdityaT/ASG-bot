# Windows Auto-Deploy Design

## Goal

Automatically deploy GitHub `master` to the always-on Windows bot and its
unpacked Meet Monitor extension without risking the developer checkout or
replacing a working bot with a broken build.

## Deployment checkout

The setup script creates a dedicated clean checkout outside the developer
workspace. The bot runs from this checkout, and Chrome loads the unpacked
extension from its `meet-monitor` directory. Local secrets remain in the
ignored `config.local.json`.

## Bot supervisor

A PowerShell supervisor starts the compiled bot and polls `origin/master`
every 60 seconds. When the remote commit changes, it:

1. Fast-forwards the deployment checkout.
2. Runs `go test ./...` and the extension's `node --test` suite while the old
   bot remains running.
3. Builds a temporary executable.
4. Stops the old bot only after verification succeeds.
5. Atomically promotes the executable and starts it.

Failures are logged and leave the previous bot running. The supervisor retries
when a later commit arrives. A Scheduled Task starts the supervisor at Windows
login.

## Extension update

Meet Monitor periodically reads only the version from the repository's raw
`manifest.json`. When GitHub has a newer version than the running extension,
it calls `chrome.runtime.reload()`. If the deployment checkout has not pulled
the files yet, subsequent checks retry.

After a successful version change, the new service worker reloads only
allowlisted Meet tabs so their content scripts also update. Saved options,
tokens, per-tab policy, and history remain in Chrome storage.

Extension changes must increment `manifest.json`. Bot-only commits need no
extension version change.

## Safety and operations

- No downloaded code is evaluated; GitHub supplies only a version signal.
- The supervisor accepts code only from the configured `origin/master`.
- Updates are fast-forward-only.
- Build and test failures do not interrupt the running bot.
- Logs record commit, validation, restart, and failure events without secrets.
- Setup, status, manual update, and uninstall commands are documented.

## Verification

Automated tests cover version comparison, update polling, tab refresh after a
version transition, and supervisor decision helpers. A manual acceptance check
uses a version-only test commit to confirm pull, bot restart, extension reload,
and configured Meet-tab refresh.
