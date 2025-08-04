package db

import (
	"context"
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow/store/sqlstore"
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

func SetupDB(ctx *context.Context) (*sqlstore.Container, *sql.DB) {
	db, err := sql.Open("sqlite3", "file:whatsmeow.db?_foreign_keys=on")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	container := sqlstore.NewWithDB(db, "sqlite3", nil)

	if err := container.Upgrade(*ctx); err != nil {
		log.Fatalf("Failed to upgrade whatsmeow store: %v", err)
	}

	_, err = db.Exec(createRemindersTable)
	if err != nil {
		log.Fatalf("Failed to create Reminders Table: %v", err)
	}

	_, err = db.Exec(createBirthdaysTable)
	if err != nil {
		log.Fatalf("Failed to create Birthdays Table: %v", err)
	}

	log.Println("Successfully created whatsmeow tables and my_custom_table.")

	return container, db
}
