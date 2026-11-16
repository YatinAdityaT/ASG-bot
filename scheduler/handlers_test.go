package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func dueReminderTask(t *testing.T) (*ReminderTask, *fakeSender) {
	t.Helper()
	database := openSchedulerDB(t)
	id := insertReminder(t, database, time.Now().Add(-time.Minute))
	tasks, err := NewReminderProvider(database).GetDueTasks(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("get due tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("due task count = %d, want 1", len(tasks))
	}
	task, ok := tasks[0].(*ReminderTask)
	if !ok {
		t.Fatalf("task type = %T, want *ReminderTask", tasks[0])
	}
	if task.ID() != "1" || id != 1 {
		t.Fatalf("task id = %q (insert id %d), want 1", task.ID(), id)
	}
	return task, &fakeSender{}
}

func TestReminderRetainedWhenSendFails(t *testing.T) {
	task, sender := dueReminderTask(t)
	sender.err = errors.New("send failed")

	err := task.Run(context.Background(), sender)

	if err == nil || !strings.Contains(err.Error(), "send failed") {
		t.Fatalf("Run error = %v, want send failure", err)
	}
	assertReminderCount(t, task.db, 1)
}

func TestMentionedReminderDeliversStoredAttributionAndDeactivatesOnce(t *testing.T) {
	database := openSchedulerDB(t)
	const (
		recipient   = "15557654321@s.whatsapp.net"
		attribution = `@15557654321 Yatin asked me to remind you to "submit the form"`
	)
	result, err := database.Exec(
		`INSERT INTO reminders (chat_id, to_id, requested_by, message, remind_at) VALUES (?, ?, ?, ?, ?)`,
		"120363000000000000@g.us",
		recipient,
		"916355442207@s.whatsapp.net",
		attribution,
		time.Now().Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("insert mentioned reminder: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get reminder id: %v", err)
	}

	tasks, err := NewReminderProvider(database).GetDueTasks(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("get due tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("due task count = %d, want 1", len(tasks))
	}
	sender := &fakeSender{}
	if err := tasks[0].Run(context.Background(), sender); err != nil {
		t.Fatalf("run reminder: %v", err)
	}

	if sender.count() != 1 {
		t.Fatalf("sent message count = %d, want 1", sender.count())
	}
	message := sender.messages[0].GetExtendedTextMessage()
	if message.GetText() != attribution {
		t.Errorf("delivered text = %q, want %q", message.GetText(), attribution)
	}
	mentioned := message.GetContextInfo().GetMentionedJID()
	if len(mentioned) != 1 || mentioned[0] != recipient {
		t.Errorf("MentionedJID = %v, want [%q]", mentioned, recipient)
	}
	assertReminderCount(t, database, 1)
	assertActiveReminderCount(t, database, 0)
	var deliveryCount int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM reminder_deliveries WHERE reminder_id = ?",
		id,
	).Scan(&deliveryCount); err != nil {
		t.Fatalf("count reminder deliveries: %v", err)
	}
	if deliveryCount != 1 {
		t.Fatalf("delivery count = %d, want 1", deliveryCount)
	}
}

func TestReminderAdvanceFailureIsReturnedAndRetainsActiveReminder(t *testing.T) {
	task, sender := dueReminderTask(t)
	_, err := task.db.Exec(`
		CREATE TRIGGER prevent_reminder_update
		BEFORE UPDATE ON reminders
		BEGIN
			SELECT RAISE(FAIL, 'advance blocked');
		END;
	`)
	if err != nil {
		t.Fatalf("create update trigger: %v", err)
	}

	err = task.Run(context.Background(), sender)

	if err == nil || !strings.Contains(err.Error(), "advance blocked") {
		t.Fatalf("Run error = %v, want advance failure", err)
	}
	assertReminderCount(t, task.db, 1)
	assertActiveReminderCount(t, task.db, 1)
}

func TestRecurringReminderRecordsDeliveryAndAdvancesSeries(t *testing.T) {
	database := openSchedulerDB(t)
	now := time.Date(2026, time.November, 16, 14, 5, 0, 0, time.Local)
	scheduled := time.Date(2026, time.November, 16, 14, 0, 0, 0, time.Local)
	result, err := database.Exec(`
		INSERT INTO reminders (
			chat_id, to_id, requested_by, message, remind_at, body,
			requester_name, recurrence_kind
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "123@g.us", "456@s.whatsapp.net", "456@s.whatsapp.net",
		`@456 you asked me to remind you to "eat" at 2:00 PM`,
		scheduled, "eat", "Yatin", "daily")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()

	tasks, err := NewReminderProvider(database).GetDueTasks(context.Background(), now)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks = %d, error = %v", len(tasks), err)
	}
	sender := &fakeSender{}
	if err := tasks[0].Run(context.Background(), sender); err != nil {
		t.Fatal(err)
	}

	var next time.Time
	var message string
	var active int
	if err := database.QueryRow(
		"SELECT remind_at, message, active FROM reminders WHERE id = ?",
		id,
	).Scan(&next, &message, &active); err != nil {
		t.Fatal(err)
	}
	wantNext := time.Date(2026, time.November, 17, 14, 0, 0, 0, time.Local)
	if !next.Equal(wantNext) || active != 1 ||
		!strings.Contains(message, "at 2:00 PM") {
		t.Fatalf("next/message/active = %v/%q/%d", next, message, active)
	}
	var deliveries int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM reminder_deliveries WHERE source_type = 'whatsapp' AND reminder_id = ?",
		id,
	).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 || len(sender.extras) != 1 || sender.extras[0].ID == "" {
		t.Fatalf("deliveries/extras = %d/%#v", deliveries, sender.extras)
	}
}
