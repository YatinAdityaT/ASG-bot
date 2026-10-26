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
		"outbound_jobs",
		"idx_remind_at",
		"idx_reminders_requester_chat",
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
