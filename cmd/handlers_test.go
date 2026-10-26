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
	sender := &fakeSender{err: errors.New("send failed")}
	shutdownCalled := false
	manager := NewCommandManager(sender, []string{"456"}, func() {
		shutdownCalled = true
	})

	manager.RouteMessage(nil, context.Background(), messageEvent("/die"))

	if shutdownCalled {
		t.Fatal("shutdown called after goodbye failed")
	}
}
