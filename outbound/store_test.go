package outbound_test

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	appdb "whatsappBot/db"
	"whatsappBot/outbound"
)

const (
	targetGroup    = "120363418911552290@g.us"
	otherGroup     = "120363400000000000@g.us"
	fixedMessageID = "0123456789ABCDEFABCD"
)

func TestCreateAndGetJob(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	sendAt := now.Add(time.Hour)
	store := newStore(t, targetGroup,
		outbound.WithClock(func() time.Time { return now }),
		outbound.WithMessageIDGenerator(func() (string, error) { return fixedMessageID, nil }),
	)

	created, err := store.Create(context.Background(), outbound.CreateRequest{
		IdempotencyKey: "request-1",
		Text:           "hello",
		SendAt:         sendAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.ChatID != targetGroup || created.Message != "hello" {
		t.Fatalf("unexpected created job: %#v", created)
	}
	if created.MessageID != fixedMessageID || created.Status != outbound.StatusPending || created.Attempts != 0 {
		t.Fatalf("unexpected initial state: %#v", created)
	}
	if !created.SendAt.Equal(sendAt) || !created.NextAttemptAt.Equal(sendAt) || !created.CreatedAt.Equal(now) {
		t.Fatalf("unexpected timestamps: %#v", created)
	}

	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *created {
		t.Fatalf("Get() = %#v, want %#v", got, created)
	}
}

func TestCreateUsesCurrentTimeForImmediateJobs(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store := newStore(t, targetGroup,
		outbound.WithClock(func() time.Time { return now }),
		outbound.WithMessageIDGenerator(func() (string, error) { return fixedMessageID, nil }),
	)

	job, err := store.Create(context.Background(), outbound.CreateRequest{
		IdempotencyKey: "immediate",
		Text:           "hello now",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !job.SendAt.Equal(now) || !job.NextAttemptAt.Equal(now) {
		t.Fatalf("immediate timestamps = %v, %v; want %v", job.SendAt, job.NextAttemptAt, now)
	}
}

func TestCreateGeneratesUppercaseTwentyHexMessageID(t *testing.T) {
	store := newStore(t, targetGroup)

	job, err := store.Create(context.Background(), outbound.CreateRequest{
		IdempotencyKey: "random-id",
		Text:           "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9A-F]{20}$`).MatchString(job.MessageID) {
		t.Fatalf("MessageID = %q, want 20 uppercase hexadecimal characters", job.MessageID)
	}
}

func TestCreateReplaysSameIdempotencyKey(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	generatorCalls := 0
	store := newStore(t, targetGroup,
		outbound.WithClock(func() time.Time { return now }),
		outbound.WithMessageIDGenerator(func() (string, error) {
			generatorCalls++
			return fixedMessageID, nil
		}),
	)
	request := outbound.CreateRequest{
		IdempotencyKey: "replay",
		Text:           "same",
		SendAt:         now.Add(time.Hour),
	}

	first, err := store.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if *second != *first {
		t.Fatalf("replay = %#v, want %#v", second, first)
	}
	if generatorCalls != 1 {
		t.Fatalf("message ID generator calls = %d, want 1", generatorCalls)
	}
}

func TestCreateRejectsConflictingIdempotencyKeyReuse(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	database := newDatabase(t)
	options := []outbound.Option{
		outbound.WithClock(func() time.Time { return now }),
		outbound.WithMessageIDGenerator(func() (string, error) { return fixedMessageID, nil }),
	}
	store := outbound.NewStore(database, targetGroup, options...)
	original := outbound.CreateRequest{IdempotencyKey: "conflict", Text: "same", SendAt: now.Add(time.Hour)}
	if _, err := store.Create(context.Background(), original); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		store   *outbound.Store
		request outbound.CreateRequest
	}{
		{name: "text", store: store, request: outbound.CreateRequest{IdempotencyKey: "conflict", Text: "different", SendAt: original.SendAt}},
		{name: "send time", store: store, request: outbound.CreateRequest{IdempotencyKey: "conflict", Text: original.Text, SendAt: original.SendAt.Add(time.Second)}},
		{name: "target", store: outbound.NewStore(database, otherGroup, options...), request: original},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.store.Create(context.Background(), tt.request); !errors.Is(err, outbound.ErrIdempotencyConflict) {
				t.Fatalf("Create() error = %v, want ErrIdempotencyConflict", err)
			}
		})
	}
}

func TestDueReturnsOnlyPendingDueJobsInOrder(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	ids := []string{
		"00000000000000000001",
		"00000000000000000002",
		"00000000000000000003",
	}
	nextID := 0
	store := newStore(t, targetGroup,
		outbound.WithClock(func() time.Time { return now }),
		outbound.WithMessageIDGenerator(func() (string, error) {
			id := ids[nextID]
			nextID++
			return id, nil
		}),
	)
	later, err := store.Create(context.Background(), outbound.CreateRequest{IdempotencyKey: "later", Text: "later", SendAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := store.Create(context.Background(), outbound.CreateRequest{IdempotencyKey: "earlier", Text: "earlier", SendAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), outbound.CreateRequest{IdempotencyKey: "future", Text: "future", SendAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	due, err := store.Due(context.Background(), now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 || due[0].ID != earlier.ID || due[1].ID != later.ID {
		t.Fatalf("Due() = %#v, want earlier then later", due)
	}
}

func TestClaimIsAtomic(t *testing.T) {
	store := newStore(t, targetGroup,
		outbound.WithMessageIDGenerator(func() (string, error) { return fixedMessageID, nil }),
	)
	job, err := store.Create(context.Background(), outbound.CreateRequest{IdempotencyKey: "claim", Text: "claim"})
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := store.Claim(context.Background(), job.ID, job.NextAttemptAt)
	if err != nil || !claimed {
		t.Fatalf("first Claim() = %v, %v; want true, nil", claimed, err)
	}
	claimed, err = store.Claim(context.Background(), job.ID, job.NextAttemptAt)
	if err != nil || claimed {
		t.Fatalf("second Claim() = %v, %v; want false, nil", claimed, err)
	}
	got, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != outbound.StatusSending {
		t.Fatalf("status = %q, want sending", got.Status)
	}
}

func TestMarkSentTransitionsSendingJob(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store := newStore(t, targetGroup,
		outbound.WithClock(func() time.Time { return now }),
		outbound.WithMessageIDGenerator(func() (string, error) { return fixedMessageID, nil }),
	)
	job, err := store.Create(context.Background(), outbound.CreateRequest{IdempotencyKey: "sent", Text: "sent"})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.Claim(context.Background(), job.ID, job.NextAttemptAt); err != nil || !claimed {
		t.Fatalf("Claim() = %v, %v", claimed, err)
	}
	if err := store.MarkSent(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != outbound.StatusSent || got.SentAt == nil || !got.SentAt.Equal(now) {
		t.Fatalf("sent job = %#v", got)
	}
}

func TestMarkFailedRetriesThenStopsAtFiveAttempts(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store := newStore(t, targetGroup,
		outbound.WithClock(func() time.Time { return now }),
		outbound.WithMessageIDGenerator(func() (string, error) { return fixedMessageID, nil }),
	)
	job, err := store.Create(context.Background(), outbound.CreateRequest{IdempotencyKey: "failed", Text: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	backoffs := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}
	for attempt := 1; attempt <= 5; attempt++ {
		if claimed, err := store.Claim(context.Background(), job.ID, now); err != nil || !claimed {
			t.Fatalf("attempt %d Claim() = %v, %v", attempt, claimed, err)
		}
		if err := store.MarkFailed(context.Background(), job.ID, "send failed"); err != nil {
			t.Fatalf("attempt %d MarkFailed(): %v", attempt, err)
		}
		got, err := store.Get(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Attempts != attempt || got.LastError == nil || *got.LastError != "send failed" {
			t.Fatalf("attempt %d state = %#v", attempt, got)
		}
		if attempt < 5 {
			if got.Status != outbound.StatusPending || !got.NextAttemptAt.Equal(now.Add(backoffs[attempt-1])) {
				t.Fatalf("attempt %d retry state = %#v", attempt, got)
			}
			now = got.NextAttemptAt
		} else if got.Status != outbound.StatusFailed {
			t.Fatalf("attempt 5 status = %q, want failed", got.Status)
		}
	}
}

func TestRecoverSendingReturnsJobsToPending(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store := newStore(t, targetGroup,
		outbound.WithClock(func() time.Time { return now }),
		outbound.WithMessageIDGenerator(func() (string, error) { return fixedMessageID, nil }),
	)
	job, err := store.Create(context.Background(), outbound.CreateRequest{IdempotencyKey: "recover", Text: "recover", SendAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.Claim(context.Background(), job.ID, job.NextAttemptAt); err != nil || !claimed {
		t.Fatalf("Claim() = %v, %v", claimed, err)
	}

	recovered, err := store.RecoverSending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("RecoverSending() = %d, want 1", recovered)
	}
	got, err := store.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != outbound.StatusPending || !got.NextAttemptAt.Equal(now) {
		t.Fatalf("recovered job = %#v", got)
	}
}

func TestGetMissingJob(t *testing.T) {
	store := newStore(t, targetGroup)
	if _, err := store.Get(context.Background(), 999); !errors.Is(err, outbound.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func newStore(t *testing.T, chatID string, options ...outbound.Option) *outbound.Store {
	t.Helper()
	return outbound.NewStore(newDatabase(t), chatID, options...)
}

func newDatabase(t *testing.T) *sql.DB {
	t.Helper()
	_, database, err := appdb.SetupDB(context.Background(), t.TempDir()+"/outbound.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return database
}
