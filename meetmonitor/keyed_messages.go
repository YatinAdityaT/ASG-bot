package meetmonitor

import (
	"context"
	"strings"
	"unicode/utf8"
)

func (s *Service) QueueMessageWithID(ctx context.Context, id, text string) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	id = strings.TrimSpace(id)
	text = strings.TrimSpace(text)
	if id == "" || utf8.RuneCountInString(id) > 255 ||
		text == "" || utf8.RuneCountInString(text) > maxMeetMessageRunes {
		return Message{}, ErrInvalidMessage
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, message := range s.messages {
		if message.ID == id {
			return message, nil
		}
	}
	if len(s.messages) >= maxPendingMessages {
		return Message{}, ErrMessageQueueFull
	}
	message := Message{
		ID:        id,
		Text:      text,
		CreatedAt: s.now().UTC(),
	}
	s.messages = append(s.messages, message)
	return message, nil
}
