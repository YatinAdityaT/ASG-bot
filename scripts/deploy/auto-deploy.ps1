[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$DeployRoot,
    [switch]$CheckOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2

. "$PSScriptRoot\deploy-lib.ps1"

$DeployRoot = [IO.Path]::GetFullPath($DeployRoot)
$runtimeDirectory = Join-Path $DeployRoot "runtime"
$logDirectory = Join-Path $DeployRoot "logs"
$deployLog = Join-Path $logDirectory "auto-deploy.log"
$deployedCommitPath = Join-Path $runtimeDirectory "deployed-commit.txt"
$pidPath = Join-Path $runtimeDirectory "bot.pid"
$runtimeModePath = Join-Path $runtimeDirectory "runtime-mode.txt"
$currentExecutable = Join-Path $runtimeDirectory "asg-bot.exe"
$nextExecutable = Join-Path $runtimeDirectory "asg-bot.next.exe"
$previousExecutable = Join-Path $runtimeDirectory "asg-bot.previous.exe"
$stdoutPath = Join-Path $logDirectory "bot.stdout.log"
$stderrPath = Join-Path $logDirectory "bot.stderr.log"
$configPath = Join-Path $DeployRoot "config.local.json"
$lockPath = Join-Path $runtimeDirectory "deploy.lock"
$diagnosticURL = "https://ntfy.sh/asg-deploy-diagnostic-7f4a3c91d2e84b6f"

function Send-DeployDiagnostic {
    param([Parameter(Mandatory = $true)][string]$Status)
    try {
        Invoke-WebRequest `
            -UseBasicParsing `
            -Method Post `
            -Uri $diagnosticURL `
            -Body $Status `
            -TimeoutSec 10 | Out-Null
    } catch {
        # Diagnostics must never interrupt deployment.
    }
}

if (-not (Test-Path -LiteralPath (Join-Path $DeployRoot ".git"))) {
    throw "DeployRoot is not a Git checkout: $DeployRoot"
}
New-Item -ItemType Directory -Path $runtimeDirectory -Force | Out-Null
New-Item -ItemType Directory -Path $logDirectory -Force | Out-Null
if (Test-Path -LiteralPath $configPath) {
    Write-ExtensionLocalConfig `
        -ConfigPath $configPath `
        -DestinationPath (Join-Path $DeployRoot "meet-monitor\src\local-config.json") |
        Out-Null
}

$lock = $null
try {
    try {
        $lock = [IO.File]::Open(
            $lockPath,
            [IO.FileMode]::OpenOrCreate,
            [IO.FileAccess]::ReadWrite,
            [IO.FileShare]::None
        )
    } catch [IO.IOException] {
        return
    }

    function Get-GitValue {
        param([string[]]$Arguments)
        $output = & git -C $DeployRoot @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "git $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
        }
        return ($output | Out-String).Trim()
    }

    Invoke-Checked -FilePath "git" `
        -Arguments @("-C", $DeployRoot, "fetch", "origin", "master") `
        -WorkingDirectory $DeployRoot
    $remoteCommit = Get-GitValue @("rev-parse", "origin/master")
    $activeTarget = Get-GitValue @(
        "show",
        "${remoteCommit}:scripts/deploy/active-bot-target.txt"
    )
    if ($activeTarget -notin @("windows", "termux")) {
        throw "Invalid active bot target '$activeTarget'"
    }
    Send-DeployDiagnostic -Status "cycle:${remoteCommit}:$activeTarget"
    $desiredRuntimeMode = if ($activeTarget -eq "windows") {
        "bot"
    } else {
        "monitor-relay"
    }
    $deployedCommit = Read-TrimmedFile -Path $deployedCommitPath
    $decision = Get-UpdateDecision -Deployed $deployedCommit -Remote $remoteCommit
    if ($decision -eq "current" -and -not (Test-Path -LiteralPath $currentExecutable)) {
        $decision = "bootstrap"
    }

    if ($CheckOnly) {
        [ordered]@{
            remoteCommit   = $remoteCommit
            deployedCommit = $deployedCommit
            decision       = $decision
            activeTarget   = $activeTarget
        } | ConvertTo-Json -Compress
        return
    }

    if ($decision -eq "current") {
        $running = Get-RecordedBotProcess `
            -PIDPath $pidPath `
            -ExpectedExecutable $currentExecutable
        $deployedRuntimeMode = Read-TrimmedFile -Path $runtimeModePath
        if ($null -ne $running -and $deployedRuntimeMode -ne $desiredRuntimeMode) {
            Stop-RecordedBot `
                -PIDPath $pidPath `
                -ExpectedExecutable $currentExecutable
            $running = $null
        }
        if ($null -eq $running) {
            if (-not (Test-Path -LiteralPath $configPath)) {
                throw "Missing config.local.json in deployment checkout"
            }
            Start-DeployedBot `
                -Executable $currentExecutable `
                -WorkingDirectory $DeployRoot `
                -PIDPath $pidPath `
                -StdoutPath $stdoutPath `
                -StderrPath $stderrPath `
                -RuntimeMode $desiredRuntimeMode | Out-Null
            Set-Content `
                -LiteralPath $runtimeModePath `
                -Value $desiredRuntimeMode `
                -NoNewline
            Write-DeployLog `
                -Path $deployLog `
                -Message "restarted current commit $remoteCommit in $desiredRuntimeMode mode"
            Send-DeployDiagnostic -Status "started:${remoteCommit}:$desiredRuntimeMode"
        }
        return
    }

    $trackedChanges = Get-GitValue @("status", "--porcelain", "--untracked-files=no")
    if (-not [string]::IsNullOrWhiteSpace($trackedChanges)) {
        throw "Deployment checkout has tracked local changes"
    }
    if (-not (Test-Path -LiteralPath $configPath)) {
        throw "Missing config.local.json in deployment checkout"
    }

    Remove-Item -LiteralPath $nextExecutable -Force -ErrorAction SilentlyContinue
    $parent = Split-Path -Parent $DeployRoot
    $leaf = Split-Path -Leaf $DeployRoot
    $stagingRoot = Join-Path $parent "$leaf.staging.$PID"
    $worktreeAdded = $false
    try {
        Remove-Item -LiteralPath $stagingRoot -Recurse -Force -ErrorAction SilentlyContinue
        Invoke-Checked -FilePath "git" `
            -Arguments @("-C", $DeployRoot, "worktree", "add", "--detach", $stagingRoot, $remoteCommit) `
            -WorkingDirectory $DeployRoot
        $worktreeAdded = $true
        Invoke-Checked -FilePath "go" `
            -Arguments @("build", "-o", $nextExecutable, ".") `
            -WorkingDirectory $stagingRoot
    } finally {
        if ($worktreeAdded) {
            & git -C $DeployRoot worktree remove --force $stagingRoot 2>$null
        }
        Remove-Item -LiteralPath $stagingRoot -Recurse -Force -ErrorAction SilentlyContinue
    }

    Invoke-Checked -FilePath "git" `
        -Arguments @("-C", $DeployRoot, "merge", "--ff-only", "origin/master") `
        -WorkingDirectory $DeployRoot

    Stop-RecordedBot -PIDPath $pidPath -ExpectedExecutable $currentExecutable
    Remove-Item -LiteralPath $previousExecutable -Force -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $currentExecutable) {
        Move-Item -LiteralPath $currentExecutable -Destination $previousExecutable -Force
    }
    Move-Item -LiteralPath $nextExecutable -Destination $currentExecutable -Force

    try {
        $process = Start-DeployedBot `
            -Executable $currentExecutable `
            -WorkingDirectory $DeployRoot `
            -PIDPath $pidPath `
            -StdoutPath $stdoutPath `
            -StderrPath $stderrPath `
            -RuntimeMode $desiredRuntimeMode
        Start-Sleep -Seconds 2
        $running = Get-Process -Id $process.Id -ErrorAction SilentlyContinue
        if ($null -eq $running -or $running.HasExited) {
            throw "new bot process exited during startup"
        }
    } catch {
        Remove-Item -LiteralPath $pidPath -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $currentExecutable -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $previousExecutable) {
            Move-Item -LiteralPath $previousExecutable -Destination $currentExecutable -Force
            if ($activeTarget -eq "windows") {
                Start-DeployedBot `
                    -Executable $currentExecutable `
                    -WorkingDirectory $DeployRoot `
                    -PIDPath $pidPath `
                    -StdoutPath $stdoutPath `
                    -StderrPath $stderrPath `
                    -RuntimeMode $desiredRuntimeMode | Out-Null
            }
        }
        throw
    }

    Set-Content -LiteralPath $deployedCommitPath -Value $remoteCommit -NoNewline
    Set-Content `
        -LiteralPath $runtimeModePath `
        -Value $desiredRuntimeMode `
        -NoNewline
    Write-DeployLog `
        -Path $deployLog `
        -Message "deployed commit $remoteCommit in $desiredRuntimeMode mode"
    Send-DeployDiagnostic -Status "started:${remoteCommit}:$desiredRuntimeMode"
} catch {
    Write-DeployLog -Path $deployLog -Message "deployment failed: $($_.Exception.Message)"
    Send-DeployDiagnostic -Status "failed"
    throw
} finally {
    if ($null -ne $lock) {
        $lock.Dispose()
    }
}
