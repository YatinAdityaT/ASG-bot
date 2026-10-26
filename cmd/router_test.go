package cmd

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type fakeSender struct {
	messages []*waProto.Message
	err      error
}

func (f *fakeSender) SendMessage(
	_ context.Context,
	_ types.JID,
	msg *waProto.Message,
	_ ...whatsmeow.SendRequestExtra,
) (whatsmeow.SendResponse, error) {
	f.messages = append(f.messages, msg)
	return whatsmeow.SendResponse{}, f.err
}

func TestRouteMessageRepliesToEchoWithCapturedText(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, func() {})

	manager.RouteMessage(nil, context.Background(), messageEvent("/echo hello"))

	if got := sentText(t, sender); got != "hello" {
		t.Fatalf("echo reply = %q, want %q", got, "hello")
	}
}

func TestRouteMessageRepliesToPing(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, func() {})

	manager.RouteMessage(nil, context.Background(), messageEvent("/ping"))

	if got := sentText(t, sender); got != "Pong!" {
		t.Fatalf("ping reply = %q, want %q", got, "Pong!")
	}
}

func TestRouteMessageIgnoresNonCommandText(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, func() {})

	manager.RouteMessage(nil, context.Background(), messageEvent("hello"))

	if len(sender.messages) != 0 {
		t.Fatalf("sent %d messages, want none", len(sender.messages))
	}
}

func TestRouteMessagePassesOnlyCaptureGroupsToCallback(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, func() {})
	var gotArgs []string
	manager.RegisterCommand(Command{
		Name:  "/test",
		Regex: regexp.MustCompile(`^/test (\d+) (.+)$`),
		Callback: func(_ *sql.DB, _ context.Context, _ utils.MessageSender, _ types.MessageInfo, args []string) {
			gotArgs = args
		},
	})

	manager.RouteMessage(nil, context.Background(), messageEvent("/test 12 words here"))

	if len(gotArgs) != 2 || gotArgs[0] != "12" || gotArgs[1] != "words here" {
		t.Fatalf("callback args = %#v, want []string{%q, %q}", gotArgs, "12", "words here")
	}
}

func messageEvent(text string) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   types.NewJID("123", types.GroupServer),
				Sender: types.NewJID("456", types.DefaultUserServer),
			},
			ID: "message-id",
		},
		Message: &waProto.Message{Conversation: &text},
	}
}

func sentText(t *testing.T, sender *fakeSender) string {
	t.Helper()
	if len(sender.messages) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sender.messages))
	}
	return sender.messages[0].GetExtendedTextMessage().GetText()
}
