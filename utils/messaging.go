package utils

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func ReplyToMessage(
	ctx context.Context,
	client *whatsmeow.Client,
	to types.JID,
	text string,
	originalMessageID string,
	participant types.JID,
) {
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

	client.SendMessage(ctx, to, msg)
}

// ExtractCleanText returns the normalized (trimmed + lowercased) message text.
// Returns an empty string if no usable text is found.
func ExtractCleanText(evt *events.Message) string {
	msg := evt.Message
	if msg == nil {
		return ""
	}

	text := msg.GetConversation()

	// Check for extended text message if plain conversation is empty
	if text == "" && msg.GetExtendedTextMessage() != nil {
		text = msg.GetExtendedTextMessage().GetText()
	}

	// Normalize: trim and lowercase
	return strings.ToLower(strings.TrimSpace(text))
}
