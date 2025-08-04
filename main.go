package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"whatsappBot/cmd"
	"whatsappBot/db"
	"whatsappBot/scheduler"
	"whatsappBot/tone"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

const testingGroup = "120363418911552290@g.us"
const asg = "120363160344608606@g.us"

func eventHandler(db *sql.DB, ctx context.Context, client *whatsmeow.Client, commandManager *cmd.CommandManager, toneIndicatorM *tone.ToneIManager) func(evt interface{}) {
	return func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			if v.Info.Chat.String() == testingGroup || v.Info.Chat.String() == asg {
				commandManager.RouteMessage(db, ctx, client, v)
				toneIndicatorM.MatchAndAutoRespond(ctx, client, v)
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

func main() {
	ctx := context.Background()

	adminNumbers := []string{
		"916355442207",
	}

	container, db := db.SetupDB(&ctx)
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to get first device: %w", err))
	}
	client := whatsmeow.NewClient(dev, waLog.Stdout("Client", "INFO", true))

	cmdMngr := cmd.NewCommandManager(client, adminNumbers)
	tiMngr := tone.NewToneIManager(client)

	client.AddEventHandler(eventHandler(db, ctx, client, cmdMngr, tiMngr))

	reg := scheduler.NewRegistry(db)
	sched := scheduler.NewScheduler(reg, client, 1*time.Second)
	sched.Start(ctx)

	if client.Store.ID == nil {
		fmt.Println("No existing session found. Generating QR code...")
		qrChan, err := client.GetQRChannel(ctx)
		if err != nil {
			panic(fmt.Errorf("failed to get QR channel: %w", err))
		}

		go func() {
			if err := client.Connect(); err != nil {
				fmt.Fprintf(os.Stderr, "Client connection error: %v\n", err)
				os.Exit(1)
			}
		}()

		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("Scan this QR code with your WhatsApp app:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else {
				fmt.Printf("QR event: %s\n", evt.Event)
			}
		}
	} else {
		fmt.Println("Existing session found. Connecting...")
		if err := client.Connect(); err != nil {
			panic(fmt.Errorf("failed to connect with existing session: %w", err))
		}
	}

	fmt.Println("Bot is running. Press Ctrl+C to exit.")

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	<-ch

	fmt.Println("Shutting down bot...")
	client.Disconnect()
	fmt.Println("Bot disconnected. Exiting.")
}
