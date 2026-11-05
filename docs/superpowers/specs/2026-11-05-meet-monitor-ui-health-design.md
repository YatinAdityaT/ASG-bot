# Meet Monitor UI and Health Design

## Goal

Make the Meet Monitor accurate, readable, and available only on configured
Google Meet tabs while preserving the restored toolbar popup.

## Behavior

- The toolbar popup remains available. Its **Open Monitor** button opens the
  side panel for the active configured Meet tab.
- The side panel is enabled only for URLs matching the configured monitored
  Meet links. Other tabs must not expose a working Meet Monitor panel.
- The panel follows the browser/operating-system light or dark theme.
- Health values are bold and uppercase.
- API health is `ONLINE` only after a recent successful API response,
  `OFFLINE` after a recent failure, and `UNKNOWN` when there is no current
  evidence.
- WhatsApp health is `CONNECTED` or `DISCONNECTED` only when supplied by a
  fresh API response; otherwise it is `UNKNOWN`.
- The roster is a Name/Cam/Mic table.
- The monitor account is excluded when the participant is marked `self` or its
  normalized name is `ADHD SUPPORT GROUP`.
- Identity-probe email candidates are normalized by removing surrounding
  quote/apostrophe artifacts before deduplication.

## Identity Constraints

Google Meet currently exposes ephemeral participant device identifiers such as
`spaces/.../devices/...`. The observed DOM does not expose participant email
addresses. The monitor account's page-level email may be visible, but it must
not be treated as another participant's stable identity.

## Testing

Regression tests cover side-panel tab scoping, stale/unknown health semantics,
uppercase health rendering, roster filtering/table rendering, theme hooks, and
email normalization. The complete extension test suite must pass before
release. The extension patch version is incremented for auto-update detection.
