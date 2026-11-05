# Meet Monitor UI and Health Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Meet Monitor health accurate, its panel tab-scoped and theme-aware, its roster concise, and its probed emails normalized.

**Architecture:** The service worker remains the source of policy and monitor-model data. It configures Chrome's per-tab side-panel availability from the existing monitored-link policy and emits nullable health data when evidence is stale. The side-panel view renders that model without inferring missing values.

**Tech Stack:** Chrome Manifest V3 APIs, browser JavaScript, HTML/CSS, Node.js built-in test runner.

## Global Constraints

- Preserve the toolbar popup and its **Open Monitor** action.
- Enable the side panel only on configured Google Meet tabs.
- Treat missing or stale API/WhatsApp evidence as `UNKNOWN`.
- Exclude `self` and `ADHD SUPPORT GROUP` from the roster.
- Do not infer participant email addresses from page-level account data.

---

### Task 1: Tab-scoped side panel and truthful health model

**Files:**
- Modify: `meet-monitor/src/background.js`
- Test: `meet-monitor/test/background.test.js`

**Interfaces:**
- Consumes: `isMonitoredMeetUrl(url, links)` from `src/meet-policy.js`
- Produces: `updateMeetAction(tabId, url)` side-panel configuration and nullable `whatsAppConnected`

- [ ] **Step 1: Write failing tests**

Add assertions that configured tabs call:

```js
chrome.sidePanel.setOptions({
  tabId,
  path: "src/side-panel.html",
  enabled: true,
});
```

and unconfigured tabs call the same API with `enabled: false`. Add a monitor-model case where no fresh API response yields `apiState: "unknown"` and `whatsAppConnected: null`.

- [ ] **Step 2: Verify red**

Run: `node --test meet-monitor/test/background.test.js`

Expected: failures for absent `sidePanel.setOptions` calls and stale WhatsApp data being coerced to `false`.

- [ ] **Step 3: Implement minimal behavior**

In `updateMeetAction`, set per-tab side-panel options from the same `allowed` decision used for the toolbar action. In `getMonitorModel`, emit:

```js
whatsAppConnected: apiFresh
  ? Boolean(result?.whatsAppConnected)
  : null
```

and distinguish unknown API evidence from a recent explicit failure.

- [ ] **Step 4: Verify green**

Run: `node --test meet-monitor/test/background.test.js`

Expected: all background tests pass.

### Task 2: Theme-aware health and roster table

**Files:**
- Modify: `meet-monitor/src/side-panel.html`
- Modify: `meet-monitor/src/side-panel.js`
- Test: `meet-monitor/test/side-panel.test.js`
- Test: `meet-monitor/test/extension-ui.test.js`

**Interfaces:**
- Consumes: monitor model from `GET_MONITOR_MODEL`
- Produces: uppercase health labels and Name/Cam/Mic roster rows

- [ ] **Step 1: Write failing tests**

Assert that health states render `ONLINE`, `OFFLINE`, `CONNECTED`,
`DISCONNECTED`, or `UNKNOWN`; that rows marked `self` or named
`ADHD SUPPORT GROUP` are omitted; and that roster cells are created with text
nodes. Assert the HTML has `prefers-color-scheme: dark`, bold health styling,
and a table header containing Name/Cam/Mic.

- [ ] **Step 2: Verify red**

Run: `node --test meet-monitor/test/side-panel.test.js meet-monitor/test/extension-ui.test.js`

Expected: failures for title-case health values, list markup, missing filtering,
and missing theme rules.

- [ ] **Step 3: Implement minimal behavior**

Replace the roster list with:

```html
<table>
  <thead><tr><th>Name</th><th>Cam</th><th>Mic</th></tr></thead>
  <tbody id="roster"></tbody>
</table>
```

Use CSS variables with a `prefers-color-scheme: dark` override. Normalize
health output with `String(state ?? "unknown").toUpperCase()`. Filter roster
entries before rendering and create one cell per participant field.

- [ ] **Step 4: Verify green**

Run: `node --test meet-monitor/test/side-panel.test.js meet-monitor/test/extension-ui.test.js`

Expected: all selected UI tests pass.

### Task 3: Normalize identity-probe emails and release

**Files:**
- Modify: `meet-monitor/src/identity-probe.js`
- Modify: `meet-monitor/manifest.json`
- Modify: `meet-monitor/README.md`
- Test: `meet-monitor/test/identity-probe.test.js`

**Interfaces:**
- Produces: canonical email candidates without quote/apostrophe artifacts

- [ ] **Step 1: Write failing normalization test**

Probe text containing both `"'adhdsg000@gmail.com"` and
`"adhdsg000@gmail.com"` and assert:

```js
assert.deepEqual(result.emailCandidates, ["adhdsg000@gmail.com"]);
```

- [ ] **Step 2: Verify red**

Run: `node --test meet-monitor/test/identity-probe.test.js`

Expected: the result contains a leading-apostrophe duplicate.

- [ ] **Step 3: Implement and document**

Strip leading/trailing straight or curly quote characters from each regex
match before lowercasing and adding it to the set. Increment the manifest patch
version to `1.9.2` and document unknown health and tab-scoped panel behavior.

- [ ] **Step 4: Run complete verification**

Run: `node --test meet-monitor/test/*.test.js`

Expected: zero failures.

- [ ] **Step 5: Commit**

```powershell
git add meet-monitor
git commit -m "fix: make Meet Monitor status accurate"
```
