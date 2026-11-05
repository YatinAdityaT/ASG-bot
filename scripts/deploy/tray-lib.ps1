function Get-TrayStatusLabel {
    param(
        [bool]$BotOnline,
        [bool]$Deploying
    )
    if ($Deploying) {
        return "Deploying"
    }
    if ($BotOnline) {
        return "Online"
    }
    return "Offline"
}

function Format-TrayTooltip {
    param(
        [bool]$BotOnline,
        [bool]$Deploying,
        [AllowEmptyString()][string]$Version,
        [AllowEmptyString()][string]$Commit
    )
    $status = Get-TrayStatusLabel -BotOnline $BotOnline -Deploying $Deploying
    if ([string]::IsNullOrWhiteSpace($Version)) {
        $Version = "unknown"
    }
    if ([string]::IsNullOrWhiteSpace($Commit)) {
        $shortCommit = "not deployed"
    } elseif ($Commit.Length -gt 7) {
        $shortCommit = $Commit.Substring(0, 7)
    } else {
        $shortCommit = $Commit
    }
    $text = "ASG Bot: $status | Ext $Version | $shortCommit"
    return $text.Substring(0, [Math]::Min(63, $text.Length))
}

function Test-DeploymentLockHeld {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) {
        return $false
    }
    $stream = $null
    try {
        $stream = [IO.File]::Open(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::ReadWrite,
            [IO.FileShare]::None
        )
        return $false
    } catch [IO.IOException] {
        return $true
    } finally {
        if ($null -ne $stream) {
            $stream.Dispose()
        }
    }
}
