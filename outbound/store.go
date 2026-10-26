package outbound

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotFound            = errors.New("outbound job not found")
	ErrIdempotencyConflict = errors.New("idempotency key already used with different request")
	ErrInvalidTransition   = errors.New("invalid outbound job transition")
)

var messageIDPattern = regexp.MustCompile(`^[0-9A-F]{20}$`)

var retryBackoffs = []time.Duration{
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
}

const maxAttempts = 5

type messageIDGenerator func() (string, error)

type Option func(*Store)

func WithClock(clock func() time.Time) Option {
	return func(store *Store) {
		store.now = clock
	}
}

func WithMessageIDGenerator(generator func() (string, error)) Option {
	return func(store *Store) {
		store.generateMessageID = generator
	}
}

type Store struct {
	db                *sql.DB
	chatID            string
	now               func() time.Time
	generateMessageID messageIDGenerator
}

func NewStore(database *sql.DB, chatID string, options ...Option) *Store {
	store := &Store{
		db:                database,
		chatID:            chatID,
		now:               time.Now,
		generateMessageID: newMessageID,
	}
	for _, option := range options {
		option(store)
	}
	return store
}

func (s *Store) Create(ctx context.Context, request CreateRequest) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create outbound job: %w", err)
	}
	defer tx.Rollback()

	existing, err := getByIdempotencyKey(ctx, tx, request.IdempotencyKey)
	if err == nil {
		sendAt := request.SendAt
		if sendAt.IsZero() {
			sendAt = existing.CreatedAt
		}
		if existing.ChatID != s.chatID || existing.Message != request.Text || !existing.SendAt.Equal(sendAt) {
			return nil, ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit outbound job replay: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("look up outbound idempotency key: %w", err)
	}

	now := s.now().UTC()
	sendAt := request.SendAt
	if sendAt.IsZero() {
		sendAt = now
	} else {
		sendAt = sendAt.UTC()
	}
	messageID, err := s.generateMessageID()
	if err != nil {
		return nil, fmt.Errorf("generate WhatsApp message ID: %w", err)
	}
	if !messageIDPattern.MatchString(messageID) {
		return nil, fmt.Errorf("generate WhatsApp message ID: invalid value %q", messageID)
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO outbound_jobs (
			idempotency_key, chat_id, message, message_id, send_at,
			next_attempt_at, status, attempts, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)
	`, request.IdempotencyKey, s.chatID, request.Text, messageID, toDatabaseTime(sendAt),
		toDatabaseTime(sendAt), StatusPending, toDatabaseTime(now))
	if err != nil {
		return nil, fmt.Errorf("insert outbound job: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read outbound job ID: %w", err)
	}
	job, err := getByID(ctx, tx, id)
	if err != nil {
		return nil, fmt.Errorf("read created outbound job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit outbound job: %w", err)
	}
	return job, nil
}

func (s *Store) Get(ctx context.Context, id int64) (*Job, error) {
	job, err := getByID(ctx, s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get outbound job: %w", err)
	}
	return job, nil
}

func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]Job, error) {
	if limit <= 0 {
		return []Job{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+jobColumns+`
		FROM outbound_jobs
		WHERE status = ? AND next_attempt_at <= ?
		ORDER BY next_attempt_at, id
		LIMIT ?
	`, StatusPending, toDatabaseTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("query due outbound jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan due outbound job: %w", err)
		}
		jobs = append(jobs, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due outbound jobs: %w", err)
	}
	return jobs, nil
}

func (s *Store) Claim(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbound_jobs
		SET status = ?
		WHERE id = ? AND status = ?
	`, StatusSending, id, StatusPending)
	if err != nil {
		return false, fmt.Errorf("claim outbound job: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read outbound claim result: %w", err)
	}
	return changed == 1, nil
}

func (s *Store) MarkSent(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbound_jobs
		SET status = ?, sent_at = ?, last_error = NULL
		WHERE id = ? AND status = ?
	`, StatusSent, toDatabaseTime(s.now()), id, StatusSending)
	if err != nil {
		return fmt.Errorf("mark outbound job sent: %w", err)
	}
	return requireTransition(result)
}

func (s *Store) MarkFailed(ctx context.Context, id int64, failure string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin outbound failure transition: %w", err)
	}
	defer tx.Rollback()

	var status Status
	var attempts int
	err = tx.QueryRowContext(ctx, "SELECT status, attempts FROM outbound_jobs WHERE id = ?", id).Scan(&status, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read outbound failure state: %w", err)
	}
	if status != StatusSending {
		return ErrInvalidTransition
	}

	attempts++
	nextStatus := StatusPending
	nextAttemptAt := s.now().UTC()
	if attempts >= maxAttempts {
		nextStatus = StatusFailed
	} else {
		nextAttemptAt = nextAttemptAt.Add(retryBackoffs[attempts-1])
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE outbound_jobs
		SET status = ?, attempts = ?, last_error = ?, next_attempt_at = ?
		WHERE id = ? AND status = ?
	`, nextStatus, attempts, failure, toDatabaseTime(nextAttemptAt), id, StatusSending)
	if err != nil {
		return fmt.Errorf("mark outbound job failed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outbound failure transition: %w", err)
	}
	return nil
}

func (s *Store) RecoverSending(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbound_jobs
		SET status = ?, next_attempt_at = ?
		WHERE status = ?
	`, StatusPending, toDatabaseTime(s.now()), StatusSending)
	if err != nil {
		return 0, fmt.Errorf("recover sending outbound jobs: %w", err)
	}
	recovered, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read outbound recovery result: %w", err)
	}
	return recovered, nil
}

func newMessageID() (string, error) {
	bytes := make([]byte, 10)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(bytes)), nil
}

func requireTransition(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read outbound transition result: %w", err)
	}
	if changed != 1 {
		return ErrInvalidTransition
	}
	return nil
}

const jobColumns = `
	id, idempotency_key, chat_id, message, message_id, send_at,
	next_attempt_at, status, attempts, last_error, created_at, sent_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func getByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (*Job, error) {
	return scanJob(queryer.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM outbound_jobs WHERE id = ?", id))
}

func getByIdempotencyKey(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key string) (*Job, error) {
	return scanJob(queryer.QueryRowContext(ctx,
		"SELECT "+jobColumns+" FROM outbound_jobs WHERE idempotency_key = ?", key))
}

func scanJob(row rowScanner) (*Job, error) {
	var (
		job                       Job
		sendAt, nextAt, createdAt int64
		sentAt                    sql.NullInt64
		lastError                 sql.NullString
	)
	err := row.Scan(
		&job.ID,
		&job.IdempotencyKey,
		&job.ChatID,
		&job.Message,
		&job.MessageID,
		&sendAt,
		&nextAt,
		&job.Status,
		&job.Attempts,
		&lastError,
		&createdAt,
		&sentAt,
	)
	if err != nil {
		return nil, err
	}
	job.SendAt = fromDatabaseTime(sendAt)
	job.NextAttemptAt = fromDatabaseTime(nextAt)
	job.CreatedAt = fromDatabaseTime(createdAt)
	if lastError.Valid {
		job.LastError = &lastError.String
	}
	if sentAt.Valid {
		value := fromDatabaseTime(sentAt.Int64)
		job.SentAt = &value
	}
	return &job, nil
}

func toDatabaseTime(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func fromDatabaseTime(value int64) time.Time {
	return time.Unix(0, value).UTC()
}
