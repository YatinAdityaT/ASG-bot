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
	remind_at DATETIME NOT NULL,
	body TEXT,
	requester_name TEXT,
	recurrence_kind TEXT NOT NULL DEFAULT 'none'
		CHECK (recurrence_kind IN ('none', 'daily', 'weekdays', 'weekly')),
	recurrence_weekday INTEGER CHECK (recurrence_weekday BETWEEN 0 AND 6),
	active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
	updated_at INTEGER);
	CREATE INDEX IF NOT EXISTS idx_remind_at ON reminders(remind_at);
	`

var createBirthdaysTable = `
	CREATE TABLE IF NOT EXISTS birthdays (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id TEXT NOT NULL,
	name TEXT NOT NULL,
	birth_date DATE NOT NULL);
	CREATE INDEX IF NOT EXISTS idx_birth_date ON birthdays(birth_date);
	CREATE TABLE IF NOT EXISTS birthday_deliveries (
	birthday_id INTEGER NOT NULL,
	delivery_year INTEGER NOT NULL,
	delivered_at DATETIME NOT NULL,
	PRIMARY KEY (birthday_id, delivery_year),
	FOREIGN KEY (birthday_id) REFERENCES birthdays(id) ON DELETE CASCADE);`

var createOutboundJobsTable = `
	CREATE TABLE IF NOT EXISTS outbound_jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	idempotency_key TEXT NOT NULL UNIQUE,
	chat_id TEXT NOT NULL,
	message TEXT NOT NULL,
	message_id TEXT NOT NULL UNIQUE,
	send_at INTEGER NOT NULL,
	next_attempt_at INTEGER NOT NULL,
	status TEXT NOT NULL CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error TEXT,
	created_at INTEGER NOT NULL,
	sent_at INTEGER);
	CREATE INDEX IF NOT EXISTS idx_outbound_jobs_due
		ON outbound_jobs(status, next_attempt_at);
	CREATE INDEX IF NOT EXISTS idx_outbound_jobs_status
		ON outbound_jobs(status);`

var createMeetMonitorStateTable = `
	CREATE TABLE IF NOT EXISTS meet_monitor_state (
		meeting_code TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		snapshot_json TEXT NOT NULL,
		snapshot_hash TEXT NOT NULL,
		extension_version TEXT NOT NULL,
		forwarding_enabled INTEGER NOT NULL CHECK (forwarding_enabled IN (0, 1)),
		last_heartbeat_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);`

var createIdentityTables = `
	CREATE TABLE IF NOT EXISTS people (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL COLLATE NOCASE UNIQUE,
		canonical_name TEXT NOT NULL,
		whatsapp_jid TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS meet_aliases (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		person_id INTEGER NOT NULL,
		alias TEXT NOT NULL,
		normalized_alias TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL,
		FOREIGN KEY (person_id) REFERENCES people(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_meet_aliases_person
		ON meet_aliases(person_id);`

var createMeetRemindersTable = `
	CREATE TABLE IF NOT EXISTS meet_reminders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		source_key TEXT NOT NULL UNIQUE,
		meeting_code TEXT NOT NULL,
		requester_person_id INTEGER NOT NULL,
		target_person_id INTEGER NOT NULL,
		whatsapp_chat_id TEXT NOT NULL,
		body TEXT NOT NULL,
		remind_at INTEGER NOT NULL,
		whatsapp_delivered_at INTEGER,
		meet_status TEXT NOT NULL DEFAULT 'pending'
			CHECK (meet_status IN ('pending', 'queued', 'skipped_absent', 'skipped_offline')),
		completed_at INTEGER,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		recurrence_kind TEXT NOT NULL DEFAULT 'none'
			CHECK (recurrence_kind IN ('none', 'daily', 'weekdays', 'weekly')),
		recurrence_weekday INTEGER CHECK (recurrence_weekday BETWEEN 0 AND 6),
		FOREIGN KEY (requester_person_id) REFERENCES people(id) ON DELETE RESTRICT,
		FOREIGN KEY (target_person_id) REFERENCES people(id) ON DELETE RESTRICT
	);
	CREATE INDEX IF NOT EXISTS idx_meet_reminders_due
		ON meet_reminders(completed_at, remind_at);`

var createReminderDeliveriesTable = `
	CREATE TABLE IF NOT EXISTS reminder_deliveries (
		message_id TEXT PRIMARY KEY,
		source_type TEXT NOT NULL CHECK (source_type IN ('whatsapp', 'meet')),
		reminder_id INTEGER NOT NULL,
		chat_id TEXT NOT NULL,
		requester_jid TEXT NOT NULL,
		target_jid TEXT NOT NULL,
		scheduled_for INTEGER NOT NULL,
		delivered_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		acted_at INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_reminder_deliveries_source
		ON reminder_deliveries(source_type, reminder_id, delivered_at);`

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

	_, err = database.ExecContext(ctx, createOutboundJobsTable)
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create outbound jobs schema: %w", err)
	}

	_, err = database.ExecContext(ctx, createMeetMonitorStateTable)
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create meet monitor state schema: %w", err)
	}

	_, err = database.ExecContext(ctx, createIdentityTables)
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create identity schema: %w", err)
	}

	_, err = database.ExecContext(ctx, createMeetRemindersTable)
	if err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create Meet reminders schema: %w", err)
	}
	if err := migrateMeetReminderRecurrence(ctx, database); err != nil {
		database.Close()
		return nil, nil, err
	}

	if _, err = database.ExecContext(ctx, createReminderDeliveriesTable); err != nil {
		database.Close()
		return nil, nil, fmt.Errorf("create reminder deliveries schema: %w", err)
	}
	if err := addColumnIfMissing(
		ctx, database, "reminder_deliveries", "acted_at", "INTEGER",
	); err != nil {
		database.Close()
		return nil, nil, err
	}

	return container, database, nil
}

func migrateReminderRequester(ctx context.Context, database *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{"requested_by", "TEXT"},
		{"body", "TEXT"},
		{"requester_name", "TEXT"},
		{"recurrence_kind", "TEXT NOT NULL DEFAULT 'none' CHECK (recurrence_kind IN ('none', 'daily', 'weekdays', 'weekly'))"},
		{"recurrence_weekday", "INTEGER CHECK (recurrence_weekday BETWEEN 0 AND 6)"},
		{"active", "INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1))"},
		{"updated_at", "INTEGER"},
	}
	for _, column := range columns {
		if err := addColumnIfMissing(ctx, database, "reminders", column.name, column.definition); err != nil {
			return err
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

func migrateMeetReminderRecurrence(ctx context.Context, database *sql.DB) error {
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"recurrence_kind", "TEXT NOT NULL DEFAULT 'none' CHECK (recurrence_kind IN ('none', 'daily', 'weekdays', 'weekly'))"},
		{"recurrence_weekday", "INTEGER CHECK (recurrence_weekday BETWEEN 0 AND 6)"},
	} {
		if err := addColumnIfMissing(ctx, database, "meet_reminders", column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func addColumnIfMissing(
	ctx context.Context,
	database *sql.DB,
	table, column, definition string,
) error {
	var count int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pragma_table_info(?)
		WHERE name = ?
	`, table, column).Scan(&count); err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
	}
	if count != 0 {
		return nil
	}
	if _, err := database.ExecContext(
		ctx,
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition),
	); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}
