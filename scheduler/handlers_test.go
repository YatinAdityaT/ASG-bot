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

func TestMentionedReminderDeliversStoredAttributionAndDeletesOnce(t *testing.T) {
	database := openSchedulerDB(t)
	const (
		recipient   = "15557654321@s.whatsapp.net"
		attribution = `@15557654321 Yatin asked me to remind you to "submit the form"`
	)
	if _, err := database.Exec(`
		CREATE TABLE reminder_deletions (reminder_id INTEGER NOT NULL);
		CREATE TRIGGER record_reminder_delete
		AFTER DELETE ON reminders
		BEGIN
			INSERT INTO reminder_deletions (reminder_id) VALUES (OLD.id);
		END;
	`); err != nil {
		t.Fatalf("create deletion audit: %v", err)
	}
	result, err := database.Exec(
		`INSERT INTO reminders (chat_id, to_id, message, remind_at) VALUES (?, ?, ?, ?)`,
		"120363000000000000@g.us",
		recipient,
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
	assertReminderCount(t, database, 0)
	var deletionCount int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM reminder_deletions WHERE reminder_id = ?",
		id,
	).Scan(&deletionCount); err != nil {
		t.Fatalf("count reminder deletions: %v", err)
	}
	if deletionCount != 1 {
		t.Fatalf("deletion count = %d, want 1", deletionCount)
	}
}

func TestReminderCleanupFailureIsReturnedAndRetainsReminder(t *testing.T) {
	task, sender := dueReminderTask(t)
	_, err := task.db.Exec(`
		CREATE TRIGGER prevent_reminder_delete
		BEFORE DELETE ON reminders
		BEGIN
			SELECT RAISE(FAIL, 'cleanup blocked');
		END;
	`)
	if err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}

	err = task.Run(context.Background(), sender)

	if err == nil || !strings.Contains(err.Error(), "cleanup blocked") {
		t.Fatalf("Run error = %v, want cleanup failure", err)
	}
	assertReminderCount(t, task.db, 1)
}
