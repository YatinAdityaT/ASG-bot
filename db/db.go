package db

import (
	"context"
	"database/sql"
	"fmt"

	"go.mau.fi/whatsmeow/store/sqlstore"
	_ "modernc.org/sqlite"
)

var createRemindersTable = `
	CREATE TABLE IF NOT EXISTS reminders (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id TEXT NOT NULL,
	to_id TEXT NOT NULL,
	requested_by TEXT,
	message TEXT NOT NULL,
	remind_at DATETIME NOT NULL);
	CREATE INDEX IF NOT EXISTS idx_remind_at ON reminders(remind_at);
	`

var createBirthdaysTable = `
	CREATE TABLE IF NOT EXISTS birthdays (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id TEXT NOT NULL,
	name TEXT NOT NULL,
	birth_date DATE NOT NULL);
	CREATE INDEX IF NOT EXISTS idx_birth_date ON birthdays(birth_date);`

func SetupDB(ctx context.Context, path string) (*sqlstore.Container, *sql.DB, error) {
	database, err := sql.Open("sqlite", "file:"+path+"?_foreign_keys=on")
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	container := sqlstore.NewWithDB(database, "sqlite3", nil)

	if err := container.Upgrade(ctx); err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("upgrade whatsmeow store: %w", err)
	}

	_, err = database.ExecContext(ctx, createRemindersTable)
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create reminders schema: %w", err)
	}
	if err := migrateReminderRequester(ctx, database); err != nil {
		database.Close()
		return nil, nil, err
	}

	_, err = database.ExecContext(ctx, createBirthdaysTable)
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create birthdays schema: %w", err)
	}

	return container, database, nil
}

func migrateReminderRequester(ctx context.Context, database *sql.DB) error {
	var requestedByColumns int
	err := database.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pragma_table_info('reminders')
		WHERE name = 'requested_by'
	`).Scan(&requestedByColumns)
	if err != nil {
		return fmt.Errorf("inspect reminders schema: %w", err)
	}

	if requestedByColumns == 0 {
		if _, err := database.ExecContext(ctx, "ALTER TABLE reminders ADD COLUMN requested_by TEXT"); err != nil {
			return fmt.Errorf("add reminders requested_by: %w", err)
		}
	}
	if _, err := database.ExecContext(ctx, `
		UPDATE reminders
		SET requested_by = to_id
		WHERE requested_by IS NULL
	`); err != nil {
		return fmt.Errorf("backfill reminders requested_by: %w", err)
	}
	if _, err := database.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_reminders_requester_chat
		ON reminders(requested_by, chat_id)
	`); err != nil {
		return fmt.Errorf("index reminders requester and chat: %w", err)
	}
	return nil
}
