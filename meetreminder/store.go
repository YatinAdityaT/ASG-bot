package meetreminder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"whatsappBot/identity"
)

var ErrNotFound = errors.New("Meet reminder not found")

type CreateRequest struct {
	SourceKey         string
	MeetingCode       string
	RequesterPersonID int64
	TargetPersonID    int64
	WhatsAppChatID    string
	Body              string
	RemindAt          time.Time
}

type Reminder struct {
	ID                  int64
	SourceKey           string
	MeetingCode         string
	Requester           identity.Person
	Target              identity.Person
	WhatsAppChatID      string
	Body                string
	RemindAt            time.Time
	WhatsAppDeliveredAt *time.Time
	MeetStatus          string
	CompletedAt         *time.Time
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(database *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: database, now: now}
}

func (s *Store) Create(ctx context.Context, request CreateRequest) (Reminder, bool, error) {
	request.SourceKey = strings.TrimSpace(request.SourceKey)
	request.MeetingCode = strings.TrimSpace(request.MeetingCode)
	request.WhatsAppChatID = strings.TrimSpace(request.WhatsAppChatID)
	request.Body = strings.Join(strings.Fields(request.Body), " ")
	if request.SourceKey == "" || request.MeetingCode == "" ||
		request.RequesterPersonID <= 0 || request.TargetPersonID <= 0 ||
		request.WhatsAppChatID == "" || request.Body == "" ||
		request.RemindAt.IsZero() {
		return Reminder{}, false, errors.New("invalid Meet reminder")
	}

	now := s.now().UTC().UnixNano()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO meet_reminders (
			source_key, meeting_code, requester_person_id, target_person_id,
			whatsapp_chat_id, body, remind_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_key) DO NOTHING
	`, request.SourceKey, request.MeetingCode, request.RequesterPersonID,
		request.TargetPersonID, request.WhatsAppChatID, request.Body,
		request.RemindAt.UTC().UnixNano(), now, now)
	if err != nil {
		return Reminder{}, false, fmt.Errorf("create Meet reminder: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Reminder{}, false, fmt.Errorf("read Meet reminder insertion: %w", err)
	}
	reminder, err := s.GetBySourceKey(ctx, request.SourceKey)
	if err != nil {
		return Reminder{}, false, err
	}
	return reminder, affected == 1, nil
}

func (s *Store) GetBySourceKey(ctx context.Context, sourceKey string) (Reminder, error) {
	return s.getOne(ctx, `
		WHERE reminder.source_key = ?
	`, strings.TrimSpace(sourceKey))
}

func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]Reminder, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, reminderSelect+`
		WHERE reminder.completed_at IS NULL
		  AND reminder.remind_at <= ?
		ORDER BY reminder.remind_at, reminder.id
		LIMIT ?
	`, now.UTC().UnixNano(), limit)
	if err != nil {
		return nil, fmt.Errorf("query due Meet reminders: %w", err)
	}
	defer rows.Close()

	var reminders []Reminder
	for rows.Next() {
		reminder, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		reminders = append(reminders, reminder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due Meet reminders: %w", err)
	}
	return reminders, nil
}

func (s *Store) MarkWhatsAppDelivered(ctx context.Context, id int64, deliveredAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE meet_reminders
		SET whatsapp_delivered_at = COALESCE(whatsapp_delivered_at, ?),
			updated_at = ?
		WHERE id = ? AND completed_at IS NULL
	`, deliveredAt.UTC().UnixNano(), s.now().UTC().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("mark Meet reminder WhatsApp delivery: %w", err)
	}
	return requireChanged(result, "Meet reminder")
}

func (s *Store) Complete(ctx context.Context, id int64, meetStatus string, completedAt time.Time) error {
	switch meetStatus {
	case "queued", "skipped_absent", "skipped_offline":
	default:
		return errors.New("invalid Meet reminder outcome")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE meet_reminders
		SET meet_status = ?, completed_at = ?, updated_at = ?
		WHERE id = ?
		  AND completed_at IS NULL
		  AND whatsapp_delivered_at IS NOT NULL
	`, meetStatus, completedAt.UTC().UnixNano(), s.now().UTC().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("complete Meet reminder: %w", err)
	}
	return requireChanged(result, "Meet reminder")
}

const reminderSelect = `
	SELECT
		reminder.id,
		reminder.source_key,
		reminder.meeting_code,
		requester.id,
		requester.username,
		requester.canonical_name,
		requester.whatsapp_jid,
		target.id,
		target.username,
		target.canonical_name,
		target.whatsapp_jid,
		COALESCE((
			SELECT group_concat(alias, char(31))
			FROM meet_aliases
			WHERE person_id = target.id
		), ''),
		reminder.whatsapp_chat_id,
		reminder.body,
		reminder.remind_at,
		reminder.whatsapp_delivered_at,
		reminder.meet_status,
		reminder.completed_at
	FROM meet_reminders AS reminder
	JOIN people AS requester ON requester.id = reminder.requester_person_id
	JOIN people AS target ON target.id = reminder.target_person_id
`

func (s *Store) getOne(ctx context.Context, predicate string, args ...any) (Reminder, error) {
	row := s.db.QueryRowContext(ctx, reminderSelect+predicate, args...)
	reminder, err := scanReminder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Reminder{}, ErrNotFound
	}
	if err != nil {
		return Reminder{}, err
	}
	return reminder, nil
}

type scanner interface {
	Scan(...any) error
}

func scanReminder(row scanner) (Reminder, error) {
	var (
		reminder            Reminder
		remindAt            int64
		whatsAppDeliveredAt sql.NullInt64
		completedAt         sql.NullInt64
		targetAliases       string
	)
	err := row.Scan(
		&reminder.ID,
		&reminder.SourceKey,
		&reminder.MeetingCode,
		&reminder.Requester.ID,
		&reminder.Requester.Username,
		&reminder.Requester.CanonicalName,
		&reminder.Requester.WhatsAppJID,
		&reminder.Target.ID,
		&reminder.Target.Username,
		&reminder.Target.CanonicalName,
		&reminder.Target.WhatsAppJID,
		&targetAliases,
		&reminder.WhatsAppChatID,
		&reminder.Body,
		&remindAt,
		&whatsAppDeliveredAt,
		&reminder.MeetStatus,
		&completedAt,
	)
	if err != nil {
		return Reminder{}, err
	}
	reminder.RemindAt = time.Unix(0, remindAt).UTC()
	if targetAliases != "" {
		reminder.Target.MeetAliases = strings.Split(targetAliases, string(rune(31)))
	}
	if whatsAppDeliveredAt.Valid {
		value := time.Unix(0, whatsAppDeliveredAt.Int64).UTC()
		reminder.WhatsAppDeliveredAt = &value
	}
	if completedAt.Valid {
		value := time.Unix(0, completedAt.Int64).UTC()
		reminder.CompletedAt = &value
	}
	return reminder, nil
}

func requireChanged(result sql.Result, label string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s update: %w", label, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
