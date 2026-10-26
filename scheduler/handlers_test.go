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
