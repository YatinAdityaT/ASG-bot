package cmd

import (
	"context"
	"errors"
	"testing"
)

func TestDieShutsDownAfterGoodbyeIsSent(t *testing.T) {
	sender := &fakeSender{}
	shutdownCalled := false
	manager := NewCommandManager(sender, []string{"456"}, func() {
		shutdownCalled = true
	})

	manager.RouteMessage(nil, context.Background(), messageEvent("/die"))

	if got := sentText(t, sender); got != "Goodbye cruel world!" {
		t.Fatalf("die reply = %q, want %q", got, "Goodbye cruel world!")
	}
	if !shutdownCalled {
		t.Fatal("shutdown was not called after successful goodbye")
	}
}

func TestDieDoesNotShutDownWhenGoodbyeFails(t *testing.T) {
	sendErr := errors.New("send failed")
	sender := &fakeSender{err: sendErr}
	shutdownCalled := false
	manager := NewCommandManager(sender, []string{"456"}, func() {
		shutdownCalled = true
	})

	err := manager.RouteMessage(nil, context.Background(), messageEvent("/die"))

	if shutdownCalled {
		t.Fatal("shutdown called after goodbye failed")
	}
	if !errors.Is(err, sendErr) {
		t.Fatalf("RouteMessage() error = %v, want %v", err, sendErr)
	}
}

func TestDieAllowsNilShutdown(t *testing.T) {
	sender := &fakeSender{}
	manager := NewCommandManager(sender, []string{"456"}, nil)

	if err := manager.RouteMessage(nil, context.Background(), messageEvent("/die")); err != nil {
		t.Fatalf("RouteMessage() error = %v", err)
	}
}
