[CmdletBinding()]
param(
    [string]$DeployRoot = (Join-Path $env:LOCALAPPDATA "ASGBotDeploy"),
    [string]$RepositoryURL = "https://github.com/YatinAdityaT/ASG-bot.git",
    [Parameter(Mandatory = $true)][string]$ConfigPath
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2

$DeployRoot = [IO.Path]::GetFullPath($DeployRoot)
$ConfigPath = [IO.Path]::GetFullPath($ConfigPath)
$taskName = "ASG Bot Auto Deploy"

foreach ($command in @("git", "go", "node", "powershell.exe")) {
    if ($null -eq (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "Required command is unavailable: $command"
    }
}
if (-not (Test-Path -LiteralPath $ConfigPath -PathType Leaf)) {
    throw "Config file does not exist: $ConfigPath"
}

if (-not (Test-Path -LiteralPath $DeployRoot)) {
    & git clone --branch master --single-branch $RepositoryURL $DeployRoot
    if ($LASTEXITCODE -ne 0) {
        throw "git clone failed with exit code $LASTEXITCODE"
    }
} elseif (-not (Test-Path -LiteralPath (Join-Path $DeployRoot ".git"))) {
    throw "DeployRoot exists but is not a Git checkout: $DeployRoot"
}

$actualOrigin = (& git -C $DeployRoot remote get-url origin | Out-String).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "Could not read deployment checkout origin"
}
$normalizeURL = {
    param([string]$Value)
    return ($Value.Trim().TrimEnd("/") -replace "\.git$", "")
}
if ((& $normalizeURL $actualOrigin) -ne (& $normalizeURL $RepositoryURL)) {
    throw "Deployment checkout origin '$actualOrigin' does not match '$RepositoryURL'"
}

$destinationConfig = Join-Path $DeployRoot "config.local.json"
if (-not [string]::Equals(
    $ConfigPath,
    [IO.Path]::GetFullPath($destinationConfig),
    [StringComparison]::OrdinalIgnoreCase
)) {
    Copy-Item -LiteralPath $ConfigPath -Destination $destinationConfig -Force
}

$excludePath = Join-Path $DeployRoot ".git\info\exclude"
$excludeLines = @()
if (Test-Path -LiteralPath $excludePath) {
    $excludeLines = @(Get-Content -LiteralPath $excludePath)
}
foreach ($entry in @("/runtime/", "/logs/")) {
    if ($excludeLines -notcontains $entry) {
        Add-Content -LiteralPath $excludePath -Value $entry
    }
}

$runtimeDirectory = Join-Path $DeployRoot "runtime"
New-Item -ItemType Directory -Path $runtimeDirectory -Force | Out-Null
$marker = [ordered]@{
    deployRoot    = $DeployRoot
    repositoryURL = $RepositoryURL
    installedAt   = (Get-Date).ToUniversalTime().ToString("o")
}
$marker | ConvertTo-Json | Set-Content `
    -LiteralPath (Join-Path $runtimeDirectory "managed-deployment.json") `
    -Encoding UTF8

$deployScript = Join-Path $DeployRoot "scripts\deploy\auto-deploy.ps1"
if (-not (Test-Path -LiteralPath $deployScript)) {
    throw "Deployment script is missing from master: $deployScript"
}
$taskArguments = "-NoProfile -ExecutionPolicy Bypass -File `"$deployScript`" -DeployRoot `"$DeployRoot`""
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $taskArguments
$trigger = New-ScheduledTaskTrigger `
    -Once `
    -At (Get-Date).AddMinutes(1) `
    -RepetitionInterval (New-TimeSpan -Minutes 1)
$settings = New-ScheduledTaskSettingsSet `
    -StartWhenAvailable `
    -MultipleInstances IgnoreNew `
    -ExecutionTimeLimit (New-TimeSpan -Minutes 30)
$principal = New-ScheduledTaskPrincipal `
    -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) `
    -LogonType Interactive `
    -RunLevel Limited

Register-ScheduledTask `
    -TaskName $taskName `
    -Action $action `
    -Trigger $trigger `
    -Settings $settings `
    -Principal $principal `
    -Description "Validates and deploys ASG Bot master every minute." `
    -Force | Out-Null

& powershell.exe -NoProfile -ExecutionPolicy Bypass `
    -File $deployScript `
    -DeployRoot $DeployRoot
if ($LASTEXITCODE -ne 0) {
    throw "Initial deployment failed with exit code $LASTEXITCODE"
}

Write-Output "Auto-deploy installed."
Write-Output "Bot checkout: $DeployRoot"
Write-Output "Load Chrome extension from: $(Join-Path $DeployRoot 'meet-monitor')"
