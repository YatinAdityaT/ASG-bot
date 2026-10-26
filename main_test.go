package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsappBot/config"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	_ "modernc.org/sqlite"
)

type fakeMessageRouter struct {
	mu     sync.Mutex
	routed int
}

func (f *fakeMessageRouter) RouteMessage(*sql.DB, context.Context, *events.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routed++
	return nil
}

func (f *fakeMessageRouter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.routed
}

type fakeToneResponder struct {
	mu      sync.Mutex
	matched int
}

func (f *fakeToneResponder) MatchAndAutoRespond(context.Context, *events.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.matched++
	return nil
}

func (f *fakeToneResponder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.matched
}

func TestEventHandlerRoutesOnlyAllowedGroups(t *testing.T) {
	router := &fakeMessageRouter{}
	responder := &fakeToneResponder{}
	allowed := allowedGroupSet([]string{"123@g.us"})
	handler := eventHandler(nil, context.Background(), router, responder, allowed)

	handler(groupMessage("999@g.us"))
	if router.count() != 0 || responder.count() != 0 {
		t.Fatal("disallowed group was processed")
	}

	handler(groupMessage("123@g.us"))
	if router.count() != 1 || responder.count() != 1 {
		t.Fatalf("allowed group calls = router %d, tone %d; want 1 each", router.count(), responder.count())
	}
}

func TestConnectClientFailsWhenQRChannelClosesWithoutPairing(t *testing.T) {
	client := &fakeLifecycleClient{
		qr: make(chan whatsmeow.QRChannelItem),
	}
	close(client.qr)

	err := connectClient(context.Background(), client, func() bool { return false }, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "QR channel closed") {
		t.Fatalf("connectClient error = %v, want QR channel closure failure", err)
	}
}

func TestRunStartsSchedulerAfterConnectionAndCleansUpOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	deviceID := types.NewJID("123", types.DefaultUserServer)
	client := &fakeLifecycleClient{}
	schedulerStarted := make(chan struct{})

	deps := runtimeDependencies{
		setupStore: func(context.Context, string) (*store.Device, *sql.DB, error) {
			return &store.Device{ID: &deviceID}, database, nil
		},
		newClient: func(*store.Device, config.Config) lifecycleClient {
			return client
		},
		newToneManager: func(utils.MessageSender) (toneResponder, error) {
			return &fakeToneResponder{}, nil
		},
		startScheduler: func(context.Context, *sql.DB, utils.MessageSender) {
			if !client.IsConnected() {
				t.Error("scheduler started before client connected")
			}
			close(schedulerStarted)
		},
		renderQR: func(string) {},
	}

	done := make(chan error, 1)
	go func() {
		done <- runWithDependencies(ctx, config.Config{
			AdminNumbers:  []string{"456"},
			AllowedGroups: []string{"123@g.us"},
			DBPath:        "ignored.db",
			LogLevel:      "INFO",
		}, deps)
	}()

	select {
	case <-schedulerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not start")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithDependencies returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWithDependencies did not stop after cancellation")
	}

	if !client.disconnected {
		t.Fatal("client was not disconnected")
	}
	if err := database.Ping(); err == nil {
		t.Fatal("database remained open")
	}
}

func TestRunReturnsToneLoadingErrorBeforeConnecting(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeLifecycleClient{}
	want := errors.New("load tones")
	deps := runtimeDependencies{
		setupStore: func(context.Context, string) (*store.Device, *sql.DB, error) {
			return &store.Device{}, database, nil
		},
		newClient: func(*store.Device, config.Config) lifecycleClient {
			return client
		},
		newToneManager: func(utils.MessageSender) (toneResponder, error) {
			return nil, want
		},
		startScheduler: func(context.Context, *sql.DB, utils.MessageSender) {
			t.Error("scheduler started after tone loading failure")
		},
		renderQR: func(string) {},
	}

	err = runWithDependencies(context.Background(), config.Config{}, deps)
	if !errors.Is(err, want) {
		t.Fatalf("runWithDependencies error = %v, want wrapped tone error", err)
	}
	if client.connectCalls != 0 {
		t.Fatalf("Connect called %d times, want 0", client.connectCalls)
	}
	if err := database.Ping(); err == nil {
		t.Fatal("database remained open after startup failure")
	}
}

type fakeLifecycleClient struct {
	mu           sync.Mutex
	qr           chan whatsmeow.QRChannelItem
	connectErr   error
	connectCalls int
	connected    bool
	disconnected bool
	handler      whatsmeow.EventHandler
}

func (f *fakeLifecycleClient) AddEventHandler(handler whatsmeow.EventHandler) uint32 {
	f.handler = handler
	return 1
}

func (f *fakeLifecycleClient) Connect() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectCalls++
	if f.connectErr == nil && f.qr == nil {
		f.connected = true
	}
	return f.connectErr
}

func (f *fakeLifecycleClient) Disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = false
	f.disconnected = true
}

func (f *fakeLifecycleClient) GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return f.qr, nil
}

func (f *fakeLifecycleClient) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeLifecycleClient) SendMessage(
	context.Context,
	types.JID,
	*waProto.Message,
	...whatsmeow.SendRequestExtra,
) (whatsmeow.SendResponse, error) {
	return whatsmeow.SendResponse{}, nil
}

func groupMessage(jid string) *events.Message {
	chat, _ := types.ParseJID(jid)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat},
		},
	}
}
