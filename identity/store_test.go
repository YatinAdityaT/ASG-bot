package identity_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	appdb "whatsappBot/db"
	"whatsappBot/identity"
)

func TestStoreAddsListsAndDeletesMappings(t *testing.T) {
	store := newStore(t)
	person, err := store.Add(
		t.Context(),
		"aditi",
		"Aditi Sharma",
		"123@s.whatsapp.net",
		"Aditi",
	)
	if err != nil {
		t.Fatal(err)
	}
	if person.Username != "aditi" || len(person.MeetAliases) != 1 {
		t.Fatalf("person = %#v", person)
	}
	if err := store.AddAlias(t.Context(), "aditi", "Aditi S"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context(), "ADITI")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MeetAliases) != 2 {
		t.Fatalf("aliases = %#v", got.MeetAliases)
	}
	people, err := store.List(t.Context())
	if err != nil || len(people) != 1 {
		t.Fatalf("people = %#v, err = %v", people, err)
	}
	if err := store.Delete(t.Context(), "aditi"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), "aditi"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("Get() error = %v, want not found", err)
	}
}

func TestStoreRejectsAmbiguousMappings(t *testing.T) {
	store := newStore(t)
	if _, err := store.Add(
		t.Context(), "aditi", "Aditi", "123@s.whatsapp.net", "Aditi",
	); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		username string
		jid      string
		alias    string
		want     error
	}{
		{"username", "aditi", "456@s.whatsapp.net", "Different", identity.ErrUsernameTaken},
		{"WhatsApp", "different", "123@s.whatsapp.net", "Different", identity.ErrWhatsAppTaken},
		{"Meet alias", "different", "456@s.whatsapp.net", " aditi ", identity.ErrAliasTaken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.Add(t.Context(), test.username, "Different", test.jid, test.alias)
			if !errors.Is(err, test.want) {
				t.Fatalf("Add() error = %v, want %v", err, test.want)
			}
		})
	}
}

func newStore(t *testing.T) *identity.Store {
	t.Helper()
	_, database, err := appdb.SetupDB(
		context.Background(),
		filepath.Join(t.TempDir(), "identity.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return identity.NewStore(database)
}
