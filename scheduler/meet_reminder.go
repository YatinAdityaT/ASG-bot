package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"whatsappBot/identity"
	"whatsappBot/meetmonitor"
	"whatsappBot/meetreminder"
	"whatsappBot/utils"
)

const meetReminderBatchSize = 100

type meetReminderMonitor interface {
	CallRoster(context.Context) (meetmonitor.CallRoster, error)
	QueueMessageWithID(context.Context, string, string) (meetmonitor.Message, error)
}

type MeetReminderProvider struct {
	store   *meetreminder.Store
	monitor meetReminderMonitor
}

type MeetReminderTask struct {
	store    *meetreminder.Store
	monitor  meetReminderMonitor
	reminder meetreminder.Reminder
	now      time.Time
}

func NewMeetReminderProvider(store *meetreminder.Store, monitor meetReminderMonitor) TaskProvider {
	return &MeetReminderProvider{store: store, monitor: monitor}
}

func (p *MeetReminderProvider) GetDueTasks(
	ctx context.Context,
	now time.Time,
) ([]ScheduledTask, error) {
	if p == nil || p.store == nil || p.monitor == nil {
		return nil, nil
	}
	reminders, err := p.store.Due(ctx, now, meetReminderBatchSize)
	if err != nil {
		return nil, err
	}
	tasks := make([]ScheduledTask, 0, len(reminders))
	for _, reminder := range reminders {
		tasks = append(tasks, &MeetReminderTask{
			store:    p.store,
			monitor:  p.monitor,
			reminder: reminder,
			now:      now,
		})
	}
	return tasks, nil
}

func (t *MeetReminderTask) Run(ctx context.Context, sender utils.MessageSender) error {
	if t.reminder.WhatsAppDeliveredAt == nil {
		if err := t.sendWhatsApp(ctx, sender); err != nil {
			return err
		}
		if err := t.store.MarkWhatsAppDelivered(ctx, t.reminder.ID, t.now); err != nil {
			return fmt.Errorf("record Meet reminder WhatsApp delivery: %w", err)
		}
	}

	roster, err := t.monitor.CallRoster(ctx)
	if err != nil {
		return fmt.Errorf("load roster for Meet reminder: %w", err)
	}
	if roster.MonitorState != "online" {
		return t.complete(ctx, "skipped_offline")
	}

	present := false
	for _, participant := range roster.Participants {
		if participant.Self {
			continue
		}
		if identity.MatchesMeetAlias(t.reminder.Target, participant.Name) {
			present = true
			break
		}
	}
	if !present {
		return t.complete(ctx, "skipped_absent")
	}

	meetText := fmt.Sprintf(
		`%s — %s asked me to remind you to %q at %s.`,
		t.reminder.Target.CanonicalName,
		t.reminder.Requester.CanonicalName,
		t.reminder.Body,
		t.reminder.RemindAt.In(time.Local).Format("3:04 PM"),
	)
	if t.reminder.Target.ID == t.reminder.Requester.ID {
		meetText = fmt.Sprintf(
			`%s — you asked me to remind you to %q at %s.`,
			t.reminder.Target.CanonicalName,
			t.reminder.Body,
			t.reminder.RemindAt.In(time.Local).Format("3:04 PM"),
		)
	}
	_, err = t.monitor.QueueMessageWithID(
		ctx,
		fmt.Sprintf("meet-reminder-%d", t.reminder.ID),
		meetText,
	)
	if err != nil {
		return fmt.Errorf("queue Meet reminder %d: %w", t.reminder.ID, err)
	}
	return t.complete(ctx, "queued")
}

func (t *MeetReminderTask) sendWhatsApp(ctx context.Context, sender utils.MessageSender) error {
	chatID, err := types.ParseJID(t.reminder.WhatsAppChatID)
	if err != nil {
		return fmt.Errorf("parse Meet reminder chat ID: %w", err)
	}
	targetJID, err := types.ParseJID(t.reminder.Target.WhatsAppJID)
	if err != nil {
		return fmt.Errorf("parse Meet reminder target JID: %w", err)
	}
	targetJID = targetJID.ToNonAD()
	message := fmt.Sprintf(
		`@%s %s asked me to remind you to %q at %s`,
		targetJID.User,
		t.reminder.Requester.CanonicalName,
		t.reminder.Body,
		t.reminder.RemindAt.In(time.Local).Format("3:04 PM"),
	)
	if t.reminder.Target.ID == t.reminder.Requester.ID {
		message = fmt.Sprintf(
			`@%s you asked me to remind you to %q at %s`,
			targetJID.User,
			t.reminder.Body,
			t.reminder.RemindAt.In(time.Local).Format("3:04 PM"),
		)
	}
	_, err = sender.SendMessage(
		ctx,
		chatID,
		&waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: &message,
				ContextInfo: &waProto.ContextInfo{
					MentionedJID: []string{targetJID.String()},
				},
			},
		},
		whatsmeow.SendRequestExtra{ID: meetReminderMessageID(t.reminder.ID)},
	)
	if err != nil {
		return fmt.Errorf("send Meet reminder %d to WhatsApp: %w", t.reminder.ID, err)
	}
	return nil
}

func (t *MeetReminderTask) complete(ctx context.Context, meetStatus string) error {
	if err := t.store.Complete(ctx, t.reminder.ID, meetStatus, t.now); err != nil {
		return fmt.Errorf("complete Meet reminder %d: %w", t.reminder.ID, err)
	}
	return nil
}

func (t *MeetReminderTask) Time() time.Time {
	return t.reminder.RemindAt
}

func (t *MeetReminderTask) ID() string {
	return fmt.Sprintf("meet-reminder-%d", t.reminder.ID)
}

func meetReminderMessageID(id int64) types.MessageID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("meet-reminder:%d:whatsapp", id)))
	return types.MessageID(strings.ToUpper(hex.EncodeToString(sum[:10])))
}
