[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ApkPath,
    [string]$Device
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2

$ApkPath = [IO.Path]::GetFullPath($ApkPath)
if (-not (Test-Path -LiteralPath $ApkPath -PathType Leaf)) {
    throw "Termux:X11 APK does not exist: $ApkPath"
}
if ($null -eq (Get-Command adb -ErrorAction SilentlyContinue)) {
    throw "adb is not available on PATH"
}

$adbArgs = @()
if (-not [string]::IsNullOrWhiteSpace($Device)) {
    $adbArgs += @("-s", $Device)
}
$state = (& adb @adbArgs get-state 2>&1 | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $state -ne "device") {
    throw "ADB device is unavailable or unauthorized"
}

& adb @adbArgs install -r $ApkPath
if ($LASTEXITCODE -ne 0) {
    throw "Termux:X11 APK installation failed"
}
$packagePath = (& adb @adbArgs shell pm path com.termux.x11 | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $packagePath -notmatch "^package:") {
    throw "Termux:X11 package was not found after installation"
}

& adb @adbArgs shell am start -n com.termux.x11/.MainActivity | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "Termux:X11 installed but could not be opened"
}
Write-Output "Termux:X11 installed and opened on the authorized device."
