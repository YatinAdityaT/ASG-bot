package messageapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMeetEventsAreFormattedAndQueued(t *testing.T) {
	tests := []struct {
		name     string
		event    map[string]any
		expected string
	}{
		{"join", meetEvent("join", ""), "🎥 Meet abc-defg-hij: Alex joined the call."},
		{"leave", meetEvent("leave", ""), "🎥 Meet abc-defg-hij: Alex left the call."},
		{"mic muted", meetEvent("mic_muted", ""), "🎙️ Meet abc-defg-hij: Alex muted their mic."},
		{"mic unmuted", meetEvent("mic_unmuted", ""), "🎙️ Meet abc-defg-hij: Alex unmuted their mic."},
		{"camera on", meetEvent("cam_on", ""), "📷 Meet abc-defg-hij: Alex turned their camera on."},
		{"camera off", meetEvent("cam_off", ""), "📷 Meet abc-defg-hij: Alex turned their camera off."},
		{"rename", meetEvent("rename", ""), "✏️ Meet abc-defg-hij: A participant is now named Alex."},
		{"chat", meetEvent("chat_message", "Hello"), `💬 Meet abc-defg-hij: Alex said: Hello`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, store := newFixture(t, func() time.Time { return fixedNow })
			response := submitMeet(t, server.Handler(), test.event)
			if response.Code != http.StatusAccepted {
				t.Fatalf("status = %d, body %q", response.Code, response.Body.String())
			}
			var submitted struct {
				ID string `json:"id"`
			}
			decodeResponse(t, response, &submitted)
			job := getJob(t, store, submitted.ID)
			if job.ChatID != targetGroup {
				t.Fatalf("destination = %q, want configured group", job.ChatID)
			}
			if job.Message != test.expected {
				t.Fatalf("message = %q, want %q", job.Message, test.expected)
			}
			if !strings.HasPrefix(job.IdempotencyKey, "meet-") {
				t.Fatalf("idempotency key = %q, want meet- prefix", job.IdempotencyKey)
			}
		})
	}
}

func TestMeetEventReplayIsIdempotent(t *testing.T) {
	server, store := newFixture(t, func() time.Time { return fixedNow })
	event := meetEvent("join", "")

	first := submitMeet(t, server.Handler(), event)
	second := submitMeet(t, server.Handler(), event)

	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay responses differ: %q, %q", first.Body.String(), second.Body.String())
	}
	jobs, err := store.Due(t.Context(), fixedNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("queued jobs = %d, want 1", len(jobs))
	}
}

func TestMeetChatReplayIgnoresObservationTimestamp(t *testing.T) {
	server, store := newFixture(t, func() time.Time { return fixedNow })
	firstEvent := meetEvent("chat_message", "Hello")
	secondEvent := meetEvent("chat_message", "Hello")
	secondEvent["ts"] = "2026-10-27T08:00:02Z"

	first := submitMeet(t, server.Handler(), firstEvent)
	second := submitMeet(t, server.Handler(), secondEvent)

	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d; bodies = %q, %q", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	jobs, err := store.Due(t.Context(), fixedNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("queued jobs = %d, want 1", len(jobs))
	}
}

func TestMeetStateIdentityIncludesObservationTimestamp(t *testing.T) {
	server, store := newFixture(t, func() time.Time { return fixedNow })
	firstEvent := meetEvent("cam_on", "")
	secondEvent := meetEvent("cam_on", "")
	secondEvent["ts"] = "2026-10-27T08:00:02Z"

	first := submitMeet(t, server.Handler(), firstEvent)
	second := submitMeet(t, server.Handler(), secondEvent)

	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d; bodies = %q, %q", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	jobs, err := store.Due(t.Context(), fixedNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("queued jobs = %d, want 2", len(jobs))
	}
}

func TestMeetEventIdempotencyUsesNormalizedEventIdentity(t *testing.T) {
	server, store := newFixture(t, func() time.Time { return fixedNow })
	firstEvent := meetEvent("join", "")
	delete(firstEvent, "text")
	secondEvent := meetEvent("join", "   ")
	secondEvent["cam"] = true
	secondEvent["mic"] = "unmuted"
	secondEvent["self"] = true
	secondEvent["host"] = true

	first := submitMeet(t, server.Handler(), firstEvent)
	second := submitMeet(t, server.Handler(), secondEvent)

	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d; bodies = %q, %q", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("semantic replay responses differ: %q, %q", first.Body.String(), second.Body.String())
	}
	jobs, err := store.Due(t.Context(), fixedNow, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("queued jobs = %d, want 1", len(jobs))
	}
}

func TestMeetEventSanitizesParticipantControlledText(t *testing.T) {
	server, store := newFixture(t, func() time.Time { return fixedNow })
	event := meetEvent("chat_message", "hello\r\nworld\u202e")
	event["name"] = "Al\u202eex\nAdmin"

	response := submitMeet(t, server.Handler(), event)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %q", response.Code, response.Body.String())
	}
	var submitted struct {
		ID string `json:"id"`
	}
	decodeResponse(t, response, &submitted)
	job := getJob(t, store, submitted.ID)
	if job.Message != "💬 Meet abc-defg-hij: Alex Admin said: hello world" {
		t.Fatalf("sanitized message = %q", job.Message)
	}
}

func TestMeetEventValidation(t *testing.T) {
	tests := []struct {
		name  string
		event map[string]any
	}{
		{"unsupported type", meetEvent("screen_shared", "")},
		{"blank id", withMeetField(meetEvent("join", ""), "id", " ")},
		{"blank meeting", withMeetField(meetEvent("join", ""), "meeting", "")},
		{"blank name", withMeetField(meetEvent("join", ""), "name", "")},
		{"bad timestamp", withMeetField(meetEvent("join", ""), "ts", "tomorrow")},
		{"chat without text", meetEvent("chat_message", "")},
		{"text on non-chat", meetEvent("join", "unexpected")},
		{"oversized name", withMeetField(meetEvent("join", ""), "name", strings.Repeat("界", 201))},
		{"oversized id", withMeetField(meetEvent("join", ""), "id", strings.Repeat("i", 256))},
		{"oversized meeting", withMeetField(meetEvent("join", ""), "meeting", strings.Repeat("m", 256))},
		{"oversized chat", meetEvent("chat_message", strings.Repeat("界", 3501))},
		{"unknown field", withMeetField(meetEvent("join", ""), "destination", "attacker@g.us")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := newFixture(t, func() time.Time { return fixedNow })
			response := submitMeet(t, server.Handler(), test.event)
			assertError(t, response, http.StatusBadRequest, "invalid_request")
		})
	}
}

func TestMeetEventRequestPolicy(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })

	wrongType := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/meet-events",
		strings.NewReader(`{}`), map[string]string{"Content-Type": "text/plain"})
	assertError(t, wrongType, http.StatusUnsupportedMediaType, "unsupported_media_type")

	malformed := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/meet-events",
		strings.NewReader(`{`), map[string]string{"Content-Type": "application/json"})
	assertError(t, malformed, http.StatusBadRequest, "invalid_request")

	oversized := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/meet-events",
		strings.NewReader(`{"type":"join","padding":"`+strings.Repeat("x", 17*1024)+`"}`),
		map[string]string{"Content-Type": "application/json"})
	assertError(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
}

func meetEvent(eventType, text string) map[string]any {
	return map[string]any{
		"type":    eventType,
		"id":      "participant-1",
		"meeting": "abc-defg-hij",
		"ts":      "2026-10-27T08:00:00Z",
		"name":    "Alex",
		"cam":     nil,
		"mic":     nil,
		"self":    false,
		"host":    false,
		"text":    text,
	}
}

func withMeetField(event map[string]any, key string, value any) map[string]any {
	event[key] = value
	return event
}

func submitMeet(t *testing.T, handler http.Handler, event map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return authenticatedRequest(t, handler, http.MethodPost, "/v1/meet-events", strings.NewReader(string(body)),
		map[string]string{"Content-Type": "application/json"})
}
