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

	_, err = database.ExecContext(ctx, createBirthdaysTable)
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create birthdays schema: %w", err)
	}

	return container, database, nil
}
