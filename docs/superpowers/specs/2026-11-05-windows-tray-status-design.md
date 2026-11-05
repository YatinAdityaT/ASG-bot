# Windows Tray Status Design

## Goal

Make the remotely managed ASG bot feel like a Windows application by showing a
notification-area icon with live status and common controls, without opening
terminal windows.

## Tray process

`scripts/deploy/tray-status.ps1` runs as a hidden, interactive PowerShell
process and uses `System.Windows.Forms.NotifyIcon`. It reads only deployment
artifacts from the dedicated checkout:

- `runtime/bot.pid`
- `runtime/deployed-commit.txt`
- `runtime/deploy.lock`
- `meet-monitor/manifest.json`
- `logs/auto-deploy.log`

The process refreshes every five seconds. The tooltip reports bot
online/offline state, Meet Monitor version, and the short deployed commit.

## Icon

The icon is drawn locally with `System.Drawing`: a blue circular background
with a white `A`. A lower-right status dot is green when the recorded bot
process is healthy, red when offline, and amber while the deployment lock is
held. No downloaded image or icon dependency is required.

## Menu actions

The right-click menu contains:

- A disabled live status line.
- **Check for updates now**: starts `auto-deploy.ps1` hidden.
- **Restart bot**: safely stops only the recorded deployed executable and then
  starts `auto-deploy.ps1` hidden.
- **Open logs**: opens the deployment log directory in Explorer.
- **Exit tray**: closes only the tray UI; it does not stop the bot or updater.

Actions are asynchronous so the icon remains responsive.

## Installation and removal

The existing installer registers a second task named `ASG Bot Status` with an
interactive current-user principal and an at-logon trigger. It starts the tray
script with hidden PowerShell and also launches it immediately after install.

The uninstall script stops and unregisters both `ASG Bot Auto Deploy` and
`ASG Bot Status`. Existing safety rules for bot process termination and file
removal remain unchanged.

## Verification

Pure status and tooltip helpers are tested without opening UI. All PowerShell
files receive a parser check. Manual acceptance confirms icon color, tooltip,
menu actions, hidden execution, and clean removal on the bot laptop.
