# Windows Auto-Deploy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically validate and deploy GitHub `master` to the Windows bot and its unpacked Meet Monitor extension.

**Architecture:** A one-minute Windows Scheduled Task runs an idempotent PowerShell deploy command against a dedicated checkout. It validates the remote commit in a temporary worktree before updating the live checkout and restarting the compiled bot. Meet Monitor uses the GitHub manifest only as a version signal, reloads itself after the live checkout updates, and refreshes configured Meet tabs after its version changes.

**Tech Stack:** PowerShell 5.1+, Git, Go 1.26, Node.js test runner, Chrome Manifest V3.

## Global Constraints

- Deploy only `origin/master`.
- Never modify or deploy from the developer checkout.
- Keep the old bot running while staging tests and build execute.
- Failed validation must not update the live checkout or restart the bot.
- Never download or evaluate remote JavaScript.
- Extension changes must increment `meet-monitor/manifest.json`.
- Poll every 60 seconds.

---

### Task 1: Extension version signal and safe tab refresh

**Files:**
- Create: `meet-monitor/src/version-update.js`
- Create: `meet-monitor/test/version-update.test.js`
- Modify: `meet-monitor/manifest.json`
- Modify: `meet-monitor/src/background.js`
- Modify: `meet-monitor/test/background.test.js`

**Interfaces:**
- Produces: `MMVersionUpdate.isNewer(current, candidate) bool`
- Produces: `MMVersionUpdate.manifestURL string`
- Produces: background alarm `meet-monitor-update-check`
- Stores: `runningExtensionVersion` and `pendingExtensionVersion` in `chrome.storage.local`

- [ ] **Step 1: Write version comparison tests**

```javascript
const update = require('../src/version-update.js');

test('detects only a newer numeric Chrome extension version', () => {
  assert.equal(update.isNewer('1.8.0', '1.9.0'), true);
  assert.equal(update.isNewer('1.9.0', '1.9.0'), false);
  assert.equal(update.isNewer('2.0.0', '1.9.9'), false);
  assert.equal(update.isNewer('1.9.0', 'invalid'), false);
});
```

- [ ] **Step 2: Run the focused test and confirm failure**

Run: `cd meet-monitor; node --test test/version-update.test.js`

Expected: FAIL because `src/version-update.js` does not exist.

- [ ] **Step 3: Implement the pure version module**

```javascript
(function (root, factory) {
  const api = factory();
  root.MMVersionUpdate = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const manifestURL =
    'https://raw.githubusercontent.com/YatinAdityaT/ASG-bot/master/meet-monitor/manifest.json';
  const parts = value => /^\d+(?:\.\d+){0,3}$/.test(value)
    ? value.split('.').map(Number)
    : null;
  function isNewer(current, candidate) {
    const left = parts(current);
    const right = parts(candidate);
    if (!left || !right) return false;
    for (let i = 0; i < Math.max(left.length, right.length); i++) {
      if ((right[i] || 0) !== (left[i] || 0)) {
        return (right[i] || 0) > (left[i] || 0);
      }
    }
    return false;
  }
  return { isNewer, manifestURL };
});
```

- [ ] **Step 4: Add background behavior tests**

Extend the background harness with a `runtime.reload` counter, `tabs.reload`
calls, and an alarm event assertion. Test these exact cases:

```javascript
test('newer GitHub manifest reloads the extension', async () => {
  // Remote 1.9.0 while runtime is 1.8.0.
  // Alarm check stores pendingExtensionVersion and calls runtime.reload once.
});

test('new runtime version refreshes only allowlisted Meet tabs', async () => {
  // runningExtensionVersion=1.8.0, runtime=1.9.0.
  // Reload configured Meet tabs; do not reload unrelated tabs.
});

test('failed or non-newer version checks do not reload', async () => {
  // Network failure, malformed manifest, and equal version all reload zero times.
});
```

- [ ] **Step 5: Wire the update alarm and version transition**

Update the manifest:

```json
"version": "1.9.0",
"host_permissions": [
  "https://meet.google.com/*",
  "https://raw.githubusercontent.com/*"
]
```

Load `version-update.js` before other background dependencies. Create
`meet-monitor-update-check` with a one-minute period. On that alarm, fetch the
manifest with `{cache: 'no-store'}` and a 10-second abort timeout. If its
version is newer, store it as `pendingExtensionVersion` and call
`chrome.runtime.reload()`.

At worker initialization, compare `runningExtensionVersion` with
`chrome.runtime.getManifest().version`. On a real transition, update the stored
version and call `chrome.tabs.reload(tab.id)` only for URLs accepted by
`MMMeetPolicy.isMonitoredMeetURL`.

- [ ] **Step 6: Run extension tests**

Run: `cd meet-monitor; node --test`

Expected: all tests PASS.

- [ ] **Step 7: Commit**

```powershell
git add meet-monitor
git commit -m "feat: auto-reload Meet Monitor releases"
```

---

### Task 2: Transactional Windows deployment command

**Files:**
- Create: `scripts/deploy/deploy-lib.ps1`
- Create: `scripts/deploy/auto-deploy.ps1`
- Create: `scripts/deploy/test-deploy-lib.ps1`

**Interfaces:**
- Consumes: dedicated checkout path, remote `origin`, branch `master`
- Produces: `runtime/asg-bot.exe`, `runtime/deployed-commit.txt`, `runtime/bot.pid`
- Produces: `logs/auto-deploy.log`
- Supports: `auto-deploy.ps1 -DeployRoot <path> [-CheckOnly]`

- [ ] **Step 1: Write failing pure-helper tests**

```powershell
. "$PSScriptRoot\deploy-lib.ps1"

Assert-Equal (Get-UpdateDecision -Deployed "abc" -Remote "abc") "current"
Assert-Equal (Get-UpdateDecision -Deployed "abc" -Remote "def") "update"
Assert-Equal (Get-UpdateDecision -Deployed "" -Remote "def") "bootstrap"
```

The test script exits nonzero on the first mismatch and prints
`deploy-lib tests passed` on success.

- [ ] **Step 2: Confirm the helper test fails**

Run: `powershell -NoProfile -File scripts/deploy/test-deploy-lib.ps1`

Expected: FAIL because `deploy-lib.ps1` does not exist.

- [ ] **Step 3: Implement focused deployment helpers**

`deploy-lib.ps1` must contain:

```powershell
function Get-UpdateDecision {
    param([string]$Deployed, [string]$Remote)
    if ([string]::IsNullOrWhiteSpace($Remote)) { throw "Remote commit is empty" }
    if ([string]::IsNullOrWhiteSpace($Deployed)) { return "bootstrap" }
    if ($Deployed -eq $Remote) { return "current" }
    return "update"
}

function Invoke-Checked {
    param([string]$FilePath, [string[]]$Arguments, [string]$WorkingDirectory)
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
}
```

- [ ] **Step 4: Implement one idempotent deploy run**

`auto-deploy.ps1` must:

1. Acquire an exclusive lock at `runtime/deploy.lock`; exit successfully when
   another run owns it.
2. Run `git fetch origin master`.
3. Read `origin/master` and `runtime/deployed-commit.txt`.
4. For `-CheckOnly`, print compact JSON and exit without mutation.
5. Create a sibling temporary detached worktree at the remote commit.
6. Run `go test ./...`, `node --test` from `meet-monitor`, and build the
   candidate outside the disposable worktree with
   `go build -o <live>\runtime\asg-bot.next.exe .`.
7. Remove the staging worktree in `finally`.
8. Fast-forward the live checkout with
   `git merge --ff-only origin/master`.
9. Stop only the PID recorded in `runtime/bot.pid`, retain the current binary
   as `asg-bot.previous.exe`, promote `asg-bot.next.exe`, start it with
   `config.local.json` in the live checkout, and write its PID.
10. Wait two seconds and verify the process is still running. If startup
    failed, restore `asg-bot.previous.exe` and restart the previous release.
11. Write the deployed commit only after the new bot remains running.
12. Append timestamped, secret-free outcomes to `logs/auto-deploy.log`.

- [ ] **Step 5: Verify check-only behavior**

Run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/deploy/auto-deploy.ps1 -DeployRoot $PWD -CheckOnly
```

Expected: JSON containing `remoteCommit`, `deployedCommit`, and `decision`,
with no changed files or restarted process.

- [ ] **Step 6: Commit**

```powershell
git add scripts/deploy
git commit -m "feat: add transactional Windows bot deployer"
```

---

### Task 3: Installer, operations, and full verification

**Files:**
- Create: `scripts/deploy/install-auto-deploy.ps1`
- Create: `scripts/deploy/uninstall-auto-deploy.ps1`
- Modify: `README.md`

**Interfaces:**
- Installer parameters: `-DeployRoot`, `-RepositoryURL`, `-ConfigPath`
- Scheduled Task name: `ASG Bot Auto Deploy`
- Uninstaller leaves `config.local.json` and logs unless `-RemoveFiles` is set

- [ ] **Step 1: Implement the installer**

The installer must:

```powershell
param(
    [string]$DeployRoot = "$env:LOCALAPPDATA\ASGBotDeploy",
    [string]$RepositoryURL = "https://github.com/YatinAdityaT/ASG-bot.git",
    [Parameter(Mandatory=$true)][string]$ConfigPath
)
```

Clone `master` when the deployment checkout is absent, reject a mismatched
existing origin, copy the supplied config to `config.local.json`, and register
a current-user Scheduled Task that runs every minute:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  "<deploy>\scripts\deploy\auto-deploy.ps1" -DeployRoot "<deploy>"
```

Run the deploy command once immediately so setup either starts the validated
bot or reports an actionable error.

- [ ] **Step 2: Implement uninstall**

Stop the Scheduled Task, unregister it, and stop only the PID recorded by the
deployer. Preserve the deployment checkout by default. `-RemoveFiles` may
delete it only after confirming it matches the configured dedicated checkout.

- [ ] **Step 3: Document setup and Chrome requirement**

Add exact commands to `README.md`:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\deploy\install-auto-deploy.ps1 `
  -ConfigPath .\config.local.json
```

Document that Chrome must load the unpacked extension from:

```text
%LOCALAPPDATA%\ASGBotDeploy\meet-monitor
```

Also document log location, status with `Get-ScheduledTask`, manual deployment,
 uninstall, the one-minute delay, and the mandatory manifest version bump.

- [ ] **Step 4: Run full verification**

Run:

```powershell
go test ./...
go vet ./...
go build ./...
Set-Location meet-monitor
node --test
Set-Location ..
powershell -NoProfile -File scripts/deploy/test-deploy-lib.ps1
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 5: Commit and push directly to master**

```powershell
git add README.md scripts/deploy
git commit -m "docs: install Windows auto-deployment"
git push origin HEAD:master
```
