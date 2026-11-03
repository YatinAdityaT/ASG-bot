package meetmonitor_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	appdb "whatsappBot/db"
	"whatsappBot/meetmonitor"
)

func TestStoreLoadReturnsErrNotSeen(t *testing.T) {
	store, _ := newStateStore(t)

	_, err := store.Load(context.Background(), "jpy-ymgz-tdo")
	if !errors.Is(err, meetmonitor.ErrNotSeen) {
		t.Fatalf("Load() error = %v, want ErrNotSeen", err)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	store, database := newStateStore(t)
	location := time.FixedZone("test", 5*60*60+30*60)
	state := meetmonitor.PersistedState{
		Meeting:   "jpy-ymgz-tdo",
		SessionID: "01234567-89ab-cdef-0123-456789abcdef",
		Sequence:  17,
		Participants: []meetmonitor.Participant{
			{ID: "a", Name: "Alice", Cam: true, Mic: "unmuted", Host: true},
			{ID: "b", Name: "Bob", Mic: "muted", Self: true},
		},
		SnapshotHash:      "abc123",
		ExtensionVersion:  "2.4.0",
		ForwardingEnabled: true,
		LastHeartbeatAt:   time.Date(2026, 11, 3, 10, 0, 0, 123, location),
		UpdatedAt:         time.Date(2026, 11, 3, 10, 0, 1, 456, location),
	}

	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background(), state.Meeting)
	if err != nil {
		t.Fatal(err)
	}

	want := state
	want.LastHeartbeatAt = state.LastHeartbeatAt.UTC()
	want.UpdatedAt = state.UpdatedAt.UTC()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}

	var snapshotJSON string
	if err := database.QueryRow(
		"SELECT snapshot_json FROM meet_monitor_state WHERE meeting_code = ?",
		state.Meeting,
	).Scan(&snapshotJSON); err != nil {
		t.Fatal(err)
	}
	wantJSON := `[{"id":"a","name":"Alice","cam":true,"mic":"unmuted","self":false,"host":true},{"id":"b","name":"Bob","cam":false,"mic":"muted","self":true,"host":false}]`
	if snapshotJSON != wantJSON {
		t.Fatalf("snapshot_json = %q, want %q", snapshotJSON, wantJSON)
	}
}

func TestStoreSaveUpsertsAllFields(t *testing.T) {
	store, _ := newStateStore(t)
	ctx := context.Background()
	first := meetmonitor.PersistedState{
		Meeting:           "jpy-ymgz-tdo",
		SessionID:         "01234567-89ab-cdef-0123-456789abcdef",
		Sequence:          1,
		Participants:      []meetmonitor.Participant{{ID: "a", Name: "Alice", Mic: "muted"}},
		SnapshotHash:      "first",
		ExtensionVersion:  "1.0",
		ForwardingEnabled: false,
		LastHeartbeatAt:   time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 11, 3, 10, 0, 1, 0, time.UTC),
	}
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}

	second := meetmonitor.PersistedState{
		Meeting:           first.Meeting,
		SessionID:         "fedcba98-7654-3210-fedc-ba9876543210",
		Sequence:          2,
		Participants:      []meetmonitor.Participant{{ID: "b", Name: "Bob", Cam: true, Mic: "unmuted", Self: true, Host: true}},
		SnapshotHash:      "second",
		ExtensionVersion:  "2.0",
		ForwardingEnabled: true,
		LastHeartbeatAt:   first.LastHeartbeatAt.Add(time.Minute),
		UpdatedAt:         first.UpdatedAt.Add(time.Minute),
	}
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx, first.Meeting)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, second) {
		t.Fatalf("Load() after upsert = %#v, want %#v", got, second)
	}
}

func TestStoreLoadWrapsInvalidSnapshotJSON(t *testing.T) {
	store, database := newStateStore(t)
	_, err := database.Exec(`
		INSERT INTO meet_monitor_state (
			meeting_code, session_id, sequence, snapshot_json, snapshot_hash,
			extension_version, forwarding_enabled, last_heartbeat_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "jpy-ymgz-tdo", "session", 1, "{", "hash", "1.0", 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Load(context.Background(), "jpy-ymgz-tdo")
	if err == nil || errors.Is(err, meetmonitor.ErrNotSeen) {
		t.Fatalf("Load() error = %v, want wrapped JSON error", err)
	}
}

func newStateStore(t *testing.T) (*meetmonitor.Store, *sql.DB) {
	t.Helper()
	_, database, err := appdb.SetupDB(context.Background(), t.TempDir()+"/meetmonitor.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return meetmonitor.NewStore(database), database
}
