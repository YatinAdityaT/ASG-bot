package meetmonitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Store struct {
	db *sql.DB
}

func NewStore(database *sql.DB) *Store {
	return &Store{db: database}
}

func (s *Store) Load(ctx context.Context, meeting string) (PersistedState, error) {
	var (
		state                      PersistedState
		snapshotJSON               string
		forwarding                 bool
		lastHeartbeatAt, updatedAt int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT meeting_code, session_id, sequence, snapshot_json, snapshot_hash,
			extension_version, forwarding_enabled, last_heartbeat_at, updated_at
		FROM meet_monitor_state
		WHERE meeting_code = ?
	`, meeting).Scan(
		&state.Meeting,
		&state.SessionID,
		&state.Sequence,
		&snapshotJSON,
		&state.SnapshotHash,
		&state.ExtensionVersion,
		&forwarding,
		&lastHeartbeatAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PersistedState{}, ErrNotSeen
	}
	if err != nil {
		return PersistedState{}, fmt.Errorf("load Meet monitor state: %w", err)
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &state.Participants); err != nil {
		return PersistedState{}, fmt.Errorf("decode Meet monitor snapshot: %w", err)
	}
	state.ForwardingEnabled = forwarding
	state.LastHeartbeatAt = time.Unix(0, lastHeartbeatAt).UTC()
	state.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return state, nil
}

func (s *Store) Save(ctx context.Context, state PersistedState) error {
	snapshotJSON, err := json.Marshal(state.Participants)
	if err != nil {
		return fmt.Errorf("encode Meet monitor snapshot: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO meet_monitor_state (
			meeting_code, session_id, sequence, snapshot_json, snapshot_hash,
			extension_version, forwarding_enabled, last_heartbeat_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(meeting_code) DO UPDATE SET
			session_id = excluded.session_id,
			sequence = excluded.sequence,
			snapshot_json = excluded.snapshot_json,
			snapshot_hash = excluded.snapshot_hash,
			extension_version = excluded.extension_version,
			forwarding_enabled = excluded.forwarding_enabled,
			last_heartbeat_at = excluded.last_heartbeat_at,
			updated_at = excluded.updated_at
	`, state.Meeting, state.SessionID, state.Sequence, string(snapshotJSON), state.SnapshotHash,
		state.ExtensionVersion, state.ForwardingEnabled, state.LastHeartbeatAt.UTC().UnixNano(),
		state.UpdatedAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("save Meet monitor state: %w", err)
	}
	return nil
}
