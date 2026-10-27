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
	"whatsappBot/outbound"
	"whatsappBot/scheduler"
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

	err := connectClient(
		context.Background(),
		client,
		func() bool { return false },
		func(string) {},
		newAuthenticationWaiter(client),
	)
	if err == nil || !strings.Contains(err.Error(), "QR channel closed") {
		t.Fatalf("connectClient error = %v, want QR channel closure failure", err)
	}
}

func TestRunWaitsForActiveSchedulerWorkBeforeCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		CREATE TABLE reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			message TEXT NOT NULL,
			remind_at DATETIME NOT NULL
		);
		CREATE TABLE birthdays (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL,
			name TEXT NOT NULL,
			birth_date DATE NOT NULL
		);
		CREATE TABLE birthday_deliveries (
			birthday_id INTEGER NOT NULL,
			delivery_year INTEGER NOT NULL,
			delivered_at DATETIME NOT NULL,
			PRIMARY KEY (birthday_id, delivery_year)
		);
		INSERT INTO reminders (chat_id, to_id, message, remind_at)
		VALUES ('123@g.us', '456@s.whatsapp.net', 'blocked reminder', ?);
	`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	deviceID := types.NewJID("123", types.DefaultUserServer)
	client := &fakeLifecycleClient{
		sendStarted: make(chan struct{}),
		releaseSend: make(chan struct{}),
	}

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
		startScheduler: func(ctx context.Context, database *sql.DB, sender utils.MessageSender, _ *outbound.Store) <-chan struct{} {
			if !client.IsConnected() {
				t.Error("scheduler started before client connected")
			}
			return scheduler.NewScheduler(
				scheduler.NewRegistry(database),
				sender,
				time.Nanosecond,
			).Start(ctx)
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
	case <-client.sendStarted:
	case <-time.After(time.Second):
		t.Fatal("scheduler work did not start")
	}
	cancel()

	select {
	case err := <-done:
		t.Fatalf("runWithDependencies returned before scheduler work exited: %v", err)
	default:
	}
	if client.isDisconnected() {
		t.Fatal("client disconnected while scheduler work was active")
	}
	if err := database.Ping(); err != nil {
		t.Fatalf("database closed while scheduler work was active: %v", err)
	}

	close(client.releaseSend)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWithDependencies returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithDependencies did not stop after scheduler work exited")
	}

	if !client.isDisconnected() {
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
		startScheduler: func(context.Context, *sql.DB, utils.MessageSender, *outbound.Store) <-chan struct{} {
			t.Error("scheduler started after tone loading failure")
			return nil
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

func TestRunWithAPIDisabledPreservesReminderOnlyStartup(t *testing.T) {
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
		newClient: func(*store.Device, config.Config) lifecycleClient { return client },
		newToneManager: func(utils.MessageSender) (toneResponder, error) {
			return &fakeToneResponder{}, nil
		},
		newOutboundStore: func(*sql.DB, string) *outbound.Store {
			t.Error("outbound store created while API disabled")
			return nil
		},
		startAPI: func(context.Context, config.Config, *outbound.Store) <-chan error {
			t.Error("API started while disabled")
			return nil
		},
		startScheduler: func(ctx context.Context, _ *sql.DB, _ utils.MessageSender, outboundStore *outbound.Store) <-chan struct{} {
			if outboundStore != nil {
				t.Error("disabled scheduler received outbound store")
			}
			close(schedulerStarted)
			done := make(chan struct{})
			go func() {
				<-ctx.Done()
				close(done)
			}()
			return done
		},
		renderQR: func(string) {},
	}

	done := make(chan error, 1)
	go func() { done <- runWithDependencies(ctx, baseTestConfig(false), deps) }()
	<-schedulerStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithDependencies error = %v", err)
	}
}

func TestRunEnabledAPIStartsAfterAuthenticationAndRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	deviceID := types.NewJID("123", types.DefaultUserServer)
	client := &fakeLifecycleClient{}
	client.loggedIn = true
	events := make(chan string, 4)
	apiDone := make(chan error, 1)
	schedulerDone := make(chan struct{})

	deps := runtimeDependencies{
		setupStore: func(context.Context, string) (*store.Device, *sql.DB, error) {
			return &store.Device{ID: &deviceID}, database, nil
		},
		newClient: func(*store.Device, config.Config) lifecycleClient { return client },
		newToneManager: func(utils.MessageSender) (toneResponder, error) {
			return &fakeToneResponder{}, nil
		},
		newOutboundStore: func(database *sql.DB, target string) *outbound.Store {
			events <- "store"
			return outbound.NewStore(database, target)
		},
		recoverSending: func(context.Context, *outbound.Store) (int64, error) {
			events <- "recover"
			return 1, nil
		},
		startAPI: func(context.Context, config.Config, *outbound.Store) <-chan error {
			if !client.IsConnected() {
				t.Error("API started before WhatsApp authentication")
			}
			events <- "api"
			return apiDone
		},
		startScheduler: func(context.Context, *sql.DB, utils.MessageSender, *outbound.Store) <-chan struct{} {
			events <- "scheduler"
			return schedulerDone
		},
		renderQR: func(string) {},
	}

	done := make(chan error, 1)
	go func() { done <- runWithDependencies(ctx, baseTestConfig(true), deps) }()
	startupEvents := make([]string, 0, 4)
	for len(startupEvents) < 4 {
		select {
		case event := <-events:
			startupEvents = append(startupEvents, event)
		case <-time.After(time.Second):
			t.Fatalf("startup events = %q, want four events", strings.Join(startupEvents, ","))
		}
	}
	if got := strings.Join(startupEvents, ","); got != "store,recover,api,scheduler" {
		t.Fatalf("startup events = %q", got)
	}

	cancel()
	apiDone <- nil
	close(apiDone)
	close(schedulerDone)
	if err := <-done; err != nil {
		t.Fatalf("runWithDependencies error = %v", err)
	}
}

func TestRunRejectsConnectedClientThatIsNotLoggedIn(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	deviceID := types.NewJID("123", types.DefaultUserServer)
	client := &fakeLifecycleClient{
		deferLogin: true,
		connectEvents: []interface{}{
			&events.Connected{},
			&events.Disconnected{},
		},
	}
	apiStarted := false
	schedulerStarted := false
	deps := enabledRuntimeDependencies(database, &deviceID, client)
	deps.startAPI = func(context.Context, config.Config, *outbound.Store) <-chan error {
		apiStarted = true
		done := make(chan error, 1)
		done <- errors.New("API must not start")
		return done
	}
	deps.startScheduler = func(context.Context, *sql.DB, utils.MessageSender, *outbound.Store) <-chan struct{} {
		schedulerStarted = true
		done := make(chan struct{})
		close(done)
		return done
	}

	err = runWithDependencies(context.Background(), baseTestConfig(true), deps)
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("runWithDependencies error = %v, want authentication failure", err)
	}
	if apiStarted || schedulerStarted {
		t.Fatalf("components started before login: API=%t scheduler=%t", apiStarted, schedulerStarted)
	}
}

func TestRunStartsComponentsAfterLoggedInEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	deviceID := types.NewJID("123", types.DefaultUserServer)
	connectComplete := make(chan struct{})
	client := &fakeLifecycleClient{
		deferLogin:      true,
		connectComplete: connectComplete,
	}
	apiDone := make(chan error, 1)
	schedulerDone := make(chan struct{})
	schedulerStarted := make(chan struct{})
	deps := enabledRuntimeDependencies(database, &deviceID, client)
	deps.startAPI = func(context.Context, config.Config, *outbound.Store) <-chan error {
		if !client.IsLoggedIn() {
			t.Error("API started before login")
		}
		return apiDone
	}
	deps.startScheduler = func(context.Context, *sql.DB, utils.MessageSender, *outbound.Store) <-chan struct{} {
		if !client.IsLoggedIn() {
			t.Error("scheduler started before login")
		}
		close(schedulerStarted)
		return schedulerDone
	}

	done := make(chan error, 1)
	go func() { done <- runWithDependencies(ctx, baseTestConfig(true), deps) }()
	<-connectComplete
	client.setLoggedIn(true)
	client.emit(&events.Connected{})
	<-schedulerStarted
	cancel()
	apiDone <- nil
	close(apiDone)
	close(schedulerDone)
	if err := <-done; err != nil {
		t.Fatalf("runWithDependencies error = %v", err)
	}
}

func TestRunFreshPairStartsComponentsAfterLoggedInEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	connectComplete := make(chan struct{})
	client := &fakeLifecycleClient{
		qr:              make(chan whatsmeow.QRChannelItem),
		deferLogin:      true,
		connectComplete: connectComplete,
	}
	apiDone := make(chan error, 1)
	schedulerDone := make(chan struct{})
	schedulerStarted := make(chan struct{})
	deps := enabledRuntimeDependencies(database, nil, client)
	deps.startAPI = func(context.Context, config.Config, *outbound.Store) <-chan error {
		return apiDone
	}
	deps.startScheduler = func(context.Context, *sql.DB, utils.MessageSender, *outbound.Store) <-chan struct{} {
		close(schedulerStarted)
		return schedulerDone
	}

	done := make(chan error, 1)
	go func() { done <- runWithDependencies(ctx, baseTestConfig(true), deps) }()
	<-connectComplete
	client.setLoggedIn(true)
	client.emit(&events.Connected{})
	<-schedulerStarted
	cancel()
	apiDone <- nil
	close(apiDone)
	close(schedulerDone)
	if err := <-done; err != nil {
		t.Fatalf("runWithDependencies error = %v", err)
	}
}

func TestRunContextCancellationWhileWaitingForLoginStartsNoComponents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	deviceID := types.NewJID("123", types.DefaultUserServer)
	connectComplete := make(chan struct{})
	client := &fakeLifecycleClient{
		deferLogin:      true,
		connectComplete: connectComplete,
	}
	apiStarted := false
	schedulerStarted := false
	deps := enabledRuntimeDependencies(database, &deviceID, client)
	deps.startAPI = func(context.Context, config.Config, *outbound.Store) <-chan error {
		apiStarted = true
		return nil
	}
	deps.startScheduler = func(context.Context, *sql.DB, utils.MessageSender, *outbound.Store) <-chan struct{} {
		schedulerStarted = true
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- runWithDependencies(ctx, baseTestConfig(true), deps) }()
	<-connectComplete
	cancel()
	err = <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runWithDependencies error = %v, want context cancellation", err)
	}
	if apiStarted || schedulerStarted {
		t.Fatalf("components started before canceled login: API=%t scheduler=%t", apiStarted, schedulerStarted)
	}
}

func TestRunCancelsAPIBeforeSchedulerAndCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	deviceID := types.NewJID("123", types.DefaultUserServer)
	client := &fakeLifecycleClient{}
	client.loggedIn = true
	apiRelease := make(chan struct{})
	schedulerRelease := make(chan struct{})
	apiCheckedScheduler := make(chan bool, 1)
	schedulerCanceled := make(chan struct{})
	schedulerStarted := make(chan struct{})
	var schedulerCtx context.Context

	deps := enabledRuntimeDependencies(database, &deviceID, client)
	deps.startAPI = func(apiCtx context.Context, _ config.Config, _ *outbound.Store) <-chan error {
		done := make(chan error, 1)
		go func() {
			<-apiCtx.Done()
			apiCheckedScheduler <- schedulerCtx.Err() != nil
			<-apiRelease
			done <- nil
			close(done)
		}()
		return done
	}
	deps.startScheduler = func(ctx context.Context, _ *sql.DB, _ utils.MessageSender, _ *outbound.Store) <-chan struct{} {
		schedulerCtx = ctx
		close(schedulerStarted)
		done := make(chan struct{})
		go func() {
			<-ctx.Done()
			close(schedulerCanceled)
			<-schedulerRelease
			close(done)
		}()
		return done
	}

	done := make(chan error, 1)
	go func() { done <- runWithDependencies(ctx, baseTestConfig(true), deps) }()
	<-schedulerStarted
	cancel()
	if canceledTooEarly := <-apiCheckedScheduler; canceledTooEarly {
		t.Fatal("scheduler context canceled before API completed")
	}
	assertRuntimeStillOpen(t, done, client, database)

	close(apiRelease)
	<-schedulerCanceled
	assertRuntimeStillOpen(t, done, client, database)

	close(schedulerRelease)
	if err := <-done; err != nil {
		t.Fatalf("runWithDependencies error = %v", err)
	}
	if !client.isDisconnected() {
		t.Fatal("client was not disconnected")
	}
	if err := database.Ping(); err == nil {
		t.Fatal("database remained open")
	}
}

func TestRunAPIStartupFailureCleansUpWithoutStartingScheduler(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	deviceID := types.NewJID("123", types.DefaultUserServer)
	client := &fakeLifecycleClient{}
	want := errors.New("bind failed")
	deps := enabledRuntimeDependencies(database, &deviceID, client)
	deps.startAPI = func(context.Context, config.Config, *outbound.Store) <-chan error {
		done := make(chan error, 1)
		done <- want
		close(done)
		return done
	}
	deps.startScheduler = func(context.Context, *sql.DB, utils.MessageSender, *outbound.Store) <-chan struct{} {
		t.Error("scheduler started after API startup failure")
		return nil
	}

	err = runWithDependencies(context.Background(), baseTestConfig(true), deps)
	if !errors.Is(err, want) {
		t.Fatalf("runWithDependencies error = %v, want wrapped bind error", err)
	}
	if !client.isDisconnected() {
		t.Fatal("client was not disconnected after API startup failure")
	}
	if err := database.Ping(); err == nil {
		t.Fatal("database remained open after API startup failure")
	}
}

func enabledRuntimeDependencies(database *sql.DB, deviceID *types.JID, client *fakeLifecycleClient) runtimeDependencies {
	return runtimeDependencies{
		setupStore: func(context.Context, string) (*store.Device, *sql.DB, error) {
			return &store.Device{ID: deviceID}, database, nil
		},
		newClient: func(*store.Device, config.Config) lifecycleClient { return client },
		newToneManager: func(utils.MessageSender) (toneResponder, error) {
			return &fakeToneResponder{}, nil
		},
		newOutboundStore: func(database *sql.DB, target string) *outbound.Store {
			return outbound.NewStore(database, target)
		},
		recoverSending: func(context.Context, *outbound.Store) (int64, error) { return 0, nil },
		renderQR:       func(string) {},
	}
}

func baseTestConfig(apiEnabled bool) config.Config {
	return config.Config{
		AdminNumbers:   []string{"456"},
		AllowedGroups:  []string{"123@g.us"},
		DBPath:         "ignored.db",
		LogLevel:       "INFO",
		APIEnabled:     apiEnabled,
		APIAddr:        "127.0.0.1:0",
		APIToken:       "01234567890123456789012345678901",
		APITargetGroup: "123@g.us",
	}
}

func assertRuntimeStillOpen(t *testing.T, done <-chan error, client *fakeLifecycleClient, database *sql.DB) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("runWithDependencies returned before lifecycle waits completed: %v", err)
	default:
	}
	if client.isDisconnected() {
		t.Fatal("client disconnected before lifecycle waits completed")
	}
	if err := database.Ping(); err != nil {
		t.Fatalf("database closed before lifecycle waits completed: %v", err)
	}
}

type fakeLifecycleClient struct {
	mu              sync.Mutex
	qr              chan whatsmeow.QRChannelItem
	connectErr      error
	deferLogin      bool
	connectCalls    int
	connected       bool
	loggedIn        bool
	disconnected    bool
	handler         whatsmeow.EventHandler
	connectEvents   []interface{}
	connectComplete chan struct{}
	sendStarted     chan struct{}
	releaseSend     chan struct{}
	sendOnce        sync.Once
}

func (f *fakeLifecycleClient) AddEventHandler(handler whatsmeow.EventHandler) uint32 {
	f.handler = handler
	return 1
}

func (f *fakeLifecycleClient) Connect() error {
	f.mu.Lock()
	f.connectCalls++
	if f.connectErr == nil && f.qr == nil {
		f.connected = true
		if !f.deferLogin {
			f.loggedIn = true
		}
	}
	connectErr := f.connectErr
	connectEvents := append([]interface{}(nil), f.connectEvents...)
	f.mu.Unlock()

	for _, event := range connectEvents {
		f.emit(event)
	}
	if f.connectComplete != nil {
		close(f.connectComplete)
	}
	return connectErr
}

func (f *fakeLifecycleClient) Disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = false
	f.loggedIn = false
	f.disconnected = true
}

func (f *fakeLifecycleClient) isDisconnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.disconnected
}

func (f *fakeLifecycleClient) GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return f.qr, nil
}

func (f *fakeLifecycleClient) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeLifecycleClient) IsLoggedIn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loggedIn
}

func (f *fakeLifecycleClient) setLoggedIn(loggedIn bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = loggedIn
	f.loggedIn = loggedIn
}

func (f *fakeLifecycleClient) emit(event interface{}) {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(event)
	}
}

func (f *fakeLifecycleClient) SendMessage(
	context.Context,
	types.JID,
	*waProto.Message,
	...whatsmeow.SendRequestExtra,
) (whatsmeow.SendResponse, error) {
	if f.sendStarted != nil {
		f.sendOnce.Do(func() { close(f.sendStarted) })
		<-f.releaseSend
	}
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
