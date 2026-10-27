# Meet Monitor Event Stability Design

## Goal

Meet Monitor must forward only meaningful Google Meet activity to WhatsApp:
participant joins and explicit departures, confirmed camera and microphone
changes, and each chat message once. Google Meet tile redraws and duplicated
page representations must not create false rename, camera, microphone, or chat
events.

Remote extension distribution and automatic updates are out of scope.

## Evidence

The current Meet DOM exposes a participant's clean display name in
`div[jsname="giiMnc"]`. The participant tile also contains controls whose text
includes values such as `Reframe`, `Backgrounds and effects`, `Pin`, `mic_off`,
and `More options`. Reading the whole tile therefore produces unstable,
control-filled names.

Chat rows expose stable resource IDs through `data-message-id`, for example
`spaces/<space>/messages/<message>`. The same logical row may be observed by
multiple Meet tabs or extension contexts at different observation times.

## Name and Participant Policy

`selectors.js` will read names only from the dedicated name element. Repeated
copies of the same name inside that element will collapse to one value. If the
element is absent or empty, the observation has no usable name; full tile text
will not be used as a fallback.

The tracker will not emit a join until it has a usable name. Once established,
the last valid name remains associated with that participant ID when a later
observation temporarily lacks a name.

Rename events will be removed. A changing display string will update neither
WhatsApp nor the local event feed. Join and leave events remain based on
participant identity and Meet's explicit English leave announcement.

## Camera and Microphone Stability

The tracker will distinguish raw observations from confirmed state. A camera
or microphone transition must remain consistent for at least two seconds
before it becomes confirmed and emits an event. A return to the confirmed
state during that interval cancels the candidate transition.

When an observation starts a candidate transition, `content.js` will schedule
a scan for its two-second deadline. Confirmation therefore does not depend on
the existing three-second fallback interval or on another unrelated DOM
mutation.

Initial participant state is recorded with the join and does not generate
separate camera or microphone events. Temporary missing tiles do not change
confirmed state or imply departure. Self toolbar readings remain authoritative
for the local participant when self identification is available.

## Duplicate Suppression

The extension background will suppress equivalent events received within five
seconds from multiple Meet tabs or content-script contexts. The equivalence key
excludes the local observation timestamp and includes the event type, Meet
code, participant or message ID, name, media state, and chat text. This
cross-context guard applies before notification, generic webhook forwarding,
and bot API queueing.

Chat idempotency will additionally use the stable tuple of event type, meeting,
and Meet message ID without the local observation timestamp. The bot server
will use the same logical identity when creating its persistent outbound job.
Consequently, retries, page refreshes, and another tab observing the same
message resolve to one WhatsApp delivery.

Non-chat state events retain their timestamp in durable idempotency because a
participant can legitimately perform the same transition multiple times.

## Data Flow

1. `content.js` collects participant and chat observations.
2. `selectors.js` returns only clean names and raw media states.
3. `state.js` converts observations into joins, explicit leaves, and confirmed
   two-second media transitions. It never generates rename events.
4. `background.js` applies the short cross-context duplicate guard.
5. The existing persistent extension outbox submits accepted events.
6. The Go endpoint validates and formats events. Stable chat identity prevents
   duplicate persistent jobs.
7. The existing scheduler delivers jobs only to the configured WhatsApp group.

Authentication, endpoint configuration, generic webhook support, CSV export,
the monitoring banner, retry limits, and WhatsApp message wording remain
unchanged except that rename events are no longer produced.

## Tests

Automated extension tests will verify:

- dedicated-name extraction and repeated-name collapse;
- control text is never accepted as a participant name;
- no rename event is generated;
- transient media observations produce no event;
- a transition confirmed for two seconds produces exactly one event;
- returning to the prior state cancels a pending transition;
- missing tiles do not create state changes;
- equivalent cross-context events are forwarded once; and
- chat keys ignore observation timestamps while non-chat keys retain them.

Go tests will verify that repeated chat events with the same meeting and
message ID but different timestamps create one outbound job, while legitimate
non-chat transitions remain distinct.

The release checks will include JavaScript syntax checks, all extension tests,
`gofmt -d .`, `go vet ./...`, `go test ./...`, `go build ./...`, and
`git diff --check`.

## Acceptance

In a live Meet with at least two participants:

- WhatsApp names contain only participant display names;
- opening tile controls or changing Meet layout creates no rename or camera
  noise;
- a real camera or microphone transition appears once after about two seconds;
- a chat message appears once even when another tab observes the meeting; and
- joins and explicit departures continue to appear once.
