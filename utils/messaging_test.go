package utils

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
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

func TestReplyToMessageReturnsSendError(t *testing.T) {
	sendErr := errors.New("send failed")
	sender := &fakeSender{err: sendErr}

	err := ReplyToMessage(
		context.Background(),
		sender,
		types.NewJID("123", types.DefaultUserServer),
		"hello",
		"message-id",
		types.NewJID("456", types.DefaultUserServer),
	)

	if !errors.Is(err, sendErr) {
		t.Fatalf("ReplyToMessage() error = %v, want %v", err, sendErr)
	}
}
