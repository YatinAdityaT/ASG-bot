package messageapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsappBot/config"
	appdb "whatsappBot/db"
	"whatsappBot/meetmonitor"
	"whatsappBot/messageapi"
	"whatsappBot/outbound"
)

const (
	meetStateMeeting = "jpy-ymgz-tdo"
	meetStateSession = "8f14e45f-ea2f-4f41-9f6d-44c5b95c6301"
)

func TestMeetStateAcceptsBaselineAndQueuesChanges(t *testing.T) {
	server, database := newMeetStateFixture(t, true)

	baseline := postMeetState(t, server.Handler(), meetStateSnapshot(1))
	if baseline.Code != http.StatusOK {
		t.Fatalf("baseline status = %d, body %q", baseline.Code, baseline.Body.String())
	}
	var got meetmonitor.Result
	decodeResponse(t, baseline, &got)
	want := meetmonitor.Result{
		Status:            "baseline",
		AcceptedSequence:  1,
		ServerTime:        fixedNow,
		MonitorOnline:     true,
		WhatsAppConnected: true,
		QueuedChanges:     0,
	}
	if got != want {
		t.Fatalf("baseline result = %#v, want %#v", got, want)
	}

	changed := meetStateSnapshot(2)
	changed.Participants = []meetmonitor.Participant{{
		ID: "participant-1", Name: "Alice", Cam: true, Mic: "muted",
	}}
	response := postMeetState(t, server.Handler(), changed)
	if response.Code != http.StatusOK {
		t.Fatalf("changed status = %d, body %q", response.Code, response.Body.String())
	}
	decodeResponse(t, response, &got)
	if got.Status != "changed" || got.AcceptedSequence != 2 || got.QueuedChanges != 1 {
		t.Fatalf("changed result = %#v", got)
	}

	var count int
	var message string
	if err := database.QueryRow("SELECT COUNT(*), message FROM outbound_jobs").Scan(&count, &message); err != nil {
		t.Fatal(err)
	}
	if count != 1 || message != "🎥 Alice joined the call." {
		t.Fatalf("outbound jobs = %d, message %q", count, message)
	}
}

func TestMeetStateReportsLiveConnectivity(t *testing.T) {
	for _, connected := range []bool{true, false} {
		t.Run(fmt.Sprintf("connected=%t", connected), func(t *testing.T) {
			server, _ := newMeetStateFixture(t, connected)
			response := postMeetState(t, server.Handler(), meetStateSnapshot(1))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body %q", response.Code, response.Body.String())
			}
			var result meetmonitor.Result
			decodeResponse(t, response, &result)
			if result.WhatsAppConnected != connected {
				t.Fatalf("whatsAppConnected = %t, want %t", result.WhatsAppConnected, connected)
			}
		})
	}
}

func TestMeetStateMapsSequenceResultsAndConflict(t *testing.T) {
	server, _ := newMeetStateFixture(t, true)
	accepted := meetStateSnapshot(2)
	if response := postMeetState(t, server.Handler(), accepted); response.Code != http.StatusOK {
		t.Fatalf("baseline status = %d, body %q", response.Code, response.Body.String())
	}

	replay := postMeetState(t, server.Handler(), accepted)
	var result meetmonitor.Result
	decodeResponse(t, replay, &result)
	if replay.Code != http.StatusOK || result.Status != "replay" || result.AcceptedSequence != 2 {
		t.Fatalf("replay = %d %#v", replay.Code, result)
	}

	stale := postMeetState(t, server.Handler(), meetStateSnapshot(1))
	decodeResponse(t, stale, &result)
	if stale.Code != http.StatusOK || result.Status != "stale" || result.AcceptedSequence != 2 {
		t.Fatalf("stale = %d %#v", stale.Code, result)
	}

	conflictSnapshot := accepted
	conflictSnapshot.ExtensionVersion = "1.4.1"
	conflict := postMeetState(t, server.Handler(), conflictSnapshot)
	assertErrorMessage(t, conflict, http.StatusConflict, "sequence_conflict",
		"snapshot conflicts with the accepted sequence")
}

func TestMeetStateRejectsInvalidContentAndSnapshots(t *testing.T) {
	validJSON := encodeMeetState(t, meetStateSnapshot(1))
	requestTests := []struct {
		name        string
		contentType string
		body        string
		status      int
		code        string
	}{
		{"malformed JSON", "application/json", `{`, http.StatusBadRequest, "invalid_request"},
		{"unknown field", "application/json", strings.TrimSuffix(validJSON, "}") + `,"unknown":true}`, http.StatusBadRequest, "invalid_request"},
		{"trailing JSON", "application/json", validJSON + `{}`, http.StatusBadRequest, "invalid_request"},
		{"wrong media type", "text/plain", validJSON, http.StatusUnsupportedMediaType, "unsupported_media_type"},
	}
	for _, test := range requestTests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := newMeetStateFixture(t, true)
			response := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/meet-state",
				strings.NewReader(test.body), map[string]string{"Content-Type": test.contentType})
			assertError(t, response, test.status, test.code)
		})
	}

	snapshotTests := []struct {
		name   string
		mutate func(*meetmonitor.Snapshot)
	}{
		{"wrong meeting", func(snapshot *meetmonitor.Snapshot) { snapshot.Meeting = "other" }},
		{"invalid UUID", func(snapshot *meetmonitor.Snapshot) { snapshot.SessionID = "not-a-uuid" }},
		{"invalid time", func(snapshot *meetmonitor.Snapshot) { snapshot.CapturedAt = fixedNow.Add(6 * time.Minute) }},
		{"invalid participant", func(snapshot *meetmonitor.Snapshot) {
			snapshot.Participants = []meetmonitor.Participant{{ID: "", Name: "Alice", Mic: "muted"}}
		}},
	}
	for _, test := range snapshotTests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := newMeetStateFixture(t, true)
			snapshot := meetStateSnapshot(1)
			test.mutate(&snapshot)
			response := postMeetState(t, server.Handler(), snapshot)
			assertErrorMessage(t, response, http.StatusBadRequest, "invalid_request", "invalid request")
		})
	}
}

func TestMeetStateUsesDedicatedBodyLimit(t *testing.T) {
	server, _ := newMeetStateFixture(t, true)
	validJSON := encodeMeetState(t, meetStateSnapshot(1))

	underLimit := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/meet-state",
		strings.NewReader(validJSON+strings.Repeat(" ", 20*1024)),
		map[string]string{"Content-Type": "application/json; charset=utf-8"})
	if underLimit.Code != http.StatusOK {
		t.Fatalf("under-limit status = %d, body %q", underLimit.Code, underLimit.Body.String())
	}

	server, _ = newMeetStateFixture(t, true)
	overLimit := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/meet-state",
		strings.NewReader(validJSON+strings.Repeat(" ", 256*1024)),
		map[string]string{"Content-Type": "application/json"})
	assertErrorMessage(t, overLimit, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
}

func TestMeetStateMethodMissingServiceAndAuthentication(t *testing.T) {
	server, _ := newMeetStateFixture(t, true)
	get := authenticatedRequest(t, server.Handler(), http.MethodGet, "/v1/meet-state", nil, nil)
	assertError(t, get, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := get.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", got)
	}

	cfg := meetStateConfig()
	_, database, err := appdb.SetupDB(context.Background(), filepath.Join(t.TempDir(), "missing-service.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := outbound.NewStore(database, cfg.APITargetGroup)
	withoutService := messageapi.New(cfg, store, func() time.Time { return fixedNow })
	unavailable := authenticatedRequest(t, withoutService.Handler(), http.MethodPost, "/v1/meet-state",
		strings.NewReader(encodeMeetState(t, meetStateSnapshot(1))),
		map[string]string{"Content-Type": "application/json"})
	assertErrorMessage(t, unavailable, http.StatusServiceUnavailable, "service_unavailable", "Meet monitor service unavailable")

	unauthorized := request(t, server.Handler(), http.MethodPost, "/v1/meet-state",
		strings.NewReader(encodeMeetState(t, meetStateSnapshot(1))),
		map[string]string{"Content-Type": "application/json"})
	assertError(t, unauthorized, http.StatusUnauthorized, "unauthorized")
}

func TestMeetStateCORSAndSharedRateLimit(t *testing.T) {
	server, _ := newMeetStateFixture(t, true)
	body := encodeMeetState(t, meetStateSnapshot(1))

	allowed := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/meet-state",
		strings.NewReader(body), map[string]string{"Content-Type": "application/json", "Origin": allowedOrigin})
	if allowed.Code != http.StatusOK || allowed.Header().Get("Access-Control-Allow-Origin") != allowedOrigin {
		t.Fatalf("allowed origin response = %d, headers %#v", allowed.Code, allowed.Header())
	}
	denied := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/meet-state",
		strings.NewReader(body), map[string]string{"Content-Type": "application/json", "Origin": "https://example.com"})
	assertError(t, denied, http.StatusForbidden, "origin_forbidden")

	preflight := requestFrom(t, server.Handler(), "192.0.2.2:1234", http.MethodOptions, "/v1/meet-state", nil,
		map[string]string{"Origin": allowedOrigin, "Access-Control-Request-Method": http.MethodPost})
	if preflight.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, body %q", preflight.Code, preflight.Body.String())
	}

	rateServer, _ := newMeetStateFixture(t, true)
	health := authenticatedRequestFrom(t, rateServer.Handler(), "192.0.2.10:1234", http.MethodGet, "/healthz", nil, nil)
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, body %q", health.Code, health.Body.String())
	}
	for i := 0; i < 29; i++ {
		response := authenticatedRequestFrom(t, rateServer.Handler(), "192.0.2.10:1234", http.MethodPost, "/v1/meet-state",
			strings.NewReader(body), map[string]string{"Content-Type": "application/json"})
		if response.Code != http.StatusOK {
			t.Fatalf("Meet state request %d status = %d, body %q", i+1, response.Code, response.Body.String())
		}
	}
	limited := authenticatedRequestFrom(t, rateServer.Handler(), "192.0.2.10:5678", http.MethodPost, "/v1/meet-state",
		strings.NewReader(body), map[string]string{"Content-Type": "application/json"})
	assertError(t, limited, http.StatusTooManyRequests, "rate_limited")
}

func TestMeetStateConcurrentRequestsPreserveHighestSequence(t *testing.T) {
	server, _ := newMeetStateFixture(t, true)
	if response := postMeetState(t, server.Handler(), meetStateSnapshot(1)); response.Code != http.StatusOK {
		t.Fatalf("baseline status = %d", response.Code)
	}

	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wait sync.WaitGroup
	for _, sequence := range []int64{2, 3} {
		snapshot := meetStateSnapshot(sequence)
		snapshot.ExtensionVersion = fmt.Sprintf("1.4.%d", sequence)
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			responses <- rawMeetStateRequest(server.Handler(), encodeMeetState(t, snapshot), "192.0.2.20:1234")
		}()
	}
	close(start)
	wait.Wait()
	close(responses)
	for response := range responses {
		if response.Code != http.StatusOK {
			t.Fatalf("concurrent status = %d, body %q", response.Code, response.Body.String())
		}
	}

	latest := meetStateSnapshot(3)
	latest.ExtensionVersion = "1.4.3"
	response := postMeetState(t, server.Handler(), latest)
	var result meetmonitor.Result
	decodeResponse(t, response, &result)
	if response.Code != http.StatusOK || result.Status != "replay" || result.AcceptedSequence != 3 {
		t.Fatalf("latest replay = %d %#v", response.Code, result)
	}
}

func TestMeetStateUnexpectedServiceErrorIsInternal(t *testing.T) {
	server, database := newMeetStateFixture(t, true)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	response := postMeetState(t, server.Handler(), meetStateSnapshot(1))
	assertErrorMessage(t, response, http.StatusInternalServerError, "internal_error", "internal server error")
}

func newMeetStateFixture(t *testing.T, connected bool) (*messageapi.Server, *sql.DB) {
	t.Helper()
	cfg := meetStateConfig()
	_, database, err := appdb.SetupDB(context.Background(), filepath.Join(t.TempDir(), "meet-state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var messageID int
	jobs := outbound.NewStore(database, cfg.APITargetGroup,
		outbound.WithClock(func() time.Time { return fixedNow }),
		outbound.WithMessageIDGenerator(func() (string, error) {
			messageID++
			return fmt.Sprintf("%020X", messageID), nil
		}),
	)
	service := meetmonitor.NewService(
		meetmonitor.NewStore(database),
		jobs,
		meetStateMeeting,
		func() bool { return connected },
		func() time.Time { return fixedNow },
	)
	return messageapi.New(cfg, jobs, func() time.Time { return fixedNow }, messageapi.WithMeetService(service)), database
}

func meetStateConfig() config.Config {
	return config.Config{
		APIAddr:           "127.0.0.1:0",
		APIToken:          apiToken,
		APITargetGroup:    targetGroup,
		APIAllowedOrigins: []string{allowedOrigin},
		MeetCode:          meetStateMeeting,
	}
}

func meetStateSnapshot(sequence int64) meetmonitor.Snapshot {
	return meetmonitor.Snapshot{
		ProtocolVersion:   1,
		Meeting:           meetStateMeeting,
		SessionID:         meetStateSession,
		Sequence:          sequence,
		CapturedAt:        fixedNow,
		ExtensionVersion:  "1.4.0",
		ForwardingEnabled: true,
		Participants:      []meetmonitor.Participant{},
	}
}

func encodeMeetState(t *testing.T, snapshot meetmonitor.Snapshot) string {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func postMeetState(t *testing.T, handler http.Handler, snapshot meetmonitor.Snapshot) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedRequest(t, handler, http.MethodPost, "/v1/meet-state",
		strings.NewReader(encodeMeetState(t, snapshot)),
		map[string]string{"Content-Type": "application/json"})
}

func rawMeetStateRequest(handler http.Handler, body, remoteAddress string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/meet-state", strings.NewReader(body))
	request.RemoteAddr = remoteAddress
	request.Header.Set("Authorization", "Bearer "+apiToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertErrorMessage(t *testing.T, response *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	assertError(t, response, status, code)
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Message != message {
		t.Fatalf("message = %q, want %q", body.Error.Message, message)
	}
}
