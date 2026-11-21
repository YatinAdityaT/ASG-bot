package bothealth

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsappBot/outbound"

	_ "modernc.org/sqlite"
)

func TestStoreDistinguishesGracefulAndUnexpectedRuns(t *testing.T) {
	store := newTestStore(t)
	firstStart := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

	previous, err := store.Begin(context.Background(), firstStart)
	if err != nil {
		t.Fatal(err)
	}
	if previous != nil {
		t.Fatalf("first previous run = %#v, want nil", previous)
	}
	lastBeat := firstStart.Add(2 * time.Minute)
	if err := store.Heartbeat(context.Background(), lastBeat); err != nil {
		t.Fatal(err)
	}

	previous, err = store.Begin(context.Background(), firstStart.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || previous.StoppedGracefully ||
		!previous.LastHeartbeatAt.Equal(lastBeat) {
		t.Fatalf("unexpected previous run: %#v", previous)
	}

	if err := store.MarkGraceful(
		context.Background(),
		firstStart.Add(12*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	previous, err = store.Begin(context.Background(), firstStart.Add(13*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || !previous.StoppedGracefully {
		t.Fatalf("graceful previous run = %#v", previous)
	}
}

func TestMessagesExplainRecoveryAndHeartbeat(t *testing.T) {
	lastBeat := time.Date(2026, 11, 21, 10, 19, 0, 0, time.UTC)
	recovered := lastBeat.Add(11 * time.Hour)
	startup := StartupMessage(&PreviousRun{
		LastHeartbeatAt: lastBeat,
	}, recovered)
	for _, required := range []string{
		"unexpected stop",
		"21 Nov 2026, 3:49 PM IST",
		"22 Nov 2026, 2:49 AM IST",
		"11h",
	} {
		if !strings.Contains(startup, required) {
			t.Fatalf("startup message %q missing %q", startup, required)
		}
	}

	heartbeat := HeartbeatMessage(recovered, recovered.Add(65*time.Minute))
	if !strings.Contains(heartbeat, "alive") ||
		!strings.Contains(heartbeat, "1h 5m") {
		t.Fatalf("heartbeat message = %q", heartbeat)
	}
}

func TestRunnerQueuesStartupAndPeriodicHeartbeat(t *testing.T) {
	store := newTestStore(t)
	startedAt := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	if _, err := store.Begin(context.Background(), startedAt); err != nil {
		t.Fatal(err)
	}
	queue := &fakeQueue{created: make(chan outbound.CreateRequest, 4)}
	runner := NewRunner(
		store,
		queue,
		WithClock(func() time.Time { return startedAt.Add(time.Hour) }),
		WithIntervals(5*time.Millisecond, 10*time.Millisecond),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := runner.Start(ctx, nil, startedAt)

	first := waitForRequest(t, queue.created)
	if !strings.Contains(first.Text, "monitoring started") {
		t.Fatalf("startup request = %#v", first)
	}
	second := waitForRequest(t, queue.created)
	if !strings.Contains(second.Text, "heartbeat: alive") {
		t.Fatalf("heartbeat request = %#v", second)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
}

type fakeQueue struct {
	mu      sync.Mutex
	created chan outbound.CreateRequest
}

func (q *fakeQueue) Create(
	_ context.Context,
	request outbound.CreateRequest,
) (*outbound.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.created <- request
	return &outbound.Job{}, nil
}

func waitForRequest(
	t *testing.T,
	requests <-chan outbound.CreateRequest,
) outbound.CreateRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for heartbeat request")
		return outbound.CreateRequest{}
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(database)
	t.Cleanup(func() {
		store.Close()
		database.Close()
	})
	return store
}
