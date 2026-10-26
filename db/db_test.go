package db_test

import (
	"context"
	"path/filepath"
	"testing"

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
