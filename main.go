package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"whatsappBot/cmd"
	"whatsappBot/config"
	"whatsappBot/db"
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
}

type runtimeDependencies struct {
	setupStore     func(context.Context, string) (*store.Device, *sql.DB, error)
	newClient      func(*store.Device, config.Config) lifecycleClient
	newToneManager func(utils.MessageSender) (toneResponder, error)
	startScheduler func(context.Context, *sql.DB, utils.MessageSender)
	renderQR       func(string)
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
		startScheduler: func(ctx context.Context, database *sql.DB, sender utils.MessageSender) {
			registry := scheduler.NewRegistry(database)
			scheduler.NewScheduler(registry, sender, time.Second).Start(ctx)
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

	client := deps.newClient(device, cfg)
	defer client.Disconnect()

	toneManager, err := deps.newToneManager(client)
	if err != nil {
		return fmt.Errorf("load tone indicators: %w", err)
	}
	commandManager := cmd.NewCommandManager(client, cfg.AdminNumbers, cancel)
	client.AddEventHandler(eventHandler(
		database,
		ctx,
		commandManager,
		toneManager,
		allowedGroupSet(cfg.AllowedGroups),
	))

	if err := connectClient(ctx, client, func() bool { return device.ID != nil }, deps.renderQR); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	deps.startScheduler(ctx, database, client)
	fmt.Println("Bot is running. Press Ctrl+C to exit.")
	<-ctx.Done()
	fmt.Println("Shutting down bot...")
	return nil
}

func connectClient(
	ctx context.Context,
	client lifecycleClient,
	hasStoredDevice func() bool,
	renderQR func(string),
) error {
	if hasStoredDevice() {
		fmt.Println("Existing session found. Connecting...")
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect existing session: %w", err)
		}
		if !client.IsConnected() {
			return errors.New("connect existing session: client is not connected")
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
		case err := <-connectResult:
			if err != nil {
				return fmt.Errorf("connect for QR pairing: %w", err)
			}
			connectResult = nil
		case event, ok := <-qrChannel:
			if !ok {
				if hasStoredDevice() || client.IsConnected() {
					return nil
				}
				return errors.New("QR channel closed before pairing or connection succeeded")
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
