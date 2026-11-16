package reminderdelivery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SourceWhatsApp = "whatsapp"
	SourceMeet     = "meet"
)

var ErrNotFound = errors.New("reminder delivery not found")

type Reference struct {
	MessageID    string
	SourceType   string
	ReminderID   int64
	ChatID       string
	RequesterJID string
	TargetJID    string
	ScheduledFor time.Time
	DeliveredAt  time.Time
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func Record(ctx context.Context, database execer, reference Reference) error {
	if database == nil ||
		strings.TrimSpace(reference.MessageID) == "" ||
		(reference.SourceType != SourceWhatsApp && reference.SourceType != SourceMeet) ||
		reference.ReminderID <= 0 ||
		strings.TrimSpace(reference.ChatID) == "" ||
		strings.TrimSpace(reference.RequesterJID) == "" ||
		strings.TrimSpace(reference.TargetJID) == "" ||
		reference.ScheduledFor.IsZero() ||
		reference.DeliveredAt.IsZero() {
		return errors.New("invalid reminder delivery")
	}
	_, err := database.ExecContext(ctx, `
		INSERT INTO reminder_deliveries (
			message_id, source_type, reminder_id, chat_id,
			requester_jid, target_jid, scheduled_for, delivered_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO NOTHING
	`, reference.MessageID, reference.SourceType, reference.ReminderID,
		reference.ChatID, reference.RequesterJID, reference.TargetJID,
		reference.ScheduledFor.UTC().UnixNano(),
		reference.DeliveredAt.UTC().UnixNano(),
		time.Now().UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("record reminder delivery: %w", err)
	}
	return nil
}

func Find(ctx context.Context, database *sql.DB, messageID, chatID string) (Reference, error) {
	var (
		reference    Reference
		scheduledFor int64
		deliveredAt  int64
	)
	err := database.QueryRowContext(ctx, `
		SELECT message_id, source_type, reminder_id, chat_id,
		       requester_jid, target_jid, scheduled_for, delivered_at
		FROM reminder_deliveries
		WHERE message_id = ? AND chat_id = ?
	`, strings.TrimSpace(messageID), strings.TrimSpace(chatID)).Scan(
		&reference.MessageID,
		&reference.SourceType,
		&reference.ReminderID,
		&reference.ChatID,
		&reference.RequesterJID,
		&reference.TargetJID,
		&scheduledFor,
		&deliveredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Reference{}, ErrNotFound
	}
	if err != nil {
		return Reference{}, fmt.Errorf("find reminder delivery: %w", err)
	}
	reference.ScheduledFor = time.Unix(0, scheduledFor).UTC()
	reference.DeliveredAt = time.Unix(0, deliveredAt).UTC()
	return reference, nil
}
