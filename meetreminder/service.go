package meetreminder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"

	"whatsappBot/identity"
	"whatsappBot/utils"
)

var (
	ErrInvalidCommand  = errors.New("invalid Meet reminder command")
	ErrUnknownSender   = errors.New("unknown Meet reminder sender")
	ErrUnknownTarget   = errors.New("unknown Meet reminder target")
	ErrServiceDisabled = errors.New("Meet reminders are not configured")
)

type CommandRequest struct {
	SourceKey      string
	MeetingCode    string
	SenderMeetName string
	Text           string
	WhatsAppChatID string
}

type CommandResult struct {
	Reminder Reminder
	Created  bool
}

type Service struct {
	store      *Store
	identities *identity.Store
	now        func() time.Time
}

func NewService(store *Store, identities *identity.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, identities: identities, now: now}
}

func (s *Service) CreateFromMeet(ctx context.Context, input CommandRequest) (CommandResult, error) {
	if s == nil || s.store == nil || s.identities == nil ||
		strings.TrimSpace(input.WhatsAppChatID) == "" {
		return CommandResult{}, ErrServiceDisabled
	}

	requester, err := s.identities.GetByMeetAlias(ctx, input.SenderMeetName)
	if errors.Is(err, identity.ErrNotFound) {
		return CommandResult{}, ErrUnknownSender
	}
	if err != nil {
		return CommandResult{}, fmt.Errorf("resolve Meet reminder sender: %w", err)
	}
	requesterJID, err := types.ParseJID(requester.WhatsAppJID)
	if err != nil {
		return CommandResult{}, fmt.Errorf("parse Meet reminder sender identity: %w", err)
	}

	commandText := strings.TrimSpace(input.Text)
	fields := strings.Fields(commandText)
	if len(fields) < 3 || !strings.EqualFold(fields[0], "/remind") {
		return CommandResult{}, ErrInvalidCommand
	}

	targetToken := strings.TrimPrefix(strings.ToLower(fields[1]), "@")
	var (
		target    identity.Person
		parseText string
		mentions  []types.JID
	)
	if targetToken == "me" {
		target = requester
		parseText = commandText
	} else {
		target, err = s.identities.Get(ctx, targetToken)
		if errors.Is(err, identity.ErrNotFound) {
			return CommandResult{}, ErrUnknownTarget
		}
		if err != nil {
			return CommandResult{}, fmt.Errorf("resolve Meet reminder target: %w", err)
		}
		targetJID, parseErr := types.ParseJID(target.WhatsAppJID)
		if parseErr != nil {
			return CommandResult{}, fmt.Errorf("parse Meet reminder target identity: %w", parseErr)
		}
		mentions = []types.JID{targetJID.ToNonAD()}
		parseText = "/remind @" + target.Username +
			commandText[len(fields[0])+1+len(fields[1]):]
	}

	parsed, err := utils.ParseReminder(
		parseText,
		mentions,
		requesterJID.ToNonAD(),
		requester.CanonicalName,
		s.now(),
	)
	if err != nil {
		return CommandResult{}, fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}

	reminder, created, err := s.store.Create(ctx, CreateRequest{
		SourceKey:         input.SourceKey,
		MeetingCode:       input.MeetingCode,
		RequesterPersonID: requester.ID,
		TargetPersonID:    target.ID,
		WhatsAppChatID:    input.WhatsAppChatID,
		Body:              parsed.Body,
		RemindAt:          parsed.RemindAt,
	})
	if err != nil {
		return CommandResult{}, err
	}
	return CommandResult{Reminder: reminder, Created: created}, nil
}
