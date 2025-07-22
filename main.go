package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

const targetGroupJID = "120363418911552290@g.us"

func getMessageText(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	if msg.Conversation != nil {
		return msg.GetConversation()
	}
	if msg.ExtendedTextMessage != nil {
		return msg.GetExtendedTextMessage().GetText()
	}
	if msg.ImageMessage != nil && msg.ImageMessage.Caption != nil {
		return msg.ImageMessage.GetCaption()
	}
	if msg.VideoMessage != nil && msg.VideoMessage.Caption != nil {
		return msg.VideoMessage.GetCaption()
	}
	return ""
}

func main() {
	ctx := context.Background()

	container, err := sqlstore.New(ctx, "sqlite3", "file:whatsmeow.db?_foreign_keys=on", waLog.Stdout("SQL", "DEBUG", true))
	if err != nil {
		panic(fmt.Errorf("failed to create SQL store: %w", err))
	}

	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to get first device: %w", err))
	}

	client := whatsmeow.NewClient(dev, waLog.Stdout("Client", "DEBUG", true))

	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			info := v.Info

			if info.IsGroup && !info.IsFromMe && info.Chat.String() == targetGroupJID {
				text := getMessageText(v.Message)
				if text == "" {
					text = "[Non-text message or empty message]"
				}

				fmt.Printf("📥 [%s] %s: %s\n", info.Chat.String(), info.Sender.String(), text)

				replyText := fmt.Sprintf("Bot received your message in this specific group: \"%s\"", text)
				reply := &waProto.Message{Conversation: proto.String(replyText)}

				_, err := client.SendMessage(ctx, info.Chat, reply)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error sending message to %s: %v\n", info.Chat.String(), err)
				} else {
					fmt.Printf("📤 Replied to %s in group %s\n", info.Sender.String(), info.Chat.String())
				}
			} else if info.IsGroup && info.Chat.String() != targetGroupJID {
				fmt.Printf("Ignoring message from non-target group: [%s] %s: %s\n", info.Chat.String(), info.Sender.String(), getMessageText(v.Message))
			} else if !info.IsGroup {
				fmt.Printf("Ignoring direct message: [%s] %s: %s\n", info.Chat.String(), info.Sender.String(), getMessageText(v.Message))
			}

		case *events.Connected:
			fmt.Println("WhatsApp client connected successfully!")
		case *events.Disconnected:
			fmt.Println("WhatsApp client disconnected.")
		case *events.PairSuccess:
			fmt.Println("Successfully paired with WhatsApp!")
		}
	})

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