$ErrorActionPreference = "Stop"

function Assert-Equal {
    param($Actual, $Expected, [string]$Label)
    if ($Actual -ne $Expected) {
        throw "$Label`: got '$Actual', want '$Expected'"
    }
}

. "$PSScriptRoot\tray-lib.ps1"

Assert-Equal `
    (Get-TrayStatusLabel -BotOnline $true -Deploying $false) `
    "Online" `
    "online status"
Assert-Equal `
    (Get-TrayStatusLabel -BotOnline $false -Deploying $false) `
    "Offline" `
    "offline status"
Assert-Equal `
    (Get-TrayStatusLabel -BotOnline $true -Deploying $true) `
    "Deploying" `
    "deploying status"

$tooltip = Format-TrayTooltip `
    -BotOnline $true `
    -Deploying $false `
    -Version "1.9.1" `
    -Commit "abcdef123456"
Assert-Equal $tooltip "ASG Bot: Online | Ext 1.9.1 | abcdef1" "tooltip"

$longTooltip = Format-TrayTooltip `
    -BotOnline $false `
    -Deploying $false `
    -Version ("9" * 100) `
    -Commit ("f" * 100)
if ($longTooltip.Length -gt 63) {
    throw "tooltip length = $($longTooltip.Length), want at most 63"
}

Write-Output "tray-lib tests passed"
