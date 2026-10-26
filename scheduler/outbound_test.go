package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	appdb "whatsappBot/db"
	"whatsappBot/outbound"
)

const (
	outboundTargetGroup = "120363418911552290@g.us"
	outboundMessageID   = "0123456789ABCDEFABCD"
)

type outboundSend struct {
	chat    types.JID
	message *waProto.Message
	extra   []whatsmeow.SendRequestExtra
}

type outboundSender struct {
	mu      sync.Mutex
	sends   []outboundSend
	err     error
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *outboundSender) SendMessage(
	_ context.Context,
	chat types.JID,
	message *waProto.Message,
	extra ...whatsmeow.SendRequestExtra,
) (whatsmeow.SendResponse, error) {
	s.mu.Lock()
	s.sends = append(s.sends, outboundSend{chat: chat, message: message, extra: extra})
	s.mu.Unlock()
	if s.started != nil {
		s.once.Do(func() { close(s.started) })
		<-s.release
	}
	return whatsmeow.SendResponse{}, s.err
}

func (s *outboundSender) snapshot() []outboundSend {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]outboundSend(nil), s.sends...)
}

func TestNewRegistryAddsOutboundProviderWhenStoreSupplied(t *testing.T) {
	database := openSchedulerDB(t)
	store := outbound.NewStore(database, outboundTargetGroup)

	registry := NewRegistry(database, store)

	if len(registry.Providers) != 2 {
		t.Fatalf("provider count = %d, want 2", len(registry.Providers))
	}
	if _, ok := registry.Providers[0].(*ReminderProvider); !ok {
		t.Fatalf("first provider type = %T, want *ReminderProvider", registry.Providers[0])
	}
	if _, ok := registry.Providers[1].(*OutboundProvider); !ok {
		t.Fatalf("second provider type = %T, want *OutboundProvider", registry.Providers[1])
	}
}

func TestOutboundTaskSendsStoredConversationAndStableIDThenMarksSent(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store := newSchedulerOutboundStore(t, &now)
	job := createSchedulerOutboundJob(t, store, "success", "Exact text, unchanged.", now.Add(-time.Minute))
	tasks := dueOutboundTasks(t, store, now)
	sender := &outboundSender{}

	if err := tasks[0].Run(context.Background(), sender); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := tasks[0].Run(context.Background(), sender); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	sends := sender.snapshot()
	if len(sends) != 1 {
		t.Fatalf("send count = %d, want 1", len(sends))
	}
	wantChat, err := types.ParseJID(outboundTargetGroup)
	if err != nil {
		t.Fatal(err)
	}
	if sends[0].chat != wantChat {
		t.Fatalf("send chat = %v, want %v", sends[0].chat, wantChat)
	}
	if got := sends[0].message.GetConversation(); got != job.Message {
		t.Fatalf("conversation = %q, want %q", got, job.Message)
	}
	if sends[0].message.ExtendedTextMessage != nil {
		t.Fatal("message used ExtendedTextMessage, want Conversation")
	}
	if len(sends[0].extra) != 1 || sends[0].extra[0].ID != types.MessageID(job.MessageID) {
		t.Fatalf("send extra = %#v, want stable ID %q", sends[0].extra, job.MessageID)
	}
	got := getSchedulerOutboundJob(t, store, job.ID)
	if got.Status != outbound.StatusSent || got.SentAt == nil || got.Attempts != 0 {
		t.Fatalf("completed job = %#v, want sent with zero failed attempts", got)
	}
}

func TestOutboundTaskFailureReturnsErrorAndAppliesRetries(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store := newSchedulerOutboundStore(t, &now)
	job := createSchedulerOutboundJob(t, store, "retry", "retry me", now)
	sendErr := errors.New("WhatsApp unavailable")
	sender := &outboundSender{err: sendErr}
	backoffs := []time.Duration{
		5 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
	}

	for attempt := 1; attempt <= 5; attempt++ {
		task := dueOutboundTasks(t, store, now)[0]
		err := task.Run(context.Background(), sender)
		if !errors.Is(err, sendErr) {
			t.Fatalf("attempt %d Run() error = %v, want send error", attempt, err)
		}

		got := getSchedulerOutboundJob(t, store, job.ID)
		if got.Attempts != attempt || got.LastError == nil || *got.LastError != sendErr.Error() {
			t.Fatalf("attempt %d state = %#v, want retained send error", attempt, got)
		}
		if attempt < 5 {
			wantNext := now.Add(backoffs[attempt-1])
			if got.Status != outbound.StatusPending || !got.NextAttemptAt.Equal(wantNext) {
				t.Fatalf("attempt %d retry = %#v, want pending at %v", attempt, got, wantNext)
			}
			now = wantNext
		} else if got.Status != outbound.StatusFailed {
			t.Fatalf("attempt 5 status = %q, want failed", got.Status)
		}
	}
	if got := len(sender.snapshot()); got != 5 {
		t.Fatalf("send count = %d, want 5", got)
	}
}

func TestOutboundTaskClaimsBeforeSendToPreventConcurrentDuplicates(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store := newSchedulerOutboundStore(t, &now)
	createSchedulerOutboundJob(t, store, "concurrent", "only once", now)
	tasks := dueOutboundTasks(t, store, now)
	sender := &outboundSender{started: make(chan struct{}), release: make(chan struct{})}

	firstDone := make(chan error, 1)
	go func() { firstDone <- tasks[0].Run(context.Background(), sender) }()
	select {
	case <-sender.started:
	case <-time.After(time.Second):
		t.Fatal("first send did not start")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- tasks[0].Run(context.Background(), sender) }()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("duplicate Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("duplicate Run() blocked instead of skipping an unclaimed job")
	}
	close(sender.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if got := len(sender.snapshot()); got != 1 {
		t.Fatalf("send count = %d, want 1", got)
	}
}

func TestOutboundProviderExposesRecoveredSendingJob(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	store := newSchedulerOutboundStore(t, &now)
	job := createSchedulerOutboundJob(t, store, "recover", "recover me", now.Add(-time.Minute))
	if claimed, err := store.Claim(context.Background(), job.ID); err != nil || !claimed {
		t.Fatalf("Claim() = %v, %v; want true, nil", claimed, err)
	}
	provider := NewOutboundProvider(store)
	if tasks, err := provider.GetDueTasks(context.Background(), now); err != nil || len(tasks) != 0 {
		t.Fatalf("tasks before recovery = %d, %v; want 0, nil", len(tasks), err)
	}

	if recovered, err := store.RecoverSending(context.Background()); err != nil || recovered != 1 {
		t.Fatalf("RecoverSending() = %d, %v; want 1, nil", recovered, err)
	}
	if tasks, err := provider.GetDueTasks(context.Background(), now); err != nil || len(tasks) != 1 {
		t.Fatalf("tasks after recovery = %d, %v; want 1, nil", len(tasks), err)
	}
}

func newSchedulerOutboundStore(t *testing.T, now *time.Time) *outbound.Store {
	t.Helper()
	_, database, err := appdb.SetupDB(context.Background(), t.TempDir()+"/scheduler-outbound.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return outbound.NewStore(
		database,
		outboundTargetGroup,
		outbound.WithClock(func() time.Time { return *now }),
		outbound.WithMessageIDGenerator(func() (string, error) { return outboundMessageID, nil }),
	)
}

func createSchedulerOutboundJob(
	t *testing.T,
	store *outbound.Store,
	key string,
	text string,
	sendAt time.Time,
) *outbound.Job {
	t.Helper()
	job, err := store.Create(context.Background(), outbound.CreateRequest{
		IdempotencyKey: key,
		Text:           text,
		SendAt:         sendAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func dueOutboundTasks(t *testing.T, store *outbound.Store, now time.Time) []ScheduledTask {
	t.Helper()
	tasks, err := NewOutboundProvider(store).GetDueTasks(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("due task count = %d, want 1", len(tasks))
	}
	return tasks
}

func getSchedulerOutboundJob(t *testing.T, store *outbound.Store, id int64) *outbound.Job {
	t.Helper()
	job, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
