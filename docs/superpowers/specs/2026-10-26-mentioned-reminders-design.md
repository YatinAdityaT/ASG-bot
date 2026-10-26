# Mentioned Reminders Design

## Goal

Allow a group member to schedule a reminder for one mentioned participant using flexible natural-language word order, without adding an LLM dependency.

## Supported Syntax

The parser accepts:

- `/remind @Alex "submit the form" tomorrow at 3pm`
- `/remind tomorrow at 3pm @Alex "submit the form"`
- `/remind @Alex in 20 minutes to join the call`
- `/remind me to stretch in 30 minutes`
- `/remind in 30 minutes "stretch"`

The command keyword is case-insensitive. Reminder text preserves its original capitalization.

## Parsing

The parser uses WhatsApp mention metadata rather than trusting visible `@name` text.

- Zero mentions creates a self-reminder.
- One mention creates a reminder for that participant.
- More than one mention is rejected with concise usage examples.
- If quoted text exists, it is the reminder body.
- Otherwise, the natural-language time match, mention token, and leading filler words (`me`, `to`, `about`) are removed to derive the body.
- A leading `to` or `about` inside a quoted body is also removed so `"to submit the form"` produces `submit the form`.
- Empty bodies, missing times, malformed quotes, fake typed mentions, and ambiguous input are rejected rather than guessed.
- Time parsing continues to use `github.com/olebedev/when`; no message content leaves the machine.

## Attribution and Delivery

The requesting sender's WhatsApp push name is used for attribution, falling back to the sender's phone/JID user when unavailable.

For another participant:

```text
@Alex Yatin asked me to remind you to "submit the form"
```

For a self-reminder:

```text
@Yatin you asked me to remind you to "stretch"
```

The outbound message includes the recipient in WhatsApp's `MentionedJID` metadata.

## Ownership and Storage

The existing `to_id` column becomes the delivery target. A new nullable `requested_by` column records the creator.

On startup:

1. New databases create `requested_by` with the reminders table.
2. Existing databases add the column if absent.
3. Existing rows backfill `requested_by = to_id`, preserving self-reminder ownership.

`/rlist`, `/rupdate`, and `/rdelete` filter by `requested_by`, chat, and reminder ID. A recipient cannot modify another person's reminder. Updating changes the time/body while preserving the original recipient.

## Routing

Command routing will preserve original text for reminder bodies while matching command names case-insensitively. Tone matching can continue using normalized lowercase text.

The command handler receives the event's message info and mention metadata through a small command context instead of reading untrusted display text alone.

## Error Handling

Invalid input replies with examples. Database, parsing, migration, and outbound-send failures remain surfaced through existing error paths.

## Testing

Tests cover:

- Every documented syntax
- Case preservation and case-insensitive command names
- Self and mentioned targets
- Actual mention metadata versus fake typed mentions
- Multiple mentions, missing time, missing body, and malformed quotes
- Delivery text and `MentionedJID`
- Migration/backfill of existing reminders
- Requester-owned list/update/delete behavior
- Preservation of a mentioned target during update

All existing formatting, vet, test, build, and diff gates remain required.
