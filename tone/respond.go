package tone

import (
	"context"
	"strings"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
)

func (tim *ToneIManager) MatchAndAutoRespond(ctx context.Context, client *whatsmeow.Client, evt *events.Message) {
	info := evt.Info
	messageText := utils.ExtractCleanText(evt)

	if messageText == "" {
		return
	}

	var outputDescriptions []string

	for _, ti := range tim.toneIndicators {
		if ti.Regex.MatchString(messageText) {
			outputDescriptions = append(outputDescriptions, ti.Description)
		}
	}

	if len(outputDescriptions) == 0 {
		return
	}

	// Combine all descriptions into a single string
	outputText := "[Automatic tone identifier]\nMatched tones:\n" + strings.Join(outputDescriptions, "\n")

	utils.ReplyToMessage(ctx, client, info.Chat, outputText, info.ID, info.Sender)
}
