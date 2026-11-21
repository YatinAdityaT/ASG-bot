package bothealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type PreviousRun struct {
	StartedAt         time.Time
	LastHeartbeatAt   time.Time
	StoppedGracefully bool
}

type Store struct {
	db *sql.DB
}

func NewStore(database *sql.DB) *Store {
	return &Store{db: database}
}

func (s *Store) Begin(ctx context.Context, now time.Time) (*PreviousRun, error) {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS bot_runtime_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			started_at INTEGER NOT NULL,
			last_heartbeat_at INTEGER NOT NULL,
			stopped_gracefully INTEGER NOT NULL CHECK (stopped_gracefully IN (0, 1))
		)
	`); err != nil {
		return nil, fmt.Errorf("create bot runtime state: %w", err)
	}

	var previous *PreviousRun
	var startedAt, lastHeartbeatAt int64
	var stoppedGracefully int
	err := s.db.QueryRowContext(ctx, `
		SELECT started_at, last_heartbeat_at, stopped_gracefully
		FROM bot_runtime_state
		WHERE id = 1
	`).Scan(&startedAt, &lastHeartbeatAt, &stoppedGracefully)
	switch {
	case err == nil:
		previous = &PreviousRun{
			StartedAt:         fromDatabaseTime(startedAt),
			LastHeartbeatAt:   fromDatabaseTime(lastHeartbeatAt),
			StoppedGracefully: stoppedGracefully == 1,
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, fmt.Errorf("read previous bot runtime state: %w", err)
	}

	timestamp := now.UTC().UnixNano()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO bot_runtime_state (
			id, started_at, last_heartbeat_at, stopped_gracefully
		) VALUES (1, ?, ?, 0)
		ON CONFLICT(id) DO UPDATE SET
			started_at = excluded.started_at,
			last_heartbeat_at = excluded.last_heartbeat_at,
			stopped_gracefully = 0
	`, timestamp, timestamp); err != nil {
		return nil, fmt.Errorf("begin bot runtime: %w", err)
	}
	return previous, nil
}

func (s *Store) Close() error {
	return nil
}

func (s *Store) Heartbeat(ctx context.Context, now time.Time) error {
	return s.update(ctx, now, false)
}

func (s *Store) MarkGraceful(ctx context.Context, now time.Time) error {
	return s.update(ctx, now, true)
}

func (s *Store) update(ctx context.Context, now time.Time, graceful bool) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE bot_runtime_state
		SET last_heartbeat_at = ?, stopped_gracefully = ?
		WHERE id = 1
	`, now.UTC().UnixNano(), boolInt(graceful))
	if err != nil {
		return fmt.Errorf("update bot runtime state: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read bot runtime update: %w", err)
	}
	if changed != 1 {
		return errors.New("bot runtime state is missing")
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func fromDatabaseTime(value int64) time.Time {
	return time.Unix(0, value).UTC()
}
