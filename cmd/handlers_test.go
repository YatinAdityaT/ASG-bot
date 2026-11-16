package cmd

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "whatsappBot/db"
	"whatsappBot/identity"
	"whatsappBot/meetmonitor"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type fakeStatusProvider struct {
	status meetmonitor.Status
	err    error
	calls  int
}

type fakeMeetMessenger struct {
	messages []string
	err      error
}

type fakeToneCatalog struct {
	lines []string
}

func (f fakeToneCatalog) ToneList() []string {
	return append([]string(nil), f.lines...)
}

func (f *fakeMeetMessenger) QueueMessage(_ context.Context, text string) (meetmonitor.Message, error) {
	if f.err != nil {
		return meetmonitor.Message{}, f.err
	}
	f.messages = append(f.messages, text)
	return meetmonitor.Message{ID: "message-1", Text: text}, nil
}

func TestToneListUsesLoadedCatalog(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithToneCatalog(fakeToneCatalog{lines: []string{
			"/a — affectionate",
			"/j — joking",
		}}),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/tonelist")); err != nil {
		t.Fatal(err)
	}
	if got, want := sentText(t, sender), "Tone indicators:\n/a — affectionate\n/j — joking"; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

type fakeCallProvider struct {
	roster meetmonitor.CallRoster
	err    error
}

func (f *fakeCallProvider) CallRoster(context.Context) (meetmonitor.CallRoster, error) {
	return f.roster, f.err
}

type fakeGroupSender struct {
	fakeSender
	groupInfo *types.GroupInfo
	groupErr  error
}

func (f *fakeGroupSender) GetGroupInfo(context.Context, types.JID) (*types.GroupInfo, error) {
	return f.groupInfo, f.groupErr
}

func (f *fakeStatusProvider) Status(context.Context) (meetmonitor.Status, error) {
	f.calls++
	if f.err != nil {
		return meetmonitor.Status{}, f.err
	}
	return f.status, nil
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	_, conn, err := appdb.SetupDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestIdentityMapAdminAddsMentionedMember(t *testing.T) {
	conn := openTestDB(t)
	sender := &fakeSender{}
	manager := NewCommandManager(sender, []string{"456"}, nil)
	recipient := types.NewJID("789", types.DefaultUserServer)
	event := withMentions(
		messageEvent(`/map add @Aditi aditi | Aditi Sharma | Aditi`),
		"Yatin",
		recipient.String(),
	)

	if err := manager.RouteMessage(conn, context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var username, name, jid, alias string
	if err := conn.QueryRow(`
		SELECT p.username, p.canonical_name, p.whatsapp_jid, a.alias
		FROM people p
		JOIN meet_aliases a ON a.person_id = p.id
	`).Scan(&username, &name, &jid, &alias); err != nil {
		t.Fatal(err)
	}
	if username != "aditi" || name != "Aditi Sharma" ||
		jid != recipient.String() || alias != "Aditi" {
		t.Fatalf("mapping = %q, %q, %q, %q", username, name, jid, alias)
	}
	if got := sentText(t, sender); !strings.Contains(got, "Mapped @aditi") {
		t.Fatalf("reply = %q", got)
	}
}

func TestIdentityMapListShowsDetailedMappings(t *testing.T) {
	conn := openTestDB(t)
	if _, err := identity.NewStore(conn).Add(
		context.Background(),
		"aarti",
		"Aarti",
		"919945335823@s.whatsapp.net",
		"Aarti Iyer",
	); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	manager := NewCommandManager(sender, []string{"456"}, nil)

	if err := manager.RouteMessage(conn, context.Background(), messageEvent("/map list")); err != nil {
		t.Fatal(err)
	}

	want := "Identity mappings (1):\n\n@aarti\nName: Aarti\nWhatsApp: 919945335823@s.whatsapp.net\nMeet: Aarti Iyer"
	if got := sentText(t, sender); got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

func TestReminderCreateForMentionedRecipientStoresRequesterAndAttribution(t *testing.T) {
	conn := openTestDB(t)
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, nil)
	recipient := types.NewJID("789", types.DefaultUserServer)
	event := withMentions(
		messageEvent(`/remind @Alex "Submit the form" tomorrow at 3pm`),
		"Yatin",
		recipient.String(),
	)

	if err := manager.RouteMessage(conn, context.Background(), event); err != nil {
		t.Fatalf("create reminder: %v", err)
	}

	var chatID, toID, requestedBy, message string
	if err := conn.QueryRow(
		"SELECT chat_id, to_id, requested_by, message FROM reminders",
	).Scan(&chatID, &toID, &requestedBy, &message); err != nil {
		t.Fatalf("query created reminder: %v", err)
	}
	if chatID != "123@g.us" {
		t.Errorf("chat_id = %q, want current chat", chatID)
	}
	if toID != recipient.String() {
		t.Errorf("to_id = %q, want mentioned recipient", toID)
	}
	if requestedBy != "456@s.whatsapp.net" {
		t.Errorf("requested_by = %q, want requesting sender", requestedBy)
	}
	if want := `@789 Yatin asked me to remind you to "Submit the form" at 3:00 PM`; message != want {
		t.Errorf("message = %q, want %q", message, want)
	}
	if got := sentText(t, sender); !strings.Contains(got, `Reminder set`) {
		t.Fatalf("confirmation = %q, want reminder set", got)
	}
}

func TestReminderCreateForSelfUsesSelfAttribution(t *testing.T) {
	conn := openTestDB(t)
	sender := &fakeSender{}

	if err := NewCommandManager(sender, nil, nil).RouteMessage(
		conn,
		context.Background(),
		messageEvent(`/remind me to Stretch in 30 minutes`),
	); err != nil {
		t.Fatalf("create self reminder: %v", err)
	}

	var toID, requestedBy, message string
	if err := conn.QueryRow(
		"SELECT to_id, requested_by, message FROM reminders",
	).Scan(&toID, &requestedBy, &message); err != nil {
		t.Fatal(err)
	}
	if toID != "456@s.whatsapp.net" || requestedBy != "456@s.whatsapp.net" {
		t.Errorf("to_id/requested_by = %q/%q, want requester JID", toID, requestedBy)
	}
	if !strings.HasPrefix(message, `@456 you asked me to remind you to "Stretch" at `) {
		t.Errorf("message = %q, want reminder delivery time", message)
	}
}

func TestReminderCreateStoresRecurringSeries(t *testing.T) {
	conn := openTestDB(t)
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, nil)

	if err := manager.RouteMessage(
		conn,
		context.Background(),
		messageEvent(`/remind me to eat every day at 2pm`),
	); err != nil {
		t.Fatal(err)
	}

	var body, kind string
	var weekday sql.NullInt64
	var active int
	if err := conn.QueryRow(`
		SELECT body, recurrence_kind, recurrence_weekday, active
		FROM reminders
	`).Scan(&body, &kind, &weekday, &active); err != nil {
		t.Fatal(err)
	}
	if body != "eat" || kind != "daily" || weekday.Valid || active != 1 {
		t.Fatalf("series = body %q kind %q weekday %#v active %d", body, kind, weekday, active)
	}
	if got := sentText(t, sender); !strings.Contains(got, "Repeating reminder set") ||
		!strings.Contains(got, "(every day)") {
		t.Fatalf("confirmation = %q", got)
	}
}

func TestReminderCreateFallsBackToRequesterJIDUser(t *testing.T) {
	conn := openTestDB(t)
	recipient := types.NewJID("789", types.DefaultUserServer)
	event := withMentions(
		messageEvent(`/remind @Alex "Submit the form" tomorrow at 3pm`),
		"",
		recipient.String(),
	)

	if err := NewCommandManager(&fakeSender{}, nil, nil).RouteMessage(
		conn,
		context.Background(),
		event,
	); err != nil {
		t.Fatalf("create reminder: %v", err)
	}

	var message string
	if err := conn.QueryRow("SELECT message FROM reminders").Scan(&message); err != nil {
		t.Fatal(err)
	}
	if want := `@789 456 asked me to remind you to "Submit the form" at 3:00 PM`; message != want {
		t.Fatalf("message = %q, want JID-user fallback %q", message, want)
	}
}

func TestReminderRejectsMentionMetadataErrorBeforePersistence(t *testing.T) {
	conn := openTestDB(t)
	sender := &fakeSender{}
	event := withMentions(
		messageEvent(`/remind @Alex "Submit the form" tomorrow at 3pm`),
		"Yatin",
		types.NewJID("789", types.DefaultUserServer).String(),
		"not-a-jid",
	)

	if err := NewCommandManager(sender, nil, nil).RouteMessage(conn, context.Background(), event); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	var count int
	if err := conn.QueryRow("SELECT COUNT(*) FROM reminders").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("persisted %d reminders with malformed mention metadata, want 0", count)
	}
	reply := sentText(t, sender)
	if !strings.Contains(reply, `/remind @Alex`) || !strings.Contains(reply, `/remind me`) {
		t.Fatalf("reply = %q, want concise reminder examples", reply)
	}
}

func TestReminderListIsScopedToRequesterAndChat(t *testing.T) {
	conn := openTestDB(t)
	if _, err := conn.Exec(`
		INSERT INTO reminders (chat_id, to_id, requested_by, message, remind_at)
		VALUES
			('123@g.us', '789@s.whatsapp.net', '456@s.whatsapp.net', 'owned', '2030-01-01 12:00:00'),
			('123@g.us', '456@s.whatsapp.net', '789@s.whatsapp.net', 'recipient-owned', '2030-01-01 13:00:00'),
			('999@g.us', '789@s.whatsapp.net', '456@s.whatsapp.net', 'other-chat', '2030-01-01 14:00:00')
	`); err != nil {
		t.Fatal(err)
	}

	other := messageEvent("/rlist")
	other.Info.Sender.User = "789"
	otherSender := &fakeSender{}
	if err := NewCommandManager(otherSender, nil, nil).RouteMessage(conn, context.Background(), other); err != nil {
		t.Fatalf("list other sender reminders: %v", err)
	}
	if got := sentText(t, otherSender); !strings.Contains(got, "[ID: 2]") || strings.Contains(got, "[ID: 1]") {
		t.Fatalf("recipient list = %q, want only reminders requested by recipient", got)
	}

	listSender := &fakeSender{}
	if err := NewCommandManager(listSender, nil, nil).RouteMessage(conn, context.Background(), messageEvent("/rlist")); err != nil {
		t.Fatalf("list owner reminders: %v", err)
	}
	if got := sentText(t, listSender); !strings.Contains(got, "[ID: 1]") || strings.Contains(got, "[ID: 3]") {
		t.Fatalf("owner list = %q, want only current-chat reminders requested by owner", got)
	}
}

func TestReminderUpdateRequiresRequesterOwnershipAndPreservesTarget(t *testing.T) {
	for _, test := range []struct {
		name         string
		sender       string
		wantUpdated  bool
		wantRowCount int
	}{
		{name: "owner can update", sender: "456", wantUpdated: true, wantRowCount: 1},
		{name: "other sender cannot update", sender: "789", wantUpdated: false, wantRowCount: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := openTestDB(t)
			result, err := conn.Exec(
				"INSERT INTO reminders (chat_id, to_id, requested_by, message, remind_at) VALUES (?, ?, ?, ?, ?)",
				"123@g.us", "789@s.whatsapp.net", "456@s.whatsapp.net", "original", "2030-01-01 12:00:00",
			)
			if err != nil {
				t.Fatal(err)
			}
			id, err := result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}

			event := messageEvent("/rupdate 1 tomorrow at 4pm updated")
			event.Info.Sender.User = test.sender
			event.Info.PushName = "Yatin"
			sender := &fakeSender{}
			if err := NewCommandManager(sender, nil, nil).RouteMessage(conn, context.Background(), event); err != nil {
				t.Fatalf("update reminder: %v", err)
			}

			var toID, message string
			if err := conn.QueryRow("SELECT to_id, message FROM reminders WHERE id = ?", id).Scan(&toID, &message); err != nil {
				t.Fatal(err)
			}
			if toID != "789@s.whatsapp.net" {
				t.Fatalf("to_id = %q, want original recipient preserved", toID)
			}
			updated := strings.Contains(message, "updated")
			if updated != test.wantUpdated {
				t.Fatalf("updated = %v, want %v; message = %q", updated, test.wantUpdated, message)
			}
			var count int
			if err := conn.QueryRow("SELECT COUNT(*) FROM reminders").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != test.wantRowCount {
				t.Fatalf("reminder count = %d, want %d", count, test.wantRowCount)
			}
			if test.wantUpdated {
				if want := `@789 Yatin asked me to remind you to "updated" at 4:00 PM`; message != want {
					t.Fatalf("updated message = %q, want %q", message, want)
				}
			}
		})
	}
}

func TestReminderDeleteRequiresSenderOwnership(t *testing.T) {
	for _, test := range []struct {
		name       string
		sender     string
		wantActive int
	}{
		{name: "owner can delete", sender: "456", wantActive: 0},
		{name: "other sender cannot delete", sender: "789", wantActive: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := openTestDB(t)
			if _, err := conn.Exec(
				"INSERT INTO reminders (chat_id, to_id, requested_by, message, remind_at) VALUES (?, ?, ?, ?, ?)",
				"123@g.us", "789@s.whatsapp.net", "456@s.whatsapp.net", "original", "2030-01-01 12:00:00",
			); err != nil {
				t.Fatal(err)
			}

			event := messageEvent("/rdelete 1")
			event.Info.Sender.User = test.sender
			if err := NewCommandManager(&fakeSender{}, nil, nil).RouteMessage(conn, context.Background(), event); err != nil {
				t.Fatalf("delete reminder: %v", err)
			}

			var active int
			if err := conn.QueryRow("SELECT active FROM reminders").Scan(&active); err != nil {
				t.Fatal(err)
			}
			if active != test.wantActive {
				t.Fatalf("reminder active = %d, want %d", active, test.wantActive)
			}
		})
	}
}

func TestReminderUpdateAndDeleteDenyCrossChatRequester(t *testing.T) {
	for _, command := range []string{
		"/rupdate 1 tomorrow at 4pm updated",
		"/rdelete 1",
	} {
		t.Run(command, func(t *testing.T) {
			conn := openTestDB(t)
			if _, err := conn.Exec(
				"INSERT INTO reminders (chat_id, to_id, requested_by, message, remind_at) VALUES (?, ?, ?, ?, ?)",
				"999@g.us", "789@s.whatsapp.net", "456@s.whatsapp.net", "original", "2030-01-01 12:00:00",
			); err != nil {
				t.Fatal(err)
			}

			if err := NewCommandManager(&fakeSender{}, nil, nil).RouteMessage(
				conn,
				context.Background(),
				messageEvent(command),
			); err != nil {
				t.Fatalf("RouteMessage() error = %v", err)
			}
			var message string
			if err := conn.QueryRow("SELECT message FROM reminders WHERE id = 1").Scan(&message); err != nil {
				t.Fatal(err)
			}
			if message != "original" {
				t.Fatalf("cross-chat command changed message to %q", message)
			}
		})
	}
}

func TestReminderSuccessReplyReturnsSendErrorAfterPersistence(t *testing.T) {
	conn := openTestDB(t)
	sendErr := errors.New("confirmation failed")
	sender := &fakeSender{err: sendErr}

	err := NewCommandManager(sender, nil, nil).RouteMessage(
		conn,
		context.Background(),
		messageEvent(`/remind me to stretch in 30 minutes`),
	)

	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
	}
	var count int
	if err := conn.QueryRow("SELECT COUNT(*) FROM reminders").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted reminder count = %d, want 1", count)
	}
}

func TestReminderManagementRepliesReturnSendErrors(t *testing.T) {
	for _, command := range []string{
		"/rlist",
		"/rupdate 999 tomorrow at 4pm updated",
		"/rdelete 999",
	} {
		t.Run(command, func(t *testing.T) {
			conn := openTestDB(t)
			sendErr := errors.New("reply failed")
			err := NewCommandManager(&fakeSender{err: sendErr}, nil, nil).RouteMessage(
				conn,
				context.Background(),
				messageEvent(command),
			)
			if !errors.Is(err, sendErr) {
				t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
			}
		})
	}
}

func TestInvalidReminderTimeReturnsReplyError(t *testing.T) {
	sendErr := errors.New("send failed")
	sender := &fakeSender{err: sendErr}
	manager := NewCommandManager(sender, nil, nil)

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/remind not a reminder time"))

	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
	}
}

func withMentions(evt *events.Message, pushName string, mentionedJIDs ...string) *events.Message {
	evt.Info.PushName = pushName
	text := evt.Message.GetConversation()
	evt.Message = &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: &text,
			ContextInfo: &waProto.ContextInfo{
				MentionedJID: mentionedJIDs,
			},
		},
	}
	return evt
}

func withQuotedMessage(evt *events.Message, stanzaID string) *events.Message {
	text := evt.Message.GetConversation()
	evt.Message = &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: &text,
			ContextInfo: &waProto.ContextInfo{
				StanzaID: &stanzaID,
			},
		},
	}
	return evt
}

func TestSnoozeQuotedRecurringReminderShiftsSeriesOnce(t *testing.T) {
	conn := openTestDB(t)
	now := time.Date(2026, time.November, 16, 14, 5, 0, 0, time.Local)
	result, err := conn.Exec(`
		INSERT INTO reminders (
			chat_id, to_id, requested_by, message, remind_at, body,
			requester_name, recurrence_kind
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "123@g.us", "789@s.whatsapp.net", "456@s.whatsapp.net",
		"old", now.Add(24*time.Hour), "eat", "Yatin", "daily")
	if err != nil {
		t.Fatal(err)
	}
	reminderID, _ := result.LastInsertId()
	if _, err := conn.Exec(`
		INSERT INTO reminder_deliveries (
			message_id, source_type, reminder_id, chat_id, requester_jid,
			target_jid, scheduled_for, delivered_at, created_at
		) VALUES (?, 'whatsapp', ?, ?, ?, ?, ?, ?, ?)
	`, "delivery-1", reminderID, "123@g.us", "456@s.whatsapp.net",
		"789@s.whatsapp.net", now.Add(-5*time.Minute).UTC().UnixNano(),
		now.UTC().UnixNano(), now.UTC().UnixNano()); err != nil {
		t.Fatal(err)
	}

	manager := NewCommandManager(
		&fakeSender{}, nil, nil, WithClock(func() time.Time { return now }),
	)
	sender := &fakeSender{}
	manager.client = sender
	event := withQuotedMessage(messageEvent("/snooze 30 minutes"), "delivery-1")
	if err := manager.RouteMessage(conn, context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var next time.Time
	var active int
	if err := conn.QueryRow(
		"SELECT remind_at, active FROM reminders WHERE id = ?", reminderID,
	).Scan(&next, &active); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(30 * time.Minute); !next.Equal(want) || active != 1 {
		t.Fatalf("next/active = %v/%d, want %v/1", next, active, want)
	}
	if got := sentText(t, sender); !strings.Contains(got, "every day") {
		t.Fatalf("reply = %q", got)
	}

	sender.messages = nil
	if err := manager.RouteMessage(conn, context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if got := sentText(t, sender); !strings.Contains(got, "already been used") {
		t.Fatalf("second reply = %q", got)
	}
}

func TestReminderReplyRejectsUnauthorizedSender(t *testing.T) {
	conn := openTestDB(t)
	now := time.Date(2026, time.November, 16, 14, 5, 0, 0, time.Local)
	result, err := conn.Exec(`
		INSERT INTO reminders (chat_id, to_id, requested_by, message, remind_at, active)
		VALUES (?, ?, ?, ?, ?, 0)
	`, "123@g.us", "789@s.whatsapp.net", "456@s.whatsapp.net",
		"old", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reminderID, _ := result.LastInsertId()
	if _, err := conn.Exec(`
		INSERT INTO reminder_deliveries (
			message_id, source_type, reminder_id, chat_id, requester_jid,
			target_jid, scheduled_for, delivered_at, created_at
		) VALUES (?, 'whatsapp', ?, ?, ?, ?, ?, ?, ?)
	`, "delivery-2", reminderID, "123@g.us", "456@s.whatsapp.net",
		"789@s.whatsapp.net", now.Add(-time.Minute).UTC().UnixNano(),
		now.UTC().UnixNano(), now.UTC().UnixNano()); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{}
	manager := NewCommandManager(
		sender, nil, nil, WithClock(func() time.Time { return now }),
	)
	event := withQuotedMessage(
		messageEvent("/reschedule tomorrow at 3pm"), "delivery-2",
	)
	event.Info.Sender.User = "999"
	if err := manager.RouteMessage(conn, context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if got := sentText(t, sender); !strings.Contains(got, "requester or recipient") {
		t.Fatalf("reply = %q", got)
	}
	var actedAt sql.NullInt64
	if err := conn.QueryRow(
		"SELECT acted_at FROM reminder_deliveries WHERE message_id = 'delivery-2'",
	).Scan(&actedAt); err != nil {
		t.Fatal(err)
	}
	if actedAt.Valid {
		t.Fatal("unauthorized reply consumed delivery reference")
	}
}

func TestRescheduleQuotedMeetReminderReactivatesSeries(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, time.November, 16, 14, 5, 0, 0, time.Local)
	identities := identity.NewStore(conn)
	requester, err := identities.Add(
		ctx, "yatin", "Yatin", "456@s.whatsapp.net", "Yatin T",
	)
	if err != nil {
		t.Fatal(err)
	}
	target, err := identities.Add(
		ctx, "disha", "Disha", "789@s.whatsapp.net", "Disha",
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := conn.Exec(`
		INSERT INTO meet_reminders (
			source_key, meeting_code, requester_person_id, target_person_id,
			whatsapp_chat_id, body, remind_at, whatsapp_delivered_at,
			meet_status, completed_at, created_at, updated_at,
			recurrence_kind, recurrence_weekday
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?, ?, ?, 'weekly', ?)
	`, "event-1", "jpy-ymgz-tdo", requester.ID, target.ID, "123@g.us",
		"check in", now.UTC().UnixNano(), now.UTC().UnixNano(),
		now.UTC().UnixNano(), now.UTC().UnixNano(), now.UTC().UnixNano(),
		int(time.Monday))
	if err != nil {
		t.Fatal(err)
	}
	reminderID, _ := result.LastInsertId()
	if _, err := conn.Exec(`
		INSERT INTO reminder_deliveries (
			message_id, source_type, reminder_id, chat_id, requester_jid,
			target_jid, scheduled_for, delivered_at, created_at
		) VALUES (?, 'meet', ?, ?, ?, ?, ?, ?, ?)
	`, "delivery-3", reminderID, "123@g.us", "456@s.whatsapp.net",
		"789@s.whatsapp.net", now.UTC().UnixNano(),
		now.UTC().UnixNano(), now.UTC().UnixNano()); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{}
	manager := NewCommandManager(
		sender, nil, nil, WithClock(func() time.Time { return now }),
	)
	event := withQuotedMessage(
		messageEvent("/reschedule tomorrow at 3pm"), "delivery-3",
	)
	event.Info.Sender.User = "789"
	if err := manager.RouteMessage(conn, ctx, event); err != nil {
		t.Fatal(err)
	}
	var (
		remindAt          int64
		whatsAppDelivered sql.NullInt64
		completedAt       sql.NullInt64
		status            string
		weekday           int
	)
	if err := conn.QueryRow(`
		SELECT remind_at, whatsapp_delivered_at, meet_status, completed_at,
		       recurrence_weekday
		FROM meet_reminders WHERE id = ?
	`, reminderID).Scan(
		&remindAt, &whatsAppDelivered, &status, &completedAt, &weekday,
	); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.November, 17, 15, 0, 0, 0, time.Local)
	if time.Unix(0, remindAt).UTC() != want.UTC() ||
		whatsAppDelivered.Valid || completedAt.Valid || status != "pending" ||
		time.Weekday(weekday) != time.Tuesday {
		t.Fatalf(
			"row = %v/%v/%q/%v/%v",
			time.Unix(0, remindAt), whatsAppDelivered, status, completedAt,
			time.Weekday(weekday),
		)
	}
	if got := sentText(t, sender); !strings.Contains(got, "every Tuesday") {
		t.Fatalf("reply = %q", got)
	}
}

func TestReminderHelpDocumentsMentionedReminderRules(t *testing.T) {
	sender := &fakeSender{}
	if err := NewCommandManager(sender, nil, nil).RouteMessage(
		nil,
		context.Background(),
		messageEvent("/help"),
	); err != nil {
		t.Fatalf("help: %v", err)
	}

	help := sentText(t, sender)
	for _, required := range []string{
		`/remind @Alex "submit the form" tomorrow at 3pm`,
		`/remind tomorrow at 3pm @Alex "submit the form"`,
		`/remind @Alex in 20 minutes to join the call`,
		`/remind me to stretch in 30 minutes`,
		`/remind in 30 minutes "stretch"`,
		"one mentioned recipient",
		"Without a mention, the reminder is for you",
		"Only the requester can list, update, or delete",
		"Ambiguous or malformed reminders are rejected",
	} {
		if !strings.Contains(help, required) {
			t.Errorf("help reply missing %q", required)
		}
	}
}

func TestStatusOnlineResponseExact(t *testing.T) {
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	heartbeat := now.Add(-12 * time.Second)
	provider := &fakeStatusProvider{
		status: meetmonitor.Status{
			BotOnline:         true,
			WhatsAppConnected: true,
			MonitorState:      "online",
			LastHeartbeatAt:   &heartbeat,
			Meeting:           "jpy-ymgz-tdo",
			Participants:      7,
			ForwardingEnabled: true,
			ExtensionVersion:  "1.4.0",
		},
	}
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(provider),
		WithClock(func() time.Time { return now }),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/status")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}

	want := strings.Join([]string{
		"Bot: online",
		"Started: 3 Nov 2026, 12:00 PM UTC",
		"Uptime: 0s",
		"WhatsApp: connected",
		"Monitor: online",
		"Last heartbeat: 12s ago",
		"Meeting: jpy-ymgz-tdo",
		"Participants: 7",
		"Forwarding: ON",
		"Extension: 1.4.0",
	}, "\n")
	if got := sentText(t, sender); got != want {
		t.Fatalf("status reply = %q, want %q", got, want)
	}
}

func TestStatusNeverSeenResponseExact(t *testing.T) {
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	provider := &fakeStatusProvider{
		status: meetmonitor.Status{
			BotOnline:         true,
			WhatsAppConnected: false,
			MonitorState:      "never_seen",
			Meeting:           "jpy-ymgz-tdo",
		},
	}
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(provider),
		WithClock(func() time.Time { return now }),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/status")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}

	want := strings.Join([]string{
		"Bot: online",
		"Started: 3 Nov 2026, 12:00 PM UTC",
		"Uptime: 0s",
		"WhatsApp: disconnected",
		"Monitor: never seen",
		"Last heartbeat: never",
		"Meeting: jpy-ymgz-tdo",
		"Participants: 0",
		"Forwarding: OFF",
		"Extension: unknown",
	}, "\n")
	if got := sentText(t, sender); got != want {
		t.Fatalf("status reply = %q, want %q", got, want)
	}
}

func TestStatusOfflineResponseFormatsMinutesAndForwardingOff(t *testing.T) {
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	heartbeat := now.Add(-5*time.Minute - 45*time.Second)
	provider := &fakeStatusProvider{
		status: meetmonitor.Status{
			BotOnline:         false,
			WhatsAppConnected: true,
			MonitorState:      "offline",
			LastHeartbeatAt:   &heartbeat,
			Meeting:           "jpy-ymgz-tdo",
			Participants:      3,
			ForwardingEnabled: false,
			ExtensionVersion:  "2.0.0",
		},
	}
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(provider),
		WithClock(func() time.Time { return now }),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/status")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}

	want := strings.Join([]string{
		"Bot: offline",
		"Started: 3 Nov 2026, 12:00 PM UTC",
		"Uptime: 0s",
		"WhatsApp: connected",
		"Monitor: offline",
		"Last heartbeat: 5m ago",
		"Meeting: jpy-ymgz-tdo",
		"Participants: 3",
		"Forwarding: OFF",
		"Extension: 2.0.0",
	}, "\n")
	if got := sentText(t, sender); got != want {
		t.Fatalf("status reply = %q, want %q", got, want)
	}
}

func TestStatusFormatsSecondsHoursAndFutureHeartbeat(t *testing.T) {
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		heartbeat time.Time
		want      string
	}{
		{name: "seconds", heartbeat: now.Add(-45 * time.Second), want: "Last heartbeat: 45s ago"},
		{name: "hours", heartbeat: now.Add(-2*time.Hour - 59*time.Minute), want: "Last heartbeat: 2h ago"},
		{name: "future clamped", heartbeat: now.Add(30 * time.Second), want: "Last heartbeat: 0s ago"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sender := &fakeSender{}
			manager := NewCommandManager(
				sender,
				nil,
				nil,
				WithStatusProvider(&fakeStatusProvider{
					status: meetmonitor.Status{
						BotOnline:         true,
						WhatsAppConnected: true,
						MonitorState:      "online",
						LastHeartbeatAt:   &test.heartbeat,
						Meeting:           "jpy-ymgz-tdo",
						Participants:      1,
						ForwardingEnabled: true,
						ExtensionVersion:  "1.0.0",
					},
				}),
				WithClock(func() time.Time { return now }),
			)

			if err := manager.RouteMessage(nil, context.Background(), messageEvent("/status")); err != nil {
				t.Fatalf("RouteMessage() error = %v", err)
			}
			if got := sentText(t, sender); !strings.Contains(got, test.want) {
				t.Fatalf("status reply = %q, want substring %q", got, test.want)
			}
		})
	}
}

func TestStatusDisconnectedWhatsApp(t *testing.T) {
	now := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	heartbeat := now.Add(-12 * time.Second)
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(&fakeStatusProvider{
			status: meetmonitor.Status{
				BotOnline:         true,
				WhatsAppConnected: false,
				MonitorState:      "online",
				LastHeartbeatAt:   &heartbeat,
				Meeting:           "jpy-ymgz-tdo",
				Participants:      7,
				ForwardingEnabled: true,
				ExtensionVersion:  "1.4.0",
			},
		}),
		WithClock(func() time.Time { return now }),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/status")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	if got := sentText(t, sender); !strings.Contains(got, "WhatsApp: disconnected") {
		t.Fatalf("status reply = %q, want disconnected WhatsApp", got)
	}
}

func TestStatusNilHeartbeatFallback(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(&fakeStatusProvider{
			status: meetmonitor.Status{
				BotOnline:         true,
				WhatsAppConnected: true,
				MonitorState:      "online",
				LastHeartbeatAt:   nil,
				Meeting:           "jpy-ymgz-tdo",
				Participants:      1,
				ForwardingEnabled: true,
				ExtensionVersion:  "1.0.0",
			},
		}),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/status")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	if got := sentText(t, sender); !strings.Contains(got, "Last heartbeat: unknown") {
		t.Fatalf("status reply = %q, want unknown heartbeat", got)
	}
}

func TestStatusReturnsProviderErrorWithoutReply(t *testing.T) {
	providerErr := errors.New("status failed")
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(&fakeStatusProvider{err: providerErr}),
	)

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/status"))

	if !errors.Is(err, providerErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, providerErr)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent %d messages, want none", len(sender.messages))
	}
}

func TestStatusReturnsReplySendError(t *testing.T) {
	sendErr := errors.New("status send failed")
	sender := &fakeSender{err: sendErr}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(&fakeStatusProvider{
			status: meetmonitor.Status{
				BotOnline:         true,
				WhatsAppConnected: true,
				MonitorState:      "never_seen",
				Meeting:           "jpy-ymgz-tdo",
			},
		}),
	)

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/status"))

	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
	}
}

func TestStatusMatchesCaseInsensitively(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(&fakeStatusProvider{
			status: meetmonitor.Status{
				BotOnline:         true,
				WhatsAppConnected: false,
				MonitorState:      "never_seen",
				Meeting:           "jpy-ymgz-tdo",
			},
		}),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/STATUS")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	if got := sentText(t, sender); !strings.Contains(got, "Monitor: never seen") {
		t.Fatalf("status reply = %q, want never seen monitor state", got)
	}
}

func TestStatusAllowsRegularUsers(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		[]string{"999"},
		nil,
		WithStatusProvider(&fakeStatusProvider{
			status: meetmonitor.Status{
				BotOnline:         true,
				WhatsAppConnected: false,
				MonitorState:      "never_seen",
				Meeting:           "jpy-ymgz-tdo",
			},
		}),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/status")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	if got := sentText(t, sender); !strings.Contains(got, "Bot: online") {
		t.Fatalf("status reply = %q, want regular user response", got)
	}
}

func TestHelpIncludesStatusWhenProviderExists(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithStatusProvider(&fakeStatusProvider{
			status: meetmonitor.Status{
				BotOnline:         true,
				WhatsAppConnected: false,
				MonitorState:      "never_seen",
				Meeting:           "jpy-ymgz-tdo",
			},
		}),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/help")); err != nil {
		t.Fatalf("help: %v", err)
	}
	if got := sentText(t, sender); !strings.Contains(got, "`/status`") {
		t.Fatalf("help reply = %q, want /status", got)
	}
}

func TestManagerWithoutStatusProviderDoesNotRegisterOrAdvertiseStatus(t *testing.T) {
	manager := NewCommandManager(&fakeSender{}, nil, nil)
	if _, ok := manager.commands["/status"]; ok {
		t.Fatal("registered /status without provider")
	}

	sender := &fakeSender{}
	if err := NewCommandManager(sender, nil, nil).RouteMessage(nil, context.Background(), messageEvent("/help")); err != nil {
		t.Fatalf("help: %v", err)
	}
	if got := sentText(t, sender); strings.Contains(got, "`/status`") {
		t.Fatalf("help reply = %q, unexpectedly advertised /status", got)
	}
}

func TestDieShutsDownAfterGoodbyeIsSent(t *testing.T) {
	sender := &fakeSender{}
	shutdownCalled := false
	manager := NewCommandManager(sender, []string{"456"}, func() {
		shutdownCalled = true
	})

	manager.RouteMessage(nil, context.Background(), messageEvent("/die"))

	if got := sentText(t, sender); got != "Goodbye cruel world!" {
		t.Fatalf("die reply = %q, want %q", got, "Goodbye cruel world!")
	}
	if !shutdownCalled {
		t.Fatal("shutdown was not called after successful goodbye")
	}
}

func TestMeetCommandQueuesMessageAndRepliesToAdmin(t *testing.T) {
	conn := openTestDB(t)
	if _, err := identity.NewStore(conn).Add(
		context.Background(),
		"yatin",
		"Yatin",
		"456@s.whatsapp.net",
		"Yatin T",
	); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{}
	messenger := &fakeMeetMessenger{}
	manager := NewCommandManager(
		sender,
		[]string{"456"},
		nil,
		WithMeetMessenger(messenger),
	)
	event := messageEvent("/meet Hello everyone")
	event.Info.PushName = "Y"

	if err := manager.RouteMessage(conn, context.Background(), event); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}

	if len(messenger.messages) != 1 || messenger.messages[0] != `"Yatin" said "Hello everyone"` {
		t.Fatalf("queued messages = %#v", messenger.messages)
	}
	if got := sentText(t, sender); got != "Queued for Google Meet." {
		t.Fatalf("reply = %q", got)
	}
}

func TestMeetCommandAllowsRegularUsersAndHasNoCmessageAlias(t *testing.T) {
	sender := &fakeSender{}
	messenger := &fakeMeetMessenger{}
	manager := NewCommandManager(
		sender,
		[]string{"999"},
		nil,
		WithMeetMessenger(messenger),
	)
	event := messageEvent("/meet hello")
	event.Info.PushName = "Member"

	if err := manager.RouteMessage(nil, context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if got := sentText(t, sender); got != "Queued for Google Meet." {
		t.Fatalf("reply = %q", got)
	}
	if len(messenger.messages) != 1 || messenger.messages[0] != `"Member" said "hello"` {
		t.Fatalf("queued messages = %#v", messenger.messages)
	}

	sender.messages = nil
	messenger.messages = nil
	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/cmessage ignored")); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 0 || len(messenger.messages) != 0 {
		t.Fatalf("alias unexpectedly handled: replies=%d queued=%d", len(sender.messages), len(messenger.messages))
	}
}

func TestCallCommandListsCurrentParticipantsAlphabetically(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithCallProvider(&fakeCallProvider{roster: meetmonitor.CallRoster{
			MonitorState: "online",
			Participants: []meetmonitor.Participant{
				{ID: "2", Name: "Yatin T", Cam: true, Mic: "unmuted"},
				{ID: "1", Name: "ADHD SUPPORT GROUP", Cam: false, Mic: "muted", Self: true},
				{ID: "3", Name: "Yatin T", Cam: false, Mic: "muted"},
				{ID: "4", Name: "Alex", Cam: true, Mic: "muted"},
				{ID: "5", Name: "Disha", Cam: false, Mic: "muted"},
			},
		}}),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/incall")); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"In the call (3) (cam on, mic off):",
		"• Alex",
		"• Disha — cam off",
		"• Yatin T — mic on",
	}, "\n")
	if got := sentText(t, sender); got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}

	sender.messages = nil
	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/call")); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 0 {
		t.Fatal("old /call alias unexpectedly handled")
	}
}

func TestCallCommandRefusesToShowStaleRoster(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithCallProvider(&fakeCallProvider{roster: meetmonitor.CallRoster{
			MonitorState: "offline",
			Participants: []meetmonitor.Participant{{ID: "1", Name: "Stale person"}},
		}}),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/incall")); err != nil {
		t.Fatal(err)
	}
	if got := sentText(t, sender); got != "Meet monitor is offline, so I can't verify who is in the call." {
		t.Fatalf("reply = %q", got)
	}
}

func TestPingCallMentionsGroupAndListsCurrentRoster(t *testing.T) {
	sender := &fakeGroupSender{groupInfo: &types.GroupInfo{
		Participants: []types.GroupParticipant{
			{JID: types.NewJID("111", types.DefaultUserServer)},
			{JID: types.NewJID("222", types.DefaultUserServer)},
		},
	}}
	manager := NewCommandManager(
		sender,
		nil,
		nil,
		WithCallProvider(&fakeCallProvider{roster: meetmonitor.CallRoster{
			MonitorState: "online",
			Participants: []meetmonitor.Participant{
				{ID: "2", Name: "Yatin T"},
				{ID: "1", Name: "ADHD SUPPORT GROUP", Self: true},
				{ID: "3", Name: "Yatin T"},
			},
		}}),
	)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/pingcall")); err != nil {
		t.Fatal(err)
	}
	if got := sentText(t, &sender.fakeSender); got !=
		"📣 @111 @222\nThese people are in the call (1): Yatin T" {
		t.Fatalf("reply = %q", got)
	}
	gotMentions := sender.messages[0].GetExtendedTextMessage().
		GetContextInfo().GetMentionedJID()
	wantMentions := []string{"111@s.whatsapp.net", "222@s.whatsapp.net"}
	if strings.Join(gotMentions, ",") != strings.Join(wantMentions, ",") {
		t.Fatalf("mentions = %#v, want %#v", gotMentions, wantMentions)
	}
}

func TestDieDoesNotShutDownWhenGoodbyeFails(t *testing.T) {
	sendErr := errors.New("send failed")
	sender := &fakeSender{err: sendErr}
	shutdownCalled := false
	manager := NewCommandManager(sender, []string{"456"}, func() {
		shutdownCalled = true
	})

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/die"))

	if shutdownCalled {
		t.Fatal("shutdown called after goodbye failed")
	}
	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
	}
}

func TestDieAllowsNilShutdown(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(sender, []string{"456"}, nil)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/die")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
}
