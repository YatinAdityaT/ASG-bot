package tone

import (
	"context"
	"strings"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow/types/events"
)

func (tim *ToneIManager) MatchAndAutoRespond(ctx context.Context, evt *events.Message) error {
	info := evt.Info
	messageText := utils.ExtractCleanText(evt)

	if messageText == "" {
		return nil
	}

	var outputDescriptions []string

	for _, ti := range tim.toneIndicators {
		if ti.Regex.MatchString(messageText) {
			outputDescriptions = append(outputDescriptions, ti.Description)
		}
	}

	if len(outputDescriptions) == 0 {
		return nil
	}

	// Combine all descriptions into a single string
	outputText := "[Automatic tone identifier]\nMatched tones:\n" + strings.Join(outputDescriptions, "\n")

	return utils.ReplyToMessage(ctx, tim.client, info.Chat, outputText, info.ID, info.Sender)
}
