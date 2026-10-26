package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	_ "modernc.org/sqlite"
)

type fakeSender struct {
	mu       sync.Mutex
	messages []*waProto.Message
	err      error
}

func (f *fakeSender) SendMessage(
	_ context.Context,
	_ types.JID,
	message *waProto.Message,
	_ ...whatsmeow.SendRequestExtra,
) (whatsmeow.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	return whatsmeow.SendResponse{}, f.err
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

func openSchedulerDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	_, err = database.Exec(`
		CREATE TABLE reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			message TEXT NOT NULL,
			remind_at DATETIME NOT NULL
		);
		CREATE TABLE birthdays (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL,
			name TEXT NOT NULL,
			birth_date DATE NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return database
}

func insertReminder(t *testing.T, database *sql.DB, remindAt time.Time) int64 {
	t.Helper()
	result, err := database.Exec(
		`INSERT INTO reminders (chat_id, to_id, message, remind_at) VALUES (?, ?, ?, ?)`,
		"120363000000000000@g.us",
		"15551234567@s.whatsapp.net",
		"remember this",
		remindAt,
	)
	if err != nil {
		t.Fatalf("insert reminder: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get reminder id: %v", err)
	}
	return id
}

func assertReminderCount(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(`SELECT COUNT(*) FROM reminders`).Scan(&got); err != nil {
		t.Fatalf("count reminders: %v", err)
	}
	if got != want {
		t.Fatalf("reminder count = %d, want %d", got, want)
	}
}

func TestRunOnceSendsDueReminderOnceAndDeletesIt(t *testing.T) {
	database := openSchedulerDB(t)
	now := time.Now().UTC()
	insertReminder(t, database, now.Add(-time.Minute))
	sender := &fakeSender{}
	sched := NewScheduler(NewRegistry(database), sender, time.Second)

	if err := sched.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if err := sched.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}

	if got := sender.count(); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
	assertReminderCount(t, database, 0)
}

func TestNewRegistryRegistersOnlyReminderProvider(t *testing.T) {
	registry := NewRegistry(openSchedulerDB(t))

	if len(registry.Providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(registry.Providers))
	}
	if _, ok := registry.Providers[0].(*ReminderProvider); !ok {
		t.Fatalf("provider type = %T, want *ReminderProvider", registry.Providers[0])
	}
}

func TestStartStopsSchedulingAfterCancellation(t *testing.T) {
	database := openSchedulerDB(t)
	insertReminder(t, database, time.Now().Add(-time.Minute))
	sender := &fakeSender{}
	sched := NewScheduler(NewRegistry(database), sender, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sched.Start(ctx)
	time.Sleep(25 * time.Millisecond)

	if got := sender.count(); got != 0 {
		t.Fatalf("sent messages after cancellation = %d, want 0", got)
	}
	assertReminderCount(t, database, 1)
}
