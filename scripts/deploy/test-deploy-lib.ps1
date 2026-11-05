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

Write-Output "deploy-lib tests passed"
