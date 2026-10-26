package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"whatsappBot/outbound"
	"whatsappBot/utils"
)

const outboundBatchSize = 100

type OutboundProvider struct {
	store *outbound.Store
}

type OutboundTask struct {
	store     *outbound.Store
	jobID     int64
	chatID    types.JID
	message   string
	messageID types.MessageID
	schedule  time.Time
}

func NewOutboundProvider(store *outbound.Store) *OutboundProvider {
	return &OutboundProvider{store: store}
}

func (p *OutboundProvider) GetDueTasks(
	ctx context.Context,
	now time.Time,
) ([]ScheduledTask, error) {
	jobs, err := p.store.Due(ctx, now, outboundBatchSize)
	if err != nil {
		return nil, fmt.Errorf("query due outbound jobs: %w", err)
	}

	tasks := make([]ScheduledTask, 0, len(jobs))
	for _, job := range jobs {
		chatID, err := types.ParseJID(job.ChatID)
		if err != nil {
			return nil, fmt.Errorf("parse outbound job %d chat ID %q: %w", job.ID, job.ChatID, err)
		}
		tasks = append(tasks, &OutboundTask{
			store:     p.store,
			jobID:     job.ID,
			chatID:    chatID,
			message:   job.Message,
			messageID: types.MessageID(job.MessageID),
			schedule:  job.NextAttemptAt,
		})
	}
	return tasks, nil
}

func (t *OutboundTask) Run(ctx context.Context, sender utils.MessageSender) error {
	claimed, err := t.store.Claim(ctx, t.jobID)
	if err != nil {
		return fmt.Errorf("claim outbound job %d: %w", t.jobID, err)
	}
	if !claimed {
		return nil
	}

	message := &waProto.Message{Conversation: &t.message}
	_, sendErr := sender.SendMessage(
		ctx,
		t.chatID,
		message,
		whatsmeow.SendRequestExtra{ID: t.messageID},
	)
	if sendErr != nil {
		transitionErr := t.store.MarkFailed(ctx, t.jobID, sendErr.Error())
		if transitionErr != nil {
			return errors.Join(
				fmt.Errorf("send outbound job %d: %w", t.jobID, sendErr),
				fmt.Errorf("record outbound job %d failure: %w", t.jobID, transitionErr),
			)
		}
		return fmt.Errorf("send outbound job %d: %w", t.jobID, sendErr)
	}

	if err := t.store.MarkSent(ctx, t.jobID); err != nil {
		return fmt.Errorf("mark outbound job %d sent: %w", t.jobID, err)
	}
	return nil
}

func (t *OutboundTask) Time() time.Time {
	return t.schedule
}

func (t *OutboundTask) ID() string {
	return fmt.Sprintf("outbound-%d", t.jobID)
}
