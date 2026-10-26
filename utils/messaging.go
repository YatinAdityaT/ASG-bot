package utils

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type MessageSender interface {
	SendMessage(
		context.Context,
		types.JID,
		*waProto.Message,
		...whatsmeow.SendRequestExtra,
	) (whatsmeow.SendResponse, error)
}

func ReplyToMessage(
	ctx context.Context,
	client MessageSender,
	to types.JID,
	text string,
	originalMessageID string,
	participant types.JID,
) error {
	participantString := participant.String()
	contextInfo := &waProto.ContextInfo{
		StanzaID:    &originalMessageID,
		Participant: &participantString,
	}

	extendedText := &waProto.ExtendedTextMessage{
		Text:        &text,
		ContextInfo: contextInfo,
	}

	msg := &waProto.Message{
		ExtendedTextMessage: extendedText,
	}

	_, err := client.SendMessage(ctx, to, msg)
	return err
}

// ExtractText returns trimmed message text without changing its capitalization.
// It returns an empty string if no usable text is found.
func ExtractText(evt *events.Message) string {
	if evt == nil {
		return ""
	}
	msg := evt.Message
	if msg == nil {
		return ""
	}

	text := msg.GetConversation()

	// Check for extended text message if plain conversation is empty
	if text == "" && msg.GetExtendedTextMessage() != nil {
		text = msg.GetExtendedTextMessage().GetText()
	}

	return strings.TrimSpace(text)
}

// ExtractCleanText returns the normalized (trimmed + lowercased) message text.
// Returns an empty string if no usable text is found.
func ExtractCleanText(evt *events.Message) string {
	return strings.ToLower(ExtractText(evt))
}
