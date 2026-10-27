package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"whatsappBot/utils"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	_ "modernc.org/sqlite"
)

type fakeSender struct {
	mu       sync.Mutex
	messages []*waProto.Message
	extras   []whatsmeow.SendRequestExtra
	err      error
}

func (f *fakeSender) SendMessage(
	_ context.Context,
	_ types.JID,
	message *waProto.Message,
	extra ...whatsmeow.SendRequestExtra,
) (whatsmeow.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	if len(extra) > 0 {
		f.extras = append(f.extras, extra[0])
	}
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
		CREATE TABLE birthday_deliveries (
			birthday_id INTEGER NOT NULL,
			delivery_year INTEGER NOT NULL,
			delivered_at DATETIME NOT NULL,
			PRIMARY KEY (birthday_id, delivery_year)
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

func TestNewRegistryRegistersReminderAndBirthdayProviders(t *testing.T) {
	registry := NewRegistry(openSchedulerDB(t))

	if len(registry.Providers) != 2 {
		t.Fatalf("provider count = %d, want 2", len(registry.Providers))
	}
	if _, ok := registry.Providers[0].(*ReminderProvider); !ok {
		t.Fatalf("first provider type = %T, want *ReminderProvider", registry.Providers[0])
	}
	if _, ok := registry.Providers[1].(*BirthdayProvider); !ok {
		t.Fatalf("second provider type = %T, want *BirthdayProvider", registry.Providers[1])
	}
}

func TestRunOnceSendsBirthdayOnlyOncePerYear(t *testing.T) {
	database := openSchedulerDB(t)
	now := time.Date(2026, time.October, 27, 9, 0, 0, 0, time.UTC)
	if _, err := database.Exec(
		`INSERT INTO birthdays (chat_id, name, birth_date) VALUES (?, ?, ?)`,
		"120363000000000000@g.us", "Alex", "1990-10-27",
	); err != nil {
		t.Fatalf("insert birthday: %v", err)
	}
	sender := &fakeSender{}
	sched := NewScheduler(NewRegistry(database), sender, time.Second)

	if err := sched.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if err := sched.RunOnce(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}

	if got := sender.count(); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
	if len(sender.extras) != 1 || sender.extras[0].ID == "" {
		t.Fatalf("birthday send extra = %#v, want stable message ID", sender.extras)
	}
}

func TestStartStopsSchedulingAfterCancellation(t *testing.T) {
	database := openSchedulerDB(t)
	insertReminder(t, database, time.Now().Add(-time.Minute))
	sender := &fakeSender{}
	sched := NewScheduler(NewRegistry(database), sender, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := sched.Start(ctx)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}

	if got := sender.count(); got != 0 {
		t.Fatalf("sent messages after cancellation = %d, want 0", got)
	}
	assertReminderCount(t, database, 1)
}

func TestStartCompletionWaitsForActiveRunOnce(t *testing.T) {
	task := &blockingTask{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	sched := NewScheduler(
		&Registry{Providers: []TaskProvider{blockingProvider{task: task}}},
		&fakeSender{},
		time.Nanosecond,
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := sched.Start(ctx)

	select {
	case <-task.started:
	case <-time.After(time.Second):
		t.Fatal("scheduled task did not start")
	}
	cancel()

	select {
	case <-done:
		t.Fatal("scheduler completed while RunOnce was active")
	default:
	}

	close(task.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not complete after RunOnce exited")
	}
}

type blockingProvider struct {
	task ScheduledTask
}

func (p blockingProvider) GetDueTasks(context.Context, time.Time) ([]ScheduledTask, error) {
	return []ScheduledTask{p.task}, nil
}

type blockingTask struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *blockingTask) Run(context.Context, utils.MessageSender) error {
	t.once.Do(func() { close(t.started) })
	<-t.release
	return nil
}

func (t *blockingTask) Time() time.Time {
	return time.Time{}
}

func (t *blockingTask) ID() string {
	return "blocking"
}
