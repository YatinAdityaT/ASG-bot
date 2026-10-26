package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"whatsappBot/cmd"
	"whatsappBot/config"
	"whatsappBot/db"
	"whatsappBot/messageapi"
	"whatsappBot/outbound"
	"whatsappBot/scheduler"
	"whatsappBot/tone"
	"whatsappBot/utils"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type messageRouter interface {
	RouteMessage(*sql.DB, context.Context, *events.Message) error
}

type toneResponder interface {
	MatchAndAutoRespond(context.Context, *events.Message) error
}

type lifecycleClient interface {
	utils.MessageSender
	AddEventHandler(whatsmeow.EventHandler) uint32
	Connect() error
	Disconnect()
	GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error)
	IsConnected() bool
	IsLoggedIn() bool
}

type authenticationWaiter struct {
	client lifecycleClient
	once   sync.Once
	result chan error
}

func newAuthenticationWaiter(client lifecycleClient) *authenticationWaiter {
	return &authenticationWaiter{
		client: client,
		result: make(chan error, 1),
	}
}

func (waiter *authenticationWaiter) observe(evt interface{}) {
	switch event := evt.(type) {
	case *events.Connected:
		if waiter.client.IsLoggedIn() {
			waiter.complete(nil)
		}
	case *events.Disconnected:
		waiter.complete(errors.New("WhatsApp disconnected before authentication"))
	case *events.LoggedOut:
		waiter.complete(errors.New("WhatsApp logged out before authentication"))
	case *events.StreamReplaced:
		waiter.complete(errors.New("WhatsApp stream replaced before authentication"))
	case *events.ConnectFailure:
		waiter.complete(fmt.Errorf("WhatsApp authentication failed: %s", event.Message))
	}
}

func (waiter *authenticationWaiter) complete(err error) {
	waiter.once.Do(func() {
		waiter.result <- err
	})
}

func (waiter *authenticationWaiter) wait(ctx context.Context) error {
	if waiter.client.IsLoggedIn() {
		waiter.complete(nil)
	}
	select {
	case err := <-waiter.result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("wait for WhatsApp authentication: %w", ctx.Err())
	}
}

type runtimeDependencies struct {
	setupStore       func(context.Context, string) (*store.Device, *sql.DB, error)
	newClient        func(*store.Device, config.Config) lifecycleClient
	newToneManager   func(utils.MessageSender) (toneResponder, error)
	newOutboundStore func(*sql.DB, string) *outbound.Store
	recoverSending   func(context.Context, *outbound.Store) (int64, error)
	startAPI         func(context.Context, config.Config, *outbound.Store) <-chan error
	startScheduler   func(context.Context, *sql.DB, utils.MessageSender, *outbound.Store) <-chan struct{}
	renderQR         func(string)
}

func eventHandler(
	database *sql.DB,
	ctx context.Context,
	commandManager messageRouter,
	toneManager toneResponder,
	allowedGroups map[string]struct{},
) whatsmeow.EventHandler {
	return func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			if _, ok := allowedGroups[v.Info.Chat.String()]; !ok {
				return
			}
			if err := commandManager.RouteMessage(database, ctx, v); err != nil {
				fmt.Fprintf(os.Stderr, "Command handling error: %v\n", err)
			}
			if err := toneManager.MatchAndAutoRespond(ctx, v); err != nil {
				fmt.Fprintf(os.Stderr, "Tone response error: %v\n", err)
			}
		case *events.Connected:
			fmt.Println("WhatsApp client connected successfully!")
		case *events.Disconnected:
			fmt.Println("WhatsApp client disconnected.")
		case *events.PairSuccess:
			fmt.Println("Successfully paired with WhatsApp!")
		}
	}
}

func allowedGroupSet(groups []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		allowed[group] = struct{}{}
	}
	return allowed
}

func defaultRuntimeDependencies() runtimeDependencies {
	return runtimeDependencies{
		setupStore: func(ctx context.Context, path string) (*store.Device, *sql.DB, error) {
			container, database, err := db.SetupDB(ctx, path)
			if err != nil {
				return nil, nil, err
			}
			device, err := container.GetFirstDevice(ctx)
			if err != nil {
				database.Close()
				return nil, nil, fmt.Errorf("get first device: %w", err)
			}
			return device, database, nil
		},
		newClient: func(device *store.Device, cfg config.Config) lifecycleClient {
			return whatsmeow.NewClient(device, waLog.Stdout("Client", cfg.LogLevel, true))
		},
		newToneManager: func(sender utils.MessageSender) (toneResponder, error) {
			return tone.NewToneIManager(sender)
		},
		newOutboundStore: func(database *sql.DB, targetGroup string) *outbound.Store {
			return outbound.NewStore(database, targetGroup)
		},
		recoverSending: func(ctx context.Context, store *outbound.Store) (int64, error) {
			return store.RecoverSending(ctx)
		},
		startAPI: func(ctx context.Context, cfg config.Config, store *outbound.Store) <-chan error {
			return messageapi.New(cfg, store, time.Now).Start(ctx)
		},
		startScheduler: func(ctx context.Context, database *sql.DB, sender utils.MessageSender, outboundStore *outbound.Store) <-chan struct{} {
			registry := scheduler.NewRegistry(database, outboundStore)
			return scheduler.NewScheduler(registry, sender, time.Second).Start(ctx)
		},
		renderQR: func(code string) {
			qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
		},
	}
}

func run(ctx context.Context, cfg config.Config) error {
	return runWithDependencies(ctx, cfg, defaultRuntimeDependencies())
}

func runWithDependencies(ctx context.Context, cfg config.Config, deps runtimeDependencies) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	device, database, err := deps.setupStore(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("set up database: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, database.Close())
	}()

	var outboundStore *outbound.Store
	if cfg.APIEnabled {
		outboundStore = deps.newOutboundStore(database, cfg.APITargetGroup)
		if _, err := deps.recoverSending(ctx, outboundStore); err != nil {
			return fmt.Errorf("recover outbound messages: %w", err)
		}
	}

	client := deps.newClient(device, cfg)
	defer client.Disconnect()

	toneManager, err := deps.newToneManager(client)
	if err != nil {
		return fmt.Errorf("load tone indicators: %w", err)
	}
	commandManager := cmd.NewCommandManager(client, cfg.AdminNumbers, cancel)
	handleEvent := eventHandler(
		database,
		ctx,
		commandManager,
		toneManager,
		allowedGroupSet(cfg.AllowedGroups),
	)
	authWaiter := newAuthenticationWaiter(client)
	client.AddEventHandler(func(evt interface{}) {
		authWaiter.observe(evt)
		handleEvent(evt)
	})

	if err := connectClient(ctx, client, func() bool { return device.ID != nil }, deps.renderQR, authWaiter); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var apiDone <-chan error
	var apiCancel context.CancelFunc
	if cfg.APIEnabled {
		var apiCtx context.Context
		apiCtx, apiCancel = context.WithCancel(context.WithoutCancel(ctx))
		defer apiCancel()
		apiDone = deps.startAPI(apiCtx, cfg, outboundStore)
		select {
		case err, ok := <-apiDone:
			apiCancel()
			if !ok {
				return errors.New("start message API: server exited without a result")
			}
			if err == nil {
				return errors.New("start message API: server exited unexpectedly")
			}
			return fmt.Errorf("start message API: %w", err)
		default:
		}
	}

	schedulerCtx, schedulerCancel := context.WithCancel(context.WithoutCancel(ctx))
	schedulerDone := deps.startScheduler(schedulerCtx, database, client, outboundStore)
	fmt.Println("Bot is running. Press Ctrl+C to exit.")

	var apiErr error
	apiResultRead := false
	if apiDone == nil {
		<-ctx.Done()
	} else {
		select {
		case <-ctx.Done():
		case err, ok := <-apiDone:
			apiResultRead = true
			if !ok {
				apiErr = errors.New("message API server exited without a result")
			} else {
				apiErr = err
			}
			if apiErr == nil && ctx.Err() == nil {
				apiErr = errors.New("message API server exited unexpectedly")
			}
			cancel()
		}
		apiCancel()
		if !apiResultRead {
			if err, ok := <-apiDone; ok {
				apiErr = err
			}
		}
	}
	schedulerCancel()
	<-schedulerDone
	fmt.Println("Shutting down bot...")
	return apiErr
}

func connectClient(
	ctx context.Context,
	client lifecycleClient,
	hasStoredDevice func() bool,
	renderQR func(string),
	authWaiter *authenticationWaiter,
) error {
	if hasStoredDevice() {
		fmt.Println("Existing session found. Connecting...")
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect existing session: %w", err)
		}
		if err := authWaiter.wait(ctx); err != nil {
			return fmt.Errorf("authenticate existing session: %w", err)
		}
		return nil
	}

	fmt.Println("No existing session found. Generating QR code...")
	qrChannel, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("get QR channel: %w", err)
	}

	connectResult := make(chan error, 1)
	go func() {
		connectResult <- client.Connect()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-authWaiter.result:
			if err != nil {
				return fmt.Errorf("authenticate paired session: %w", err)
			}
			return nil
		case err := <-connectResult:
			if err != nil {
				return fmt.Errorf("connect for QR pairing: %w", err)
			}
			if client.IsLoggedIn() {
				return nil
			}
			connectResult = nil
		case event, ok := <-qrChannel:
			if !ok {
				if !hasStoredDevice() && !client.IsLoggedIn() {
					return errors.New("QR channel closed before pairing or authentication succeeded")
				}
				qrChannel = nil
				continue
			}
			if event.Event == "code" {
				fmt.Println("Scan this QR code with your WhatsApp app:")
				renderQR(event.Code)
			} else {
				fmt.Printf("QR event: %s\n", event.Event)
			}
		}
	}
}

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "Startup error: %v\n", err)
		os.Exit(1)
	}
}
