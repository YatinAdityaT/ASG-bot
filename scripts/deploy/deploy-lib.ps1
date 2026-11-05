function Get-UpdateDecision {
    param(
        [AllowEmptyString()][string]$Deployed,
        [AllowEmptyString()][string]$Remote
    )
    if ([string]::IsNullOrWhiteSpace($Remote)) {
        throw "Remote commit is empty"
    }
    if ([string]::IsNullOrWhiteSpace($Deployed)) {
        return "bootstrap"
    }
    if ($Deployed -eq $Remote) {
        return "current"
    }
    return "update"
}

function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$Arguments = @(),
        [Parameter(Mandatory = $true)][string]$WorkingDirectory
    )
    Push-Location $WorkingDirectory
    try {
        & $FilePath @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$FilePath failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
}

function Read-TrimmedFile {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) {
        return ""
    }
    return (Get-Content -LiteralPath $Path -Raw).Trim()
}

function Write-DeployLog {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Message
    )
    $directory = Split-Path -Parent $Path
    if ($directory) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    $timestamp = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    Add-Content -LiteralPath $Path -Value "$timestamp $Message"
}

function Stop-RecordedBot {
    param(
        [Parameter(Mandatory = $true)][string]$PIDPath,
        [Parameter(Mandatory = $true)][string]$ExpectedExecutable
    )
    $process = Get-RecordedBotProcess -PIDPath $PIDPath -ExpectedExecutable $ExpectedExecutable
    if ($null -eq $process) {
        return
    }
    $processID = $process.Id
    Stop-Process -Id $processID -Force
    Wait-Process -Id $processID -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $PIDPath -Force -ErrorAction SilentlyContinue
}

function Get-RecordedBotProcess {
    param(
        [Parameter(Mandatory = $true)][string]$PIDPath,
        [Parameter(Mandatory = $true)][string]$ExpectedExecutable
    )
    $rawPID = Read-TrimmedFile -Path $PIDPath
    if ([string]::IsNullOrWhiteSpace($rawPID)) {
        return $null
    }
    $processID = 0
    if (-not [int]::TryParse($rawPID, [ref]$processID)) {
        Remove-Item -LiteralPath $PIDPath -Force -ErrorAction SilentlyContinue
        return $null
    }
    $process = Get-Process -Id $processID -ErrorAction SilentlyContinue
    if ($null -eq $process) {
        Remove-Item -LiteralPath $PIDPath -Force -ErrorAction SilentlyContinue
        return $null
    }
    try {
        $actual = [IO.Path]::GetFullPath($process.Path)
        $expected = [IO.Path]::GetFullPath($ExpectedExecutable)
        if (-not [string]::Equals($actual, $expected, [StringComparison]::OrdinalIgnoreCase)) {
            throw "PID $processID does not belong to the deployed bot"
        }
    } catch {
        throw "PID $processID is not the deployed bot: $($_.Exception.Message)"
    }
    return $process
}

function Start-DeployedBot {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][string]$PIDPath,
        [Parameter(Mandatory = $true)][string]$StdoutPath,
        [Parameter(Mandatory = $true)][string]$StderrPath
    )
    $process = Start-Process -FilePath $Executable `
        -WorkingDirectory $WorkingDirectory `
        -RedirectStandardOutput $StdoutPath `
        -RedirectStandardError $StderrPath `
        -WindowStyle Hidden `
        -PassThru
    Set-Content -LiteralPath $PIDPath -Value $process.Id -NoNewline
    return $process
}
