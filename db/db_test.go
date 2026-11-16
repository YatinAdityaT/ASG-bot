package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"whatsappBot/db"
)

func TestSetupDBCreatesApplicationSchema(t *testing.T) {
	_, conn, err := db.SetupDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	for _, name := range []string{
		"reminders",
		"birthdays",
		"birthday_deliveries",
		"outbound_jobs",
		"meet_monitor_state",
		"reminder_deliveries",
		"idx_remind_at",
		"idx_reminders_requester_chat",
		"idx_reminder_deliveries_source",
		"idx_birth_date",
		"idx_outbound_jobs_due",
		"idx_outbound_jobs_status",
	} {
		var found string
		err := conn.QueryRow(
			"SELECT name FROM sqlite_master WHERE name = ?",
			name,
		).Scan(&found)
		if err != nil {
			t.Fatalf("schema object %q: %v", name, err)
		}
	}

	var requestedByColumnCount int
	if err := conn.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('reminders')
		WHERE name = 'requested_by'
	`).Scan(&requestedByColumnCount); err != nil {
		t.Fatal(err)
	}
	if requestedByColumnCount != 1 {
		t.Fatalf("requested_by column count = %d, want 1", requestedByColumnCount)
	}
	for _, column := range []string{
		"body",
		"requester_name",
		"recurrence_kind",
		"recurrence_weekday",
		"active",
		"updated_at",
	} {
		assertColumn(t, conn, "reminders", column)
	}
	for _, column := range []string{
		"recurrence_kind",
		"recurrence_weekday",
	} {
		assertColumn(t, conn, "meet_reminders", column)
	}
	assertColumn(t, conn, "reminder_deliveries", "acted_at")

	for _, column := range []string{
		"id",
		"idempotency_key",
		"chat_id",
		"message",
		"message_id",
		"send_at",
		"next_attempt_at",
		"status",
		"attempts",
		"last_error",
		"created_at",
		"sent_at",
	} {
		var count int
		if err := conn.QueryRow(`
			SELECT COUNT(*)
			FROM pragma_table_info('outbound_jobs')
			WHERE name = ?
		`, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("outbound_jobs column %q count = %d, want 1", column, count)
		}
	}

	for _, column := range []string{
		"meeting_code",
		"session_id",
		"sequence",
		"snapshot_json",
		"snapshot_hash",
		"extension_version",
		"forwarding_enabled",
		"last_heartbeat_at",
		"updated_at",
	} {
		var count int
		if err := conn.QueryRow(`
			SELECT COUNT(*)
			FROM pragma_table_info('meet_monitor_state')
			WHERE name = ?
		`, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("meet_monitor_state column %q count = %d, want 1", column, count)
		}
	}
}

func TestSetupDBMigratesAndBackfillsLegacyRemindersIdempotently(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`
		CREATE TABLE reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			message TEXT NOT NULL,
			remind_at DATETIME NOT NULL
		);
		INSERT INTO reminders (chat_id, to_id, message, remind_at)
		VALUES ('123@g.us', '456@s.whatsapp.net', 'legacy', '2030-01-01 12:00:00');
		CREATE TABLE reminder_deliveries (
			message_id TEXT PRIMARY KEY,
			source_type TEXT NOT NULL,
			reminder_id INTEGER NOT NULL,
			chat_id TEXT NOT NULL,
			requester_jid TEXT NOT NULL,
			target_jid TEXT NOT NULL,
			scheduled_for INTEGER NOT NULL,
			delivered_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		);
	`); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	_, conn, err := db.SetupDB(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var requestedBy string
	if err := conn.QueryRow("SELECT requested_by FROM reminders WHERE id = 1").Scan(&requestedBy); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if requestedBy != "456@s.whatsapp.net" {
		conn.Close()
		t.Fatalf("requested_by = %q, want legacy to_id", requestedBy)
	}
	var recurrenceKind string
	var active int
	if err := conn.QueryRow(
		"SELECT recurrence_kind, active FROM reminders WHERE id = 1",
	).Scan(&recurrenceKind, &active); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if recurrenceKind != "none" || active != 1 {
		conn.Close()
		t.Fatalf("legacy recurrence/active = %q/%d, want none/1", recurrenceKind, active)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	_, conn, err = db.SetupDB(ctx, path)
	if err != nil {
		t.Fatalf("second SetupDB() failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	var requestedByColumnCount int
	if err := conn.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info('reminders')
		WHERE name = 'requested_by'
	`).Scan(&requestedByColumnCount); err != nil {
		t.Fatal(err)
	}
	if requestedByColumnCount != 1 {
		t.Fatalf("requested_by column count after second migration = %d, want 1", requestedByColumnCount)
	}
	assertColumn(t, conn, "reminder_deliveries", "acted_at")
}

func assertColumn(t *testing.T, database *sql.DB, table, column string) {
	t.Helper()
	var count int
	if err := database.QueryRow(`
		SELECT COUNT(*)
		FROM pragma_table_info(?)
		WHERE name = ?
	`, table, column).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%s column %q count = %d, want 1", table, column, count)
	}
}

func TestSetupDBReturnsOpenErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "test.db")

	container, conn, err := db.SetupDB(context.Background(), path)

	if err == nil {
		if conn != nil {
			conn.Close()
		}
		t.Fatal("SetupDB() error = nil, want database open error")
	}
	if container != nil {
		t.Error("SetupDB() container is non-nil on error")
	}
	if conn != nil {
		conn.Close()
		t.Error("SetupDB() database is non-nil on error")
	}
}

func TestSetupDBSerializesConcurrentUse(t *testing.T) {
	_, conn, err := db.SetupDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if got := conn.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}

	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	initialWaitCount := conn.Stats().WaitCount
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, err := conn.Exec("INSERT INTO reminders (chat_id, to_id, message, remind_at) VALUES (?, ?, ?, ?)",
			"chat", "recipient", "message", time.Now())
		result <- err
	}()
	<-started

	deadline := time.Now().Add(5 * time.Second)
	for conn.Stats().WaitCount == initialWaitCount {
		if time.Now().After(deadline) {
			t.Fatal("second operation did not wait for the held connection")
		}
		runtime.Gosched()
	}

	select {
	case err := <-result:
		t.Fatalf("second operation completed while transaction held the only connection: %v", err)
	default:
	}

	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("second operation after transaction release: %v", err)
	}
}
