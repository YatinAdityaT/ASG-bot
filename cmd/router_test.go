package cmd

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
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

func TestRouteMessageMatchesCommandCaseInsensitivelyAndPreservesCapture(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, func() {})

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/EcHo Review CUDA Docs")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	if got := sentText(t, sender); got != "Review CUDA Docs" {
		t.Fatalf("echo reply = %q, want preserved capture", got)
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

func TestRouteMessageReturnsPingSendError(t *testing.T) {
	sendErr := errors.New("ping send failed")
	sender := &fakeSender{err: sendErr}
	manager := NewCommandManager(sender, nil, func() {})

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/ping"))

	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
	}
}

func TestRouteMessageReturnsEchoSendError(t *testing.T) {
	sendErr := errors.New("echo send failed")
	sender := &fakeSender{err: sendErr}
	manager := NewCommandManager(sender, nil, func() {})

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/echo hello"))

	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
	}
}

func TestRouteMessageReturnsUnauthorizedReplySendError(t *testing.T) {
	sendErr := errors.New("denial send failed")
	sender := &fakeSender{err: sendErr}
	manager := NewCommandManager(sender, nil, func() {})

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/die"))

	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
	}
}

func TestRouteMessageHelpPreservesNonAdminCommands(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(sender, nil, func() {})

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/help")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}

	reply := sentText(t, sender)
	for _, command := range []string{"/ping", "/echo", "/help", "/remind", "/rlist", "/rupdate <key>", "/rdelete <key>"} {
		if !strings.Contains(reply, "`"+command+"`") {
			t.Errorf("help reply missing %q", command)
		}
	}
	if strings.Contains(reply, "`/die`") {
		t.Error("help reply contains admin-only /die command")
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
		Callback: func(_ *sql.DB, _ context.Context, _ utils.MessageSender, _ CommandContext, args []string) error {
			gotArgs = args
			return nil
		},
	})

	manager.RouteMessage(nil, context.Background(), messageEvent("/test 12 words here"))

	if len(gotArgs) != 2 || gotArgs[0] != "12" || gotArgs[1] != "words here" {
		t.Fatalf("callback args = %#v, want []string{%q, %q}", gotArgs, "12", "words here")
	}
}

func TestRouteMessagePassesInfoAndTrustedMentionsToCallback(t *testing.T) {
	manager := NewCommandManager(&fakeSender{}, nil, func() {})
	var got CommandContext
	manager.RegisterCommand(Command{
		Name:  "/test",
		Regex: regexp.MustCompile(`^/test$`),
		Callback: func(_ *sql.DB, _ context.Context, _ utils.MessageSender, commandCtx CommandContext, _ []string) error {
			got = commandCtx
			return nil
		},
	})
	mentioned := types.NewJID("789", types.DefaultUserServer)
	evt := messageEvent("/test")
	evt.Info.PushName = "Yatin"
	evt.Message = &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: stringPointer("/test"),
			ContextInfo: &waProto.ContextInfo{
				MentionedJID: []string{mentioned.String()},
			},
		},
	}

	if err := manager.RouteMessage(nil, context.Background(), evt); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	if got.Info.ID != evt.Info.ID || got.Info.PushName != "Yatin" {
		t.Errorf("context info = %#v, want event info", got.Info)
	}
	if len(got.Mentions) != 1 || got.Mentions[0] != mentioned {
		t.Errorf("context mentions = %#v, want %#v", got.Mentions, []types.JID{mentioned})
	}
}

func TestRouteMessageIgnoresInvalidMentionMetadata(t *testing.T) {
	manager := NewCommandManager(&fakeSender{}, nil, func() {})
	var got CommandContext
	manager.RegisterCommand(Command{
		Name:  "/test",
		Regex: regexp.MustCompile(`^/test$`),
		Callback: func(_ *sql.DB, _ context.Context, _ utils.MessageSender, commandCtx CommandContext, _ []string) error {
			got = commandCtx
			return nil
		},
	})
	evt := messageEvent("/test")
	evt.Message = &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: stringPointer("/test"),
			ContextInfo: &waProto.ContextInfo{
				MentionedJID: []string{"not-a-jid"},
			},
		},
	}

	if err := manager.RouteMessage(nil, context.Background(), evt); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
	if len(got.Mentions) != 0 {
		t.Errorf("context mentions = %#v, want none", got.Mentions)
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

func stringPointer(value string) *string {
	return &value
}
