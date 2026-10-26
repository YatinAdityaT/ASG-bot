package db_test

import (
	"context"
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

	for _, name := range []string{"reminders", "birthdays", "idx_remind_at", "idx_birth_date"} {
		var found string
		err := conn.QueryRow(
			"SELECT name FROM sqlite_master WHERE name = ?",
			name,
		).Scan(&found)
		if err != nil {
			t.Fatalf("schema object %q: %v", name, err)
		}
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
