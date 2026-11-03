package meetmonitor

import (
	"context"
	"testing"
	"time"
)

func TestMeetMessagesRemainPendingUntilAcknowledged(t *testing.T) {
	now := time.Date(2026, time.November, 3, 14, 0, 0, 0, time.UTC)
	service := NewService(nil, nil, "jpy-ymgz-tdo", nil, func() time.Time { return now })

	first, err := service.QueueMessage(context.Background(), "Hello from WhatsApp")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.QueueMessage(context.Background(), "Second message")
	if err != nil {
		t.Fatal(err)
	}

	pending, err := service.PendingMessage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || pending.ID != first.ID || pending.Text != "Hello from WhatsApp" {
		t.Fatalf("pending = %#v, want first message", pending)
	}

	acknowledged, err := service.AcknowledgeMessage(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !acknowledged {
		t.Fatal("first message was not acknowledged")
	}

	pending, err = service.PendingMessage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || pending.ID != second.ID {
		t.Fatalf("pending = %#v, want second message", pending)
	}
}

func TestMeetMessageValidationRejectsEmptyAndOversizedText(t *testing.T) {
	service := NewService(nil, nil, "jpy-ymgz-tdo", nil, nil)

	if _, err := service.QueueMessage(context.Background(), "   "); err != ErrInvalidMessage {
		t.Fatalf("empty error = %v, want %v", err, ErrInvalidMessage)
	}

	oversized := make([]rune, 1001)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if _, err := service.QueueMessage(context.Background(), string(oversized)); err != ErrInvalidMessage {
		t.Fatalf("oversized error = %v, want %v", err, ErrInvalidMessage)
	}
}
