# WhatsApp Group Exporter Design

## Goal

Create a small standalone Go utility that signs into WhatsApp through a QR code and exports the names and phone numbers of members of `[Focused] Accountability subscribers`.

## Behavior

- Store the isolated whatsmeow session in `.group-exporter/session.db` under the current directory.
- Display a QR code when the session is not authenticated.
- Locate the group by exact name and stop with a clear error if it is missing or if multiple groups have that name.
- Read only the selected group's participant list after group discovery.
- Resolve each participant's best available contact name. A name may be blank when WhatsApp exposes only a phone number.
- Write `focused-accountability-subscribers.csv` in the current directory with `name`, `phone_number`, and `whatsapp_jid` columns.
- Never send messages or modify group membership.

## Error handling

The utility reports login, connection, group lookup, participant lookup, and file-writing failures. It does not silently choose among duplicate group names.

## Verification

Build the standalone command, then run it interactively for QR login and inspect the generated CSV row count and columns.
