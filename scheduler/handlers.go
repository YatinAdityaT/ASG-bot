package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"google.golang.org/protobuf/proto"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// ----------------- Reminder Provider -----------------

type ReminderTask struct {
	chatID   types.JID
	message  string
	schedule time.Time
	id       string
	db       *sql.DB
	to       string
}

func (r *ReminderTask) Run(ctx context.Context, client *whatsmeow.Client) error {
	emsg := &waProto.ExtendedTextMessage{
		Text: proto.String(r.message),
		ContextInfo: &waProto.ContextInfo{
			MentionedJID: []string{r.to},
		},
	}

	msg := &waProto.Message{
		ExtendedTextMessage: emsg,
	}

	_, err := client.SendMessage(ctx, r.chatID, msg)
	if err != nil {
		fmt.Println("Error running task %s: %v\n", r.id, err)
	} else {
		r.CleanUp(ctx)
	}
	return err
}

func (r *ReminderTask) Time() time.Time {
	return r.schedule
}

func (r *ReminderTask) ID() string {
	return r.id
}

func (r *ReminderTask) CleanUp(ctx context.Context) {
	r.db.ExecContext(ctx, `DELETE FROM reminders WHERE id = ?`, r.id)
}

func (r *ReminderProvider) GetDueTasks(ctx context.Context, now time.Time) ([]ScheduledTask, error) {
	query := `
		SELECT id, chat_id, to_id, message, remind_at
		FROM reminders
		WHERE remind_at <= ?
	`
	rows, err := r.db.QueryContext(ctx, query, now)
	if err != nil {
		return nil, fmt.Errorf("query reminders: %w", err)
	}
	defer rows.Close()

	var tasks []ScheduledTask
	for rows.Next() {
		var id, chatID, toID, message string
		var remindAt time.Time
		if err := rows.Scan(&id, &chatID, &toID, &message, &remindAt); err != nil {
			return nil, err
		}
		jid, err := types.ParseJID(chatID)
		if err != nil {
			return nil, fmt.Errorf("invalid JID %q: %w", chatID, err)
		}
		_, err = types.ParseJID(toID)
		if err != nil {
			return nil, fmt.Errorf("invalid JID %q: %w", toID, err)
		}
		task := &ReminderTask{
			id:       id,
			chatID:   jid,
			message:  message,
			schedule: remindAt,
			db:       r.db,
			to:       toID,
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// ----------------- Birthday Provider -----------------
type BirthdayTask struct {
	chatID   types.JID
	message  string
	schedule time.Time
	id       string
}

func (r *BirthdayTask) Run(ctx context.Context, client *whatsmeow.Client) error {
	msg := &waProto.Message{Conversation: &r.message}
	_, err := client.SendMessage(ctx, r.chatID, msg)
	return err
}

func (b *BirthdayTask) Time() time.Time {
	return b.schedule
}

func (b *BirthdayTask) ID() string {
	return b.id
}

func (b *BirthdayProvider) GetDueTasks(ctx context.Context, now time.Time) ([]ScheduledTask, error) {
	today := now.Format("01-02") // MM-DD
	query := `
		SELECT id, chat_id, name
		FROM birthdays
		WHERE strftime('%m-%d', birth_date) = ?
	`
	rows, err := b.db.QueryContext(ctx, query, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []ScheduledTask
	for rows.Next() {
		var id, chatID, name string
		if err := rows.Scan(&id, &chatID, &name); err != nil {
			return nil, err
		}
		msg := fmt.Sprintf("🎉 It's %s's birthday today!", name)

		jid, err := types.ParseJID(chatID)
		if err != nil {
			return nil, fmt.Errorf("invalid JID %q: %w", chatID, err)
		}

		task := &BirthdayTask{
			id:       "birthday-" + id,
			chatID:   jid,
			message:  msg,
			schedule: now,
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}
