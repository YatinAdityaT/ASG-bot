package cmd

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	appdb "whatsappBot/db"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	_, conn, err := appdb.SetupDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
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
	if want := `@789 Yatin asked me to remind you to "Submit the form"`; message != want {
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
	if want := `@456 you asked me to remind you to "Stretch"`; message != want {
		t.Errorf("message = %q, want %q", message, want)
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
	if want := `@789 456 asked me to remind you to "Submit the form"`; message != want {
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
				if want := `@789 Yatin asked me to remind you to "updated"`; message != want {
					t.Fatalf("updated message = %q, want %q", message, want)
				}
			}
		})
	}
}

func TestReminderDeleteRequiresSenderOwnership(t *testing.T) {
	for _, test := range []struct {
		name      string
		sender    string
		wantCount int
	}{
		{name: "owner can delete", sender: "456", wantCount: 0},
		{name: "other sender cannot delete", sender: "789", wantCount: 1},
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

			var count int
			if err := conn.QueryRow("SELECT COUNT(*) FROM reminders").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != test.wantCount {
				t.Fatalf("reminder count = %d, want %d", count, test.wantCount)
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
