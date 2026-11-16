package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appdb "whatsappBot/db"
	"whatsappBot/identity"
	"whatsappBot/meetmonitor"
	"whatsappBot/meetreminder"
	"whatsappBot/utils"
)

type fakeMeetReminderMonitor struct {
	roster  meetmonitor.CallRoster
	queueID string
}

func (monitor *fakeMeetReminderMonitor) CallRoster(context.Context) (meetmonitor.CallRoster, error) {
	return monitor.roster, nil
}

func (monitor *fakeMeetReminderMonitor) QueueMessageWithID(
	_ context.Context,
	id string,
	text string,
) (meetmonitor.Message, error) {
	monitor.queueID = id
	return meetmonitor.Message{ID: id, Text: text}, nil
}

func TestRecurringMeetReminderRecordsDeliveryAndAdvancesSeries(t *testing.T) {
	ctx := context.Background()
	_, database, err := appdb.SetupDB(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	identities := identity.NewStore(database)
	requester, err := identities.Add(
		ctx, "yatin", "Yatin", "916355442207@s.whatsapp.net", "Yatin T",
	)
	if err != nil {
		t.Fatal(err)
	}
	target, err := identities.Add(
		ctx, "disha", "Disha", "919353147701@s.whatsapp.net", "Disha",
	)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.November, 16, 14, 5, 0, 0, time.Local)
	scheduled := time.Date(2026, time.November, 16, 14, 0, 0, 0, time.Local)
	store := meetreminder.NewStore(database, func() time.Time { return now })
	reminder, _, err := store.Create(ctx, meetreminder.CreateRequest{
		SourceKey:         "event-1",
		MeetingCode:       "jpy-ymgz-tdo",
		RequesterPersonID: requester.ID,
		TargetPersonID:    target.ID,
		WhatsAppChatID:    "123@g.us",
		Body:              "eat",
		RemindAt:          scheduled,
		Recurrence:        utils.Recurrence{Kind: utils.RecurrenceDaily},
	})
	if err != nil {
		t.Fatal(err)
	}
	monitor := &fakeMeetReminderMonitor{roster: meetmonitor.CallRoster{
		MonitorState: "online",
		Participants: []meetmonitor.Participant{{Name: "Disha"}},
	}}
	tasks, err := NewMeetReminderProvider(store, monitor).GetDueTasks(ctx, now)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks/error = %d/%v", len(tasks), err)
	}
	sender := &fakeSender{}
	if err := tasks[0].Run(ctx, sender); err != nil {
		t.Fatal(err)
	}

	advanced, err := store.GetBySourceKey(ctx, reminder.SourceKey)
	if err != nil {
		t.Fatal(err)
	}
	wantNext := time.Date(2026, time.November, 17, 14, 0, 0, 0, time.Local)
	if !advanced.RemindAt.Equal(wantNext) || advanced.CompletedAt != nil ||
		advanced.WhatsAppDeliveredAt != nil {
		t.Fatalf("advanced reminder = %#v", advanced)
	}
	if monitor.queueID != meetReminderQueueID(reminder.ID, scheduled) {
		t.Fatalf("queue ID = %q", monitor.queueID)
	}
	var deliveries int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM reminder_deliveries WHERE source_type = 'meet' AND reminder_id = ?",
		reminder.ID,
	).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 || len(sender.extras) != 1 ||
		sender.extras[0].ID != meetReminderMessageID(reminder.ID, scheduled) {
		t.Fatalf("deliveries/extras = %d/%#v", deliveries, sender.extras)
	}
}
