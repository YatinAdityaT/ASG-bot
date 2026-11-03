package messageapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"whatsappBot/meetmonitor"
	"whatsappBot/messageapi"
)

func TestMeetMessageAPIKeepsCommandUntilExtensionAcknowledgesIt(t *testing.T) {
	service := meetmonitor.NewService(nil, nil, meetStateMeeting, nil, func() time.Time {
		return fixedNow
	})
	server := messageapi.New(
		meetStateConfig(),
		nil,
		func() time.Time { return fixedNow },
		messageapi.WithMeetService(service),
	)

	empty := authenticatedRequest(t, server.Handler(), http.MethodGet, "/v1/meet-command", nil, nil)
	if empty.Code != http.StatusNoContent {
		t.Fatalf("empty status = %d, body %q", empty.Code, empty.Body.String())
	}

	queued, err := service.QueueMessage(context.Background(), "Hello from WhatsApp")
	if err != nil {
		t.Fatal(err)
	}
	pending := authenticatedRequest(t, server.Handler(), http.MethodGet, "/v1/meet-command", nil, nil)
	if pending.Code != http.StatusOK {
		t.Fatalf("pending status = %d, body %q", pending.Code, pending.Body.String())
	}
	var got meetmonitor.Message
	decodeResponse(t, pending, &got)
	if got.ID != queued.ID || got.Text != queued.Text {
		t.Fatalf("pending = %#v, want %#v", got, queued)
	}

	body, _ := json.Marshal(map[string]string{"id": queued.ID})
	ack := authenticatedRequest(
		t,
		server.Handler(),
		http.MethodPost,
		"/v1/meet-command/ack",
		strings.NewReader(string(body)),
		map[string]string{"Content-Type": "application/json"},
	)
	if ack.Code != http.StatusOK {
		t.Fatalf("ack status = %d, body %q", ack.Code, ack.Body.String())
	}

	empty = authenticatedRequest(t, server.Handler(), http.MethodGet, "/v1/meet-command", nil, nil)
	if empty.Code != http.StatusNoContent {
		t.Fatalf("post-ack status = %d, body %q", empty.Code, empty.Body.String())
	}
}
