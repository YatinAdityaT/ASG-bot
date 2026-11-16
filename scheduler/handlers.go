package scheduler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"whatsappBot/reminderdelivery"
	"whatsappBot/utils"
)

// ----------------- Reminder Provider -----------------

type ReminderTask struct {
	chatID        types.JID
	message       string
	schedule      time.Time
	id            string
	db            *sql.DB
	to            string
	requestedBy   string
	body          string
	requesterName string
	recurrence    utils.Recurrence
	processedAt   time.Time
}

func (r *ReminderTask) Run(ctx context.Context, sender utils.MessageSender) error {
	emsg := &waProto.ExtendedTextMessage{
		Text: proto.String(r.message),
		ContextInfo: &waProto.ContextInfo{
			MentionedJID: []string{r.to},
		},
	}

	msg := &waProto.Message{
		ExtendedTextMessage: emsg,
	}

	messageID := reminderMessageID(r.id, r.schedule)
	_, err := sender.SendMessage(
		ctx,
		r.chatID,
		msg,
		whatsmeow.SendRequestExtra{ID: messageID},
	)
	if err != nil {
		return fmt.Errorf("send reminder %s: %w", r.id, err)
	}
	if err := r.recordAndAdvance(ctx, messageID); err != nil {
		return fmt.Errorf("advance reminder %s: %w", r.id, err)
	}
	return nil
}

func (r *ReminderTask) Time() time.Time {
	return r.schedule
}

func (r *ReminderTask) ID() string {
	return r.id
}

func (r *ReminderTask) recordAndAdvance(ctx context.Context, messageID types.MessageID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := reminderdelivery.Record(ctx, tx, reminderdelivery.Reference{
		MessageID:    string(messageID),
		SourceType:   reminderdelivery.SourceWhatsApp,
		ReminderID:   mustParseReminderID(r.id),
		ChatID:       r.chatID.String(),
		RequesterJID: r.requestedBy,
		TargetJID:    r.to,
		ScheduledFor: r.schedule,
		DeliveredAt:  r.processedAt,
	}); err != nil {
		return err
	}

	if !r.recurrence.IsRecurring() {
		_, err = tx.ExecContext(ctx, `
			UPDATE reminders
			SET active = 0, updated_at = ?
			WHERE id = ? AND active = 1
		`, r.processedAt.UTC().UnixNano(), r.id)
	} else {
		next := r.recurrence.NextAfter(r.schedule, r.processedAt)
		target, parseErr := types.ParseJID(r.to)
		if parseErr != nil {
			return parseErr
		}
		requester, parseErr := types.ParseJID(r.requestedBy)
		if parseErr != nil {
			return parseErr
		}
		nextMessage := utils.FormatReminderDelivery(
			target.ToNonAD(),
			requester.ToNonAD(),
			r.requesterName,
			r.body,
			next,
		)
		_, err = tx.ExecContext(ctx, `
			UPDATE reminders
			SET remind_at = ?, message = ?, updated_at = ?
			WHERE id = ? AND active = 1
		`, next, nextMessage, r.processedAt.UTC().UnixNano(), r.id)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ReminderProvider) GetDueTasks(ctx context.Context, now time.Time) ([]ScheduledTask, error) {
	query := `
		SELECT id, chat_id, to_id, COALESCE(requested_by, to_id),
		       message, remind_at, COALESCE(body, ''),
		       COALESCE(requester_name, ''), recurrence_kind,
		       recurrence_weekday
		FROM reminders
		WHERE active = 1 AND remind_at <= ?
	`
	rows, err := r.db.QueryContext(ctx, query, now)
	if err != nil {
		return nil, fmt.Errorf("query reminders: %w", err)
	}
	defer rows.Close()

	var tasks []ScheduledTask
	for rows.Next() {
		var id, chatID, toID, requestedBy, message, body, requesterName, recurrenceKind string
		var remindAt time.Time
		var recurrenceWeekday sql.NullInt64
		if err := rows.Scan(
			&id,
			&chatID,
			&toID,
			&requestedBy,
			&message,
			&remindAt,
			&body,
			&requesterName,
			&recurrenceKind,
			&recurrenceWeekday,
		); err != nil {
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
			id:            id,
			chatID:        jid,
			message:       message,
			schedule:      remindAt,
			db:            r.db,
			to:            toID,
			requestedBy:   requestedBy,
			body:          body,
			requesterName: requesterName,
			recurrence: utils.Recurrence{
				Kind:    recurrenceKind,
				Weekday: recurrenceWeekdayValue(recurrenceWeekday),
			},
			processedAt: now,
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reminders: %w", err)
	}
	return tasks, nil
}

func reminderMessageID(id string, scheduledFor time.Time) types.MessageID {
	sum := sha256.Sum256([]byte(
		"reminder:" + id + ":" + strconv.FormatInt(scheduledFor.UTC().UnixNano(), 10),
	))
	return types.MessageID(strings.ToUpper(hex.EncodeToString(sum[:10])))
}

func mustParseReminderID(id string) int64 {
	value, _ := strconv.ParseInt(id, 10, 64)
	return value
}

func recurrenceWeekdayValue(value sql.NullInt64) time.Weekday {
	if value.Valid {
		return time.Weekday(value.Int64)
	}
	return time.Sunday
}

// ----------------- Birthday Provider -----------------
type BirthdayTask struct {
	db           *sql.DB
	birthdayID   string
	deliveryYear int
	chatID       types.JID
	message      string
	messageID    types.MessageID
	schedule     time.Time
	id           string
}

func (r *BirthdayTask) Run(ctx context.Context, sender utils.MessageSender) error {
	msg := &waProto.Message{Conversation: &r.message}
	if _, err := sender.SendMessage(
		ctx,
		r.chatID,
		msg,
		whatsmeow.SendRequestExtra{ID: r.messageID},
	); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO birthday_deliveries (birthday_id, delivery_year, delivered_at)
		VALUES (?, ?, ?)
		ON CONFLICT (birthday_id, delivery_year) DO NOTHING
	`, r.birthdayID, r.deliveryYear, r.schedule)
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
		SELECT birthday.id, birthday.chat_id, birthday.name
		FROM birthdays AS birthday
		WHERE strftime('%m-%d', birthday.birth_date) = ?
		  AND NOT EXISTS (
			SELECT 1
			FROM birthday_deliveries AS delivery
			WHERE delivery.birthday_id = birthday.id
			  AND delivery.delivery_year = ?
		  )
	`
	rows, err := b.db.QueryContext(ctx, query, today, now.Year())
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
			db:           b.db,
			birthdayID:   id,
			deliveryYear: now.Year(),
			id:           "birthday-" + id,
			chatID:       jid,
			message:      msg,
			messageID:    birthdayMessageID(id, now.Year()),
			schedule:     now,
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate birthdays: %w", err)
	}
	return tasks, nil
}

func birthdayMessageID(id string, year int) types.MessageID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("birthday:%s:%d", id, year)))
	return types.MessageID(strings.ToUpper(hex.EncodeToString(sum[:10])))
}
