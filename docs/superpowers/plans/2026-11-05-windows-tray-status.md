# Windows Tray Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a hidden Windows notification-area application that shows ASG bot status and offers update, restart, and log controls.

**Architecture:** A PowerShell Windows Forms `NotifyIcon` process reads the existing deployment PID, commit, manifest, lock, and logs. A separate pure helper module formats bounded tooltip text and status values. The installer registers the tray at user login while the existing one-minute deployment task remains independent.

**Tech Stack:** Windows PowerShell 5.1, System.Windows.Forms, System.Drawing, Windows Scheduled Tasks.

## Global Constraints

- Do not open terminal windows.
- Do not add third-party dependencies.
- Closing the tray must not stop the bot or auto-deployer.
- Never stop a process unless its executable matches the managed bot path.
- Tooltip text must fit the Windows `NotifyIcon.Text` 63-character limit.

---

### Task 1: Tray status process

**Files:**
- Create: `scripts/deploy/tray-lib.ps1`
- Create: `scripts/deploy/tray-status.ps1`
- Create: `scripts/deploy/test-tray-lib.ps1`

**Interfaces:**
- Produces: `Format-TrayTooltip -BotOnline <bool> -Deploying <bool> -Version <string> -Commit <string>`
- Produces: `Get-TrayStatusLabel -BotOnline <bool> -Deploying <bool>`
- Consumes: deployment root and existing `deploy-lib.ps1`

- [ ] **Step 1: Write failing helper tests**

```powershell
. "$PSScriptRoot\tray-lib.ps1"

Assert-Equal (Get-TrayStatusLabel -BotOnline $true -Deploying $false) "Online"
Assert-Equal (Get-TrayStatusLabel -BotOnline $false -Deploying $false) "Offline"
Assert-Equal (Get-TrayStatusLabel -BotOnline $true -Deploying $true) "Deploying"

$tooltip = Format-TrayTooltip -BotOnline $true -Deploying $false `
    -Version "1.9.1" -Commit "abcdef123456"
Assert-Equal $tooltip "ASG Bot: Online | Ext 1.9.1 | abcdef1"
if ($tooltip.Length -gt 63) { throw "tooltip too long" }
```

- [ ] **Step 2: Confirm the test fails**

Run: `powershell -NoProfile -File scripts/deploy/test-tray-lib.ps1`

Expected: FAIL because `tray-lib.ps1` does not exist.

- [ ] **Step 3: Implement pure status helpers**

```powershell
function Get-TrayStatusLabel {
    param([bool]$BotOnline, [bool]$Deploying)
    if ($Deploying) { return "Deploying" }
    if ($BotOnline) { return "Online" }
    return "Offline"
}

function Format-TrayTooltip {
    param([bool]$BotOnline, [bool]$Deploying, [string]$Version, [string]$Commit)
    $status = Get-TrayStatusLabel -BotOnline $BotOnline -Deploying $Deploying
    $shortCommit = if ($Commit.Length -gt 7) { $Commit.Substring(0, 7) } else { $Commit }
    $text = "ASG Bot: $status | Ext $Version | $shortCommit"
    return $text.Substring(0, [Math]::Min(63, $text.Length))
}
```

- [ ] **Step 4: Implement the tray process**

`tray-status.ps1 -DeployRoot <path>` must:

1. Acquire a named mutex so only one tray process runs.
2. Load Windows Forms and Drawing.
3. Refresh status every five seconds from the recorded bot PID, manifest
   version, deployed commit, and exclusive deployment lock.
4. Draw a blue circle with white `A` and green/red/amber status dot.
5. Replace and dispose icons only when the status changes.
6. Provide status, update, restart, logs, and exit menu items.
7. Start update/restart PowerShell commands with `-WindowStyle Hidden`.
8. Dispose the icon and release the mutex on exit.

- [ ] **Step 5: Run helper and parser tests**

```powershell
powershell -NoProfile -File scripts/deploy/test-tray-lib.ps1
```

Parse every `scripts/deploy/*.ps1` with
`System.Management.Automation.Language.Parser.ParseFile`.

Expected: all checks exit 0.

- [ ] **Step 6: Commit**

```powershell
git add scripts/deploy/tray-lib.ps1 scripts/deploy/tray-status.ps1 scripts/deploy/test-tray-lib.ps1
git commit -m "feat: add Windows tray status application"
```

---

### Task 2: Install, remove, and document the tray

**Files:**
- Modify: `scripts/deploy/install-auto-deploy.ps1`
- Modify: `scripts/deploy/uninstall-auto-deploy.ps1`
- Modify: `README.md`

**Interfaces:**
- Scheduled Task: `ASG Bot Status`
- Trigger: current-user logon
- Action: hidden PowerShell running `tray-status.ps1 -DeployRoot <path>`

- [ ] **Step 1: Register the tray during installation**

Create a hidden action:

```powershell
$trayArguments = "-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden " +
    "-ExecutionPolicy Bypass -File `"$trayScript`" -DeployRoot `"$DeployRoot`""
$trayAction = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $trayArguments
$trayTrigger = New-ScheduledTaskTrigger -AtLogOn -User $currentUser
```

Register `ASG Bot Status` using the existing interactive current-user
principal and start it immediately after successful bot deployment.

- [ ] **Step 2: Remove the tray task during uninstall**

Stop and unregister both `ASG Bot Auto Deploy` and `ASG Bot Status`. Do not
stop the bot merely because the tray task is removed; retain the existing
explicit managed-bot shutdown behavior.

- [ ] **Step 3: Document icon behavior and controls**

Document the icon colors, tooltip fields, right-click actions, log location,
and that **Exit tray** leaves the bot and updater running until the tray starts
again at the next login or installer run.

- [ ] **Step 4: Run full verification**

```powershell
go test ./...
go vet ./...
go build ./...
Set-Location meet-monitor
node --test
Set-Location ..
powershell -NoProfile -File scripts/deploy/test-deploy-lib.ps1
powershell -NoProfile -File scripts/deploy/test-tray-lib.ps1
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 5: Commit and push to master**

```powershell
git add README.md scripts/deploy
git commit -m "feat: install ASG Bot tray status"
git push origin HEAD:master
```
