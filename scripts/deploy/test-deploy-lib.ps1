$ErrorActionPreference = "Stop"

function Assert-Equal {
    param(
        [Parameter(Mandatory = $true)]$Actual,
        [Parameter(Mandatory = $true)]$Expected,
        [Parameter(Mandatory = $true)][string]$Label
    )
    if ($Actual -ne $Expected) {
        throw "$Label`: got '$Actual', want '$Expected'"
    }
}

. "$PSScriptRoot\deploy-lib.ps1"

Assert-Equal (Get-UpdateDecision -Deployed "abc" -Remote "abc") "current" "same commit"
Assert-Equal (Get-UpdateDecision -Deployed "abc" -Remote "def") "update" "changed commit"
Assert-Equal (Get-UpdateDecision -Deployed "" -Remote "def") "bootstrap" "first deployment"

$emptyRemoteFailed = $false
try {
    Get-UpdateDecision -Deployed "abc" -Remote ""
} catch {
    $emptyRemoteFailed = $true
}
Assert-Equal $emptyRemoteFailed $true "empty remote rejection"

$hiddenLauncher = Join-Path $PSScriptRoot "run-hidden.vbs"
$hiddenMarker = Join-Path ([IO.Path]::GetTempPath()) "asg-hidden-launcher-$PID.txt"
Remove-Item -LiteralPath $hiddenMarker -Force -ErrorAction SilentlyContinue
& cscript.exe //B //NoLogo $hiddenLauncher `
    -NoProfile `
    -Command "Set-Content -LiteralPath '$hiddenMarker' -Value hidden"
for ($attempt = 0; $attempt -lt 50 -and -not (Test-Path -LiteralPath $hiddenMarker); $attempt++) {
    Start-Sleep -Milliseconds 100
}
Assert-Equal (Test-Path -LiteralPath $hiddenMarker) $true "hidden launcher completion"
Assert-Equal (Get-Content -LiteralPath $hiddenMarker -Raw).Trim() "hidden" "hidden launcher output"
Remove-Item -LiteralPath $hiddenMarker -Force

$installerSource = Get-Content `
    -LiteralPath (Join-Path $PSScriptRoot "install-auto-deploy.ps1") `
    -Raw
$windowlessTaskActions = [regex]::Matches(
    $installerSource,
    'New-ScheduledTaskAction -Execute "wscript\.exe"'
).Count
Assert-Equal $windowlessTaskActions 2 "windowless scheduled task actions"

$traySource = Get-Content -LiteralPath (Join-Path $PSScriptRoot "tray-status.ps1") -Raw
Assert-Equal `
    ($traySource -match 'Start-Process -FilePath "wscript\.exe"') `
    $true `
    "windowless tray update action"

Write-Output "deploy-lib tests passed"
