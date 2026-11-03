package meetmonitor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

const maxPendingMessages = 100
const maxMeetMessageRunes = 1000

func (s *Service) QueueMessage(ctx context.Context, text string) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxMeetMessageRunes {
		return Message{}, ErrInvalidMessage
	}

	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Message{}, err
	}
	message := Message{
		ID:        hex.EncodeToString(idBytes),
		Text:      text,
		CreatedAt: s.now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) >= maxPendingMessages {
		return Message{}, ErrMessageQueueFull
	}
	s.messages = append(s.messages, message)
	return message, nil
}

func (s *Service) PendingMessage(ctx context.Context) (*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return nil, nil
	}
	message := s.messages[0]
	return &message, nil
}

func (s *Service) AcknowledgeMessage(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, message := range s.messages {
		if message.ID != id {
			continue
		}
		s.messages = append(s.messages[:index], s.messages[index+1:]...)
		return true, nil
	}
	return false, nil
}
