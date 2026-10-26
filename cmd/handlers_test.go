package cmd

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	appdb "whatsappBot/db"
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

func TestReminderCreateAndListAreScopedToSender(t *testing.T) {
	conn := openTestDB(t)
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, nil)

	if err := manager.RouteMessage(conn, context.Background(), messageEvent("/remind tomorrow at 3pm take a break")); err != nil {
		t.Fatalf("create reminder: %v", err)
	}

	var toID string
	if err := conn.QueryRow("SELECT to_id FROM reminders").Scan(&toID); err != nil {
		t.Fatalf("query created reminder: %v", err)
	}
	if toID != "456@s.whatsapp.net" {
		t.Fatalf("created reminder to_id = %q, want sender JID", toID)
	}

	other := messageEvent("/rlist")
	other.Info.Sender.User = "789"
	otherSender := &fakeSender{}
	if err := NewCommandManager(otherSender, nil, nil).RouteMessage(conn, context.Background(), other); err != nil {
		t.Fatalf("list other sender reminders: %v", err)
	}
	if got := sentText(t, otherSender); got != "📭 No reminders found." {
		t.Fatalf("other sender list = %q, want no reminders", got)
	}

	listSender := &fakeSender{}
	if err := NewCommandManager(listSender, nil, nil).RouteMessage(conn, context.Background(), messageEvent("/rlist")); err != nil {
		t.Fatalf("list owner reminders: %v", err)
	}
	if got := sentText(t, listSender); !strings.Contains(got, "[ID: 1]") {
		t.Fatalf("owner list = %q, want created reminder", got)
	}
}

func TestReminderUpdateRequiresSenderOwnership(t *testing.T) {
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
				"INSERT INTO reminders (chat_id, to_id, message, remind_at) VALUES (?, ?, ?, ?)",
				"123@g.us", "456@s.whatsapp.net", "original", "2030-01-01 12:00:00",
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
			sender := &fakeSender{}
			if err := NewCommandManager(sender, nil, nil).RouteMessage(conn, context.Background(), event); err != nil {
				t.Fatalf("update reminder: %v", err)
			}

			var message string
			if err := conn.QueryRow("SELECT message FROM reminders WHERE id = ?", id).Scan(&message); err != nil {
				t.Fatal(err)
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
				"INSERT INTO reminders (chat_id, to_id, message, remind_at) VALUES (?, ?, ?, ?)",
				"123@g.us", "456@s.whatsapp.net", "original", "2030-01-01 12:00:00",
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

func TestInvalidReminderTimeReturnsReplyError(t *testing.T) {
	sendErr := errors.New("send failed")
	sender := &fakeSender{err: sendErr}
	manager := NewCommandManager(sender, nil, nil)

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/remind not a reminder time"))

	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
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
