package meetreminder_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	appdb "whatsappBot/db"
	"whatsappBot/identity"
	"whatsappBot/meetreminder"
	"whatsappBot/utils"
)

func TestCreateFromMeetStoresRecurringSeries(t *testing.T) {
	ctx := context.Background()
	_, database, err := appdb.SetupDB(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	person, err := identity.NewStore(database).Add(
		ctx,
		"yatin",
		"Yatin",
		"916355442207@s.whatsapp.net",
		"Yatin T",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 16, 13, 0, 0, 0, time.Local)
	service := meetreminder.NewService(
		meetreminder.NewStore(database, func() time.Time { return now }),
		identity.NewStore(database),
		func() time.Time { return now },
	)

	result, err := service.CreateFromMeet(ctx, meetreminder.CommandRequest{
		SourceKey:      "meet-event-1",
		MeetingCode:    "jpy-ymgz-tdo",
		SenderMeetName: "Yatin T",
		Text:           "/remind me to eat every day at 2pm",
		WhatsAppChatID: "123@g.us",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Reminder.Requester.ID != person.ID ||
		result.Reminder.Body != "eat" ||
		result.Reminder.Recurrence.Kind != utils.RecurrenceDaily ||
		result.Reminder.Recurrence.Label() != "every day" {
		t.Fatalf("result = %#v", result)
	}
}
