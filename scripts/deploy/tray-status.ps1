[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$DeployRoot,
    [switch]$Once
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2

$DeployRoot = [IO.Path]::GetFullPath($DeployRoot)
. "$PSScriptRoot\deploy-lib.ps1"
. "$PSScriptRoot\tray-lib.ps1"

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class ASGTrayNative {
    [DllImport("user32.dll", CharSet = CharSet.Auto)]
    public static extern bool DestroyIcon(IntPtr handle);
}
"@

$createdNew = $false
$mutex = [Threading.Mutex]::new($true, "Local\ASGBotStatusTray", [ref]$createdNew)
if (-not $createdNew) {
    $mutex.Dispose()
    return
}

$runtimeDirectory = Join-Path $DeployRoot "runtime"
$logDirectory = Join-Path $DeployRoot "logs"
$pidPath = Join-Path $runtimeDirectory "bot.pid"
$executable = Join-Path $runtimeDirectory "asg-bot.exe"
$commitPath = Join-Path $runtimeDirectory "deployed-commit.txt"
$lockPath = Join-Path $runtimeDirectory "deploy.lock"
$manifestPath = Join-Path $DeployRoot "meet-monitor\manifest.json"
$deployScript = Join-Path $DeployRoot "scripts\deploy\auto-deploy.ps1"

function New-ASGTrayIcon {
    param([Parameter(Mandatory = $true)][Drawing.Color]$StatusColor)

    $bitmap = [Drawing.Bitmap]::new(32, 32)
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try {
        $graphics.SmoothingMode = [Drawing.Drawing2D.SmoothingMode]::AntiAlias
        $background = [Drawing.SolidBrush]::new([Drawing.Color]::FromArgb(37, 99, 235))
        $letter = [Drawing.SolidBrush]::new([Drawing.Color]::White)
        $dot = [Drawing.SolidBrush]::new($StatusColor)
        $dotBorder = [Drawing.Pen]::new([Drawing.Color]::White, 2)
        $font = [Drawing.Font]::new("Segoe UI", 17, [Drawing.FontStyle]::Bold)
        $format = [Drawing.StringFormat]::new()
        try {
            $graphics.FillEllipse($background, 1, 1, 30, 30)
            $format.Alignment = [Drawing.StringAlignment]::Center
            $format.LineAlignment = [Drawing.StringAlignment]::Center
            $graphics.DrawString("A", $font, $letter, [Drawing.RectangleF]::new(1, -1, 30, 30), $format)
            $graphics.FillEllipse($dot, 21, 21, 10, 10)
            $graphics.DrawEllipse($dotBorder, 21, 21, 10, 10)
        } finally {
            $format.Dispose()
            $font.Dispose()
            $dotBorder.Dispose()
            $dot.Dispose()
            $letter.Dispose()
            $background.Dispose()
        }
        $handle = $bitmap.GetHicon()
        try {
            return [Drawing.Icon]::FromHandle($handle).Clone()
        } finally {
            [void][ASGTrayNative]::DestroyIcon($handle)
        }
    } finally {
        $graphics.Dispose()
        $bitmap.Dispose()
    }
}

function Start-HiddenDeploy {
    $arguments = "-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden " +
        "-ExecutionPolicy Bypass -File `"$deployScript`" -DeployRoot `"$DeployRoot`""
    Start-Process -FilePath "powershell.exe" `
        -ArgumentList $arguments `
        -WindowStyle Hidden | Out-Null
}

function Open-ASGLogs {
    New-Item -ItemType Directory -Path $logDirectory -Force | Out-Null
    Start-Process -FilePath "explorer.exe" -ArgumentList "`"$logDirectory`"" | Out-Null
}

function Get-CurrentTrayState {
    $botOnline = $false
    try {
        $botOnline = $null -ne (Get-RecordedBotProcess `
            -PIDPath $pidPath `
            -ExpectedExecutable $executable)
    } catch {
        $botOnline = $false
    }
    $deploying = Test-DeploymentLockHeld -Path $lockPath
    $commit = Read-TrimmedFile -Path $commitPath
    $version = "unknown"
    try {
        if (Test-Path -LiteralPath $manifestPath) {
            $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
            $version = [string]$manifest.version
        }
    } catch {
        $version = "unknown"
    }
    return [pscustomobject]@{
        BotOnline = $botOnline
        Deploying = $deploying
        Commit    = $commit
        Version   = $version
    }
}

if ($Once) {
    try {
        $state = Get-CurrentTrayState
        $color = if ($state.Deploying) {
            [Drawing.Color]::Goldenrod
        } elseif ($state.BotOnline) {
            [Drawing.Color]::LimeGreen
        } else {
            [Drawing.Color]::Crimson
        }
        $icon = New-ASGTrayIcon -StatusColor $color
        $icon.Dispose()
        Format-TrayTooltip `
            -BotOnline $state.BotOnline `
            -Deploying $state.Deploying `
            -Version $state.Version `
            -Commit $state.Commit
    } finally {
        try {
            $mutex.ReleaseMutex()
        } catch {
        }
        $mutex.Dispose()
    }
    return
}

[Windows.Forms.Application]::EnableVisualStyles()
$script:notify = [Windows.Forms.NotifyIcon]::new()
$script:menu = [Windows.Forms.ContextMenuStrip]::new()
$script:statusItem = [Windows.Forms.ToolStripMenuItem]::new("Status: starting")
$script:statusItem.Enabled = $false
$checkItem = [Windows.Forms.ToolStripMenuItem]::new("Check for updates now")
$restartItem = [Windows.Forms.ToolStripMenuItem]::new("Restart bot")
$logsItem = [Windows.Forms.ToolStripMenuItem]::new("Open logs")
$exitItem = [Windows.Forms.ToolStripMenuItem]::new("Exit tray")
[void]$script:menu.Items.Add($script:statusItem)
[void]$script:menu.Items.Add([Windows.Forms.ToolStripSeparator]::new())
[void]$script:menu.Items.Add($checkItem)
[void]$script:menu.Items.Add($restartItem)
[void]$script:menu.Items.Add($logsItem)
[void]$script:menu.Items.Add([Windows.Forms.ToolStripSeparator]::new())
[void]$script:menu.Items.Add($exitItem)
$script:notify.ContextMenuStrip = $script:menu
$script:notify.Text = "ASG Bot: starting"
$script:notify.Icon = New-ASGTrayIcon -StatusColor ([Drawing.Color]::Goldenrod)
$script:notify.Visible = $true
$script:lastVisualState = ""

function Update-ASGTray {
    $state = Get-CurrentTrayState
    $label = Get-TrayStatusLabel `
        -BotOnline $state.BotOnline `
        -Deploying $state.Deploying
    $tooltip = Format-TrayTooltip `
        -BotOnline $state.BotOnline `
        -Deploying $state.Deploying `
        -Version $state.Version `
        -Commit $state.Commit
    $script:notify.Text = $tooltip
    $script:statusItem.Text = "Status: $label"

    $visualState = if ($state.Deploying) {
        "deploying"
    } elseif ($state.BotOnline) {
        "online"
    } else {
        "offline"
    }
    if ($visualState -ne $script:lastVisualState) {
        $color = switch ($visualState) {
            "online" { [Drawing.Color]::LimeGreen }
            "deploying" { [Drawing.Color]::Goldenrod }
            default { [Drawing.Color]::Crimson }
        }
        $newIcon = New-ASGTrayIcon -StatusColor $color
        $oldIcon = $script:notify.Icon
        $script:notify.Icon = $newIcon
        if ($null -ne $oldIcon) {
            $oldIcon.Dispose()
        }
        $script:lastVisualState = $visualState
    }
}

$checkItem.Add_Click({
    Start-HiddenDeploy
})
$restartItem.Add_Click({
    try {
        Stop-RecordedBot -PIDPath $pidPath -ExpectedExecutable $executable
        Start-HiddenDeploy
    } catch {
        $script:notify.ShowBalloonTip(
            5000,
            "ASG Bot restart failed",
            $_.Exception.Message,
            [Windows.Forms.ToolTipIcon]::Error
        )
    }
})
$logsItem.Add_Click({ Open-ASGLogs })
$script:notify.Add_DoubleClick({ Open-ASGLogs })

$context = [Windows.Forms.ApplicationContext]::new()
$timer = [Windows.Forms.Timer]::new()
$timer.Interval = 5000
$timer.Add_Tick({ Update-ASGTray })
$exitItem.Add_Click({
    $timer.Stop()
    $script:notify.Visible = $false
    $context.ExitThread()
})

try {
    Update-ASGTray
    $timer.Start()
    [Windows.Forms.Application]::Run($context)
} finally {
    $timer.Stop()
    $timer.Dispose()
    $script:notify.Visible = $false
    if ($null -ne $script:notify.Icon) {
        $script:notify.Icon.Dispose()
    }
    $script:notify.Dispose()
    $script:menu.Dispose()
    $context.Dispose()
    try {
        $mutex.ReleaseMutex()
    } catch {
    }
    $mutex.Dispose()
}
