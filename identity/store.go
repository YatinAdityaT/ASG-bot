package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidUsername = errors.New("invalid username")
	ErrInvalidName     = errors.New("invalid canonical name")
	ErrInvalidJID      = errors.New("invalid WhatsApp JID")
	ErrInvalidAlias    = errors.New("invalid Meet alias")
	ErrUsernameTaken   = errors.New("username already mapped")
	ErrWhatsAppTaken   = errors.New("WhatsApp identity already mapped")
	ErrAliasTaken      = errors.New("Meet alias already mapped")
	ErrNotFound        = errors.New("person mapping not found")
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{1,31}$`)

type Person struct {
	ID            int64
	Username      string
	CanonicalName string
	WhatsAppJID   string
	MeetAliases   []string
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(database *sql.DB) *Store {
	return &Store{db: database, now: time.Now}
}

func (s *Store) Add(
	ctx context.Context,
	username, canonicalName, whatsappJID, meetAlias string,
) (Person, error) {
	username = strings.TrimSpace(username)
	canonicalName = cleanText(canonicalName)
	whatsappJID = strings.TrimSpace(whatsappJID)
	meetAlias = cleanText(meetAlias)
	if !usernamePattern.MatchString(username) {
		return Person{}, ErrInvalidUsername
	}
	if canonicalName == "" || utf8.RuneCountInString(canonicalName) > 200 {
		return Person{}, ErrInvalidName
	}
	if whatsappJID == "" || utf8.RuneCountInString(whatsappJID) > 255 {
		return Person{}, ErrInvalidJID
	}
	if meetAlias == "" || utf8.RuneCountInString(meetAlias) > 200 {
		return Person{}, ErrInvalidAlias
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Person{}, fmt.Errorf("begin add mapping: %w", err)
	}
	defer tx.Rollback()

	if exists(ctx, tx, "SELECT 1 FROM people WHERE username = ? COLLATE NOCASE", username) {
		return Person{}, ErrUsernameTaken
	}
	if exists(ctx, tx, "SELECT 1 FROM people WHERE whatsapp_jid = ?", whatsappJID) {
		return Person{}, ErrWhatsAppTaken
	}
	normalizedAlias := normalizeAlias(meetAlias)
	if exists(ctx, tx, "SELECT 1 FROM meet_aliases WHERE normalized_alias = ?", normalizedAlias) {
		return Person{}, ErrAliasTaken
	}

	now := s.now().UTC().UnixNano()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO people (username, canonical_name, whatsapp_jid, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, username, canonicalName, whatsappJID, now, now)
	if err != nil {
		return Person{}, fmt.Errorf("insert person mapping: %w", err)
	}
	personID, err := result.LastInsertId()
	if err != nil {
		return Person{}, fmt.Errorf("read person mapping ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meet_aliases (person_id, alias, normalized_alias, created_at)
		VALUES (?, ?, ?, ?)
	`, personID, meetAlias, normalizedAlias, now); err != nil {
		return Person{}, fmt.Errorf("insert Meet alias: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Person{}, fmt.Errorf("commit person mapping: %w", err)
	}
	return Person{
		ID:            personID,
		Username:      username,
		CanonicalName: canonicalName,
		WhatsAppJID:   whatsappJID,
		MeetAliases:   []string{meetAlias},
	}, nil
}

func (s *Store) AddAlias(ctx context.Context, username, alias string) error {
	username = strings.TrimSpace(username)
	alias = cleanText(alias)
	if !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}
	if alias == "" || utf8.RuneCountInString(alias) > 200 {
		return ErrInvalidAlias
	}
	var personID int64
	if err := s.db.QueryRowContext(
		ctx,
		"SELECT id FROM people WHERE username = ? COLLATE NOCASE",
		username,
	).Scan(&personID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("find person mapping: %w", err)
	}
	normalizedAlias := normalizeAlias(alias)
	if exists(ctx, s.db, "SELECT 1 FROM meet_aliases WHERE normalized_alias = ?", normalizedAlias) {
		return ErrAliasTaken
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meet_aliases (person_id, alias, normalized_alias, created_at)
		VALUES (?, ?, ?, ?)
	`, personID, alias, normalizedAlias, s.now().UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("insert Meet alias: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, username string) (Person, error) {
	username = strings.TrimSpace(username)
	var person Person
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, canonical_name, whatsapp_jid
		FROM people
		WHERE username = ? COLLATE NOCASE
	`, username).Scan(
		&person.ID,
		&person.Username,
		&person.CanonicalName,
		&person.WhatsAppJID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	if err != nil {
		return Person{}, fmt.Errorf("get person mapping: %w", err)
	}
	person.MeetAliases, err = aliases(ctx, s.db, person.ID)
	if err != nil {
		return Person{}, err
	}
	return person, nil
}

func (s *Store) GetByWhatsAppJID(ctx context.Context, whatsappJID string) (Person, error) {
	var person Person
	err := s.db.QueryRowContext(ctx, `
		SELECT id, username, canonical_name, whatsapp_jid
		FROM people
		WHERE whatsapp_jid = ?
	`, strings.TrimSpace(whatsappJID)).Scan(
		&person.ID,
		&person.Username,
		&person.CanonicalName,
		&person.WhatsAppJID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	if err != nil {
		return Person{}, fmt.Errorf("get person mapping by WhatsApp identity: %w", err)
	}
	person.MeetAliases, err = aliases(ctx, s.db, person.ID)
	if err != nil {
		return Person{}, err
	}
	return person, nil
}

func (s *Store) List(ctx context.Context) ([]Person, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, canonical_name, whatsapp_jid
		FROM people
		ORDER BY username COLLATE NOCASE
	`)
	if err != nil {
		return nil, fmt.Errorf("list person mappings: %w", err)
	}
	people := make([]Person, 0)
	for rows.Next() {
		var person Person
		if err := rows.Scan(
			&person.ID,
			&person.Username,
			&person.CanonicalName,
			&person.WhatsAppJID,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan person mapping: %w", err)
		}
		people = append(people, person)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate person mappings: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close person mappings: %w", err)
	}
	for index := range people {
		people[index].MeetAliases, err = aliases(ctx, s.db, people[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return people, nil
}

func (s *Store) Delete(ctx context.Context, username string) error {
	result, err := s.db.ExecContext(
		ctx,
		"DELETE FROM people WHERE username = ? COLLATE NOCASE",
		strings.TrimSpace(username),
	)
	if err != nil {
		return fmt.Errorf("delete person mapping: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted mapping count: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func aliases(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, personID int64) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT alias
		FROM meet_aliases
		WHERE person_id = ?
		ORDER BY normalized_alias
	`, personID)
	if err != nil {
		return nil, fmt.Errorf("list Meet aliases: %w", err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, fmt.Errorf("scan Meet alias: %w", err)
		}
		result = append(result, alias)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Meet aliases: %w", err)
	}
	return result, nil
}

func exists(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any) bool {
	var marker int
	return queryer.QueryRowContext(ctx, query, args...).Scan(&marker) == nil
}

func cleanText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeAlias(value string) string {
	return strings.ToLower(cleanText(value))
}
