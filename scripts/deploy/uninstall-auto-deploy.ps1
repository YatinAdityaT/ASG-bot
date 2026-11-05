[CmdletBinding()]
param(
    [string]$DeployRoot = (Join-Path $env:LOCALAPPDATA "ASGBotDeploy"),
    [switch]$RemoveFiles
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2

$DeployRoot = [IO.Path]::GetFullPath($DeployRoot)
$taskName = "ASG Bot Auto Deploy"

$task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
if ($null -ne $task) {
    Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
}

$library = Join-Path $DeployRoot "scripts\deploy\deploy-lib.ps1"
$pidPath = Join-Path $DeployRoot "runtime\bot.pid"
$executable = Join-Path $DeployRoot "runtime\asg-bot.exe"
if (Test-Path -LiteralPath $library) {
    . $library
    Stop-RecordedBot -PIDPath $pidPath -ExpectedExecutable $executable
}

if ($RemoveFiles) {
    $markerPath = Join-Path $DeployRoot "runtime\managed-deployment.json"
    if (-not (Test-Path -LiteralPath $markerPath -PathType Leaf)) {
        throw "Refusing to remove an unmarked deployment checkout"
    }
    $marker = Get-Content -LiteralPath $markerPath -Raw | ConvertFrom-Json
    $markedRoot = [IO.Path]::GetFullPath([string]$marker.deployRoot)
    if (-not [string]::Equals(
        $DeployRoot,
        $markedRoot,
        [StringComparison]::OrdinalIgnoreCase
    )) {
        throw "Refusing to remove a deployment checkout with a mismatched marker"
    }
    Remove-Item -LiteralPath $DeployRoot -Recurse -Force
    Write-Output "Auto-deploy and deployment files removed."
    return
}

Write-Output "Auto-deploy removed. Deployment files and logs were preserved at $DeployRoot"
