package messageapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"whatsappBot/config"
	appdb "whatsappBot/db"
	"whatsappBot/messageapi"
	"whatsappBot/outbound"
)

const (
	apiToken      = "0123456789abcdef0123456789abcdef"
	allowedOrigin = "chrome-extension://abcdefghijklmnop"
	targetGroup   = "120363418911552290@g.us"
)

var fixedNow = time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)

func TestAuthenticationIsRequiredAndGeneric(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })

	var responses []string
	for _, authorization := range []string{"", "Bearer wrong", "Basic " + apiToken} {
		response := request(t, server.Handler(), http.MethodGet, "/healthz", nil, map[string]string{
			"Authorization": authorization,
		})
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status = %d, want 401", authorization, response.Code)
		}
		responses = append(responses, response.Body.String())
		assertJSONContentType(t, response)
	}
	if responses[0] != responses[1] || responses[1] != responses[2] {
		t.Fatalf("authentication responses differ: %#v", responses)
	}
	if responses[0] != `{"error":{"code":"unauthorized","message":"unauthorized"}}`+"\n" {
		t.Fatalf("unauthorized body = %q", responses[0])
	}

	response := authenticatedRequest(t, server.Handler(), http.MethodGet, "/healthz", nil, nil)
	if response.Code != http.StatusOK || response.Body.String() != `{"status":"ok"}`+"\n" {
		t.Fatalf("authorized health response = %d %q", response.Code, response.Body.String())
	}
}

func TestCORSExactOriginAndPreflight(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })

	allowed := authenticatedRequest(t, server.Handler(), http.MethodGet, "/healthz", nil, map[string]string{
		"Origin": allowedOrigin,
	})
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed origin status = %d", allowed.Code)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("allow origin = %q", got)
	}
	if got := allowed.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, want Origin", got)
	}

	absent := authenticatedRequest(t, server.Handler(), http.MethodGet, "/healthz", nil, nil)
	if absent.Code != http.StatusOK || absent.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("absent origin response = %d, allow-origin %q", absent.Code, absent.Header().Get("Access-Control-Allow-Origin"))
	}

	denied := authenticatedRequest(t, server.Handler(), http.MethodGet, "/healthz", nil, map[string]string{
		"Origin": allowedOrigin + ".evil",
	})
	if denied.Code != http.StatusForbidden || denied.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("denied origin response = %d, allow-origin %q", denied.Code, denied.Header().Get("Access-Control-Allow-Origin"))
	}

	preflight := request(t, server.Handler(), http.MethodOptions, "/v1/messages", nil, map[string]string{
		"Origin":                         allowedOrigin,
		"Access-Control-Request-Method":  http.MethodPost,
		"Access-Control-Request-Headers": "authorization, content-type",
	})
	if preflight.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, body %q", preflight.Code, preflight.Body.String())
	}
	if preflight.Header().Get("Access-Control-Allow-Origin") != allowedOrigin ||
		preflight.Header().Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" ||
		preflight.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" {
		t.Fatalf("unexpected preflight headers: %#v", preflight.Header())
	}
}

func TestSubmitRejectsInvalidContentAndBody(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })

	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
		code        string
	}{
		{"missing content type", "", `{}`, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"wrong content type", "text/plain", `{}`, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"malformed JSON", "application/json", `{`, http.StatusBadRequest, "invalid_request"},
		{"unknown field", "application/json", `{"text":"hello","idempotencyKey":"key","destination":"attacker@g.us"}`, http.StatusBadRequest, "invalid_request"},
		{"trailing JSON", "application/json", `{"text":"hello","idempotencyKey":"key"} {}`, http.StatusBadRequest, "invalid_request"},
		{"empty text", "application/json", `{"text":"","idempotencyKey":"key"}`, http.StatusBadRequest, "invalid_request"},
		{"blank text", "application/json", `{"text":"   ","idempotencyKey":"key"}`, http.StatusBadRequest, "invalid_request"},
		{"oversized text", "application/json", `{"text":"` + strings.Repeat("界", 4097) + `","idempotencyKey":"key"}`, http.StatusBadRequest, "invalid_request"},
		{"missing key", "application/json", `{"text":"hello"}`, http.StatusBadRequest, "invalid_request"},
		{"oversized key", "application/json", `{"text":"hello","idempotencyKey":"` + strings.Repeat("k", 256) + `"}`, http.StatusBadRequest, "invalid_request"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/messages",
				strings.NewReader(test.body), map[string]string{"Content-Type": test.contentType})
			assertError(t, response, test.status, test.code)
		})
	}

	oversizedBody := `{"text":"` + strings.Repeat("x", 17*1024) + `","idempotencyKey":"key"}`
	response := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/messages",
		strings.NewReader(oversizedBody), map[string]string{"Content-Type": "application/json"})
	assertError(t, response, http.StatusRequestEntityTooLarge, "request_too_large")
}

func TestSubmitValidatesSendAt(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })

	tests := []struct {
		name   string
		sendAt string
	}{
		{"invalid", "tomorrow"},
		{"equal to now", fixedNow.Format(time.RFC3339)},
		{"past", fixedNow.Add(-time.Second).Format(time.RFC3339)},
		{"over one year", fixedNow.AddDate(1, 0, 0).Add(time.Second).Format(time.RFC3339)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := `{"text":"hello","idempotencyKey":"` + test.name + `","sendAt":"` + test.sendAt + `"}`
			response := authenticatedRequest(t, server.Handler(), http.MethodPost, "/v1/messages",
				strings.NewReader(body), map[string]string{"Content-Type": "application/json"})
			assertError(t, response, http.StatusBadRequest, "invalid_request")
		})
	}
}

func TestSubmitImmediateAndScheduledMessages(t *testing.T) {
	server, store := newFixture(t, func() time.Time { return fixedNow })

	immediate := submit(t, server.Handler(), `{"text":"hello now","idempotencyKey":"immediate"}`)
	if immediate.Code != http.StatusAccepted {
		t.Fatalf("immediate status = %d, body %q", immediate.Code, immediate.Body.String())
	}
	var immediateBody struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		SendAt string `json:"sendAt"`
	}
	decodeResponse(t, immediate, &immediateBody)
	if immediateBody.ID == "" || immediateBody.Status != "pending" || immediateBody.SendAt != fixedNow.Format(time.RFC3339) {
		t.Fatalf("unexpected immediate response: %#v", immediateBody)
	}
	immediateJob := getJob(t, store, immediateBody.ID)
	if immediateJob.ChatID != targetGroup || immediateJob.Message != "hello now" {
		t.Fatalf("stored destination/message = %q/%q", immediateJob.ChatID, immediateJob.Message)
	}

	sendAt := fixedNow.Add(time.Hour)
	scheduled := submit(t, server.Handler(), `{"text":"hello later","idempotencyKey":"scheduled","sendAt":"`+sendAt.Format(time.RFC3339)+`"}`)
	if scheduled.Code != http.StatusAccepted {
		t.Fatalf("scheduled status = %d, body %q", scheduled.Code, scheduled.Body.String())
	}
	var scheduledBody map[string]any
	decodeResponse(t, scheduled, &scheduledBody)
	if scheduledBody["sendAt"] != sendAt.Format(time.RFC3339) {
		t.Fatalf("scheduled sendAt = %#v", scheduledBody["sendAt"])
	}
}

func TestSubmitIdempotentReplayAndConflict(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })
	payload := `{"text":"same","idempotencyKey":"replay","sendAt":"` + fixedNow.Add(time.Hour).Format(time.RFC3339) + `"}`

	first := submit(t, server.Handler(), payload)
	second := submit(t, server.Handler(), payload)
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted || first.Body.String() != second.Body.String() {
		t.Fatalf("replay responses = (%d %q), (%d %q)", first.Code, first.Body.String(), second.Code, second.Body.String())
	}

	conflict := submit(t, server.Handler(), `{"text":"different","idempotencyKey":"replay","sendAt":"`+fixedNow.Add(time.Hour).Format(time.RFC3339)+`"}`)
	assertError(t, conflict, http.StatusConflict, "idempotency_conflict")
}

func TestGetMessageStatusAndNotFound(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })
	created := submit(t, server.Handler(), `{"text":"status","idempotencyKey":"status"}`)
	var submitted struct {
		ID string `json:"id"`
	}
	decodeResponse(t, created, &submitted)

	found := authenticatedRequest(t, server.Handler(), http.MethodGet, "/v1/messages/"+submitted.ID, nil, nil)
	if found.Code != http.StatusOK {
		t.Fatalf("found status = %d, body %q", found.Code, found.Body.String())
	}
	var status struct {
		ID        string  `json:"id"`
		Status    string  `json:"status"`
		Attempts  int     `json:"attempts"`
		SendAt    string  `json:"sendAt"`
		SentAt    *string `json:"sentAt"`
		LastError *string `json:"lastError"`
	}
	decodeResponse(t, found, &status)
	if status.ID != submitted.ID || status.Status != "pending" || status.Attempts != 0 ||
		status.SendAt != fixedNow.Format(time.RFC3339) || status.SentAt != nil || status.LastError != nil {
		t.Fatalf("unexpected status response: %#v", status)
	}

	notFound := authenticatedRequest(t, server.Handler(), http.MethodGet, "/v1/messages/999999", nil, nil)
	assertError(t, notFound, http.StatusNotFound, "not_found")
}

func TestHealthAndMessageStatusRequireAuthentication(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })
	for _, path := range []string{"/healthz", "/v1/messages/1"} {
		response := request(t, server.Handler(), http.MethodGet, path, nil, nil)
		assertError(t, response, http.StatusUnauthorized, "unauthorized")
	}
}

func TestUnsupportedMethodsAndPaths(t *testing.T) {
	server, _ := newFixture(t, func() time.Time { return fixedNow })
	tests := []struct {
		method string
		path   string
		status int
		code   string
	}{
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/v1/messages", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodPost, "/v1/messages/1", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/missing", http.StatusNotFound, "not_found"},
		{http.MethodGet, "/v1/messages/not-a-number", http.StatusNotFound, "not_found"},
	}
	for _, test := range tests {
		response := authenticatedRequest(t, server.Handler(), test.method, test.path, nil,
			map[string]string{"Content-Type": "application/json"})
		assertError(t, response, test.status, test.code)
	}
}

func TestRateLimitIsThirtyRequestsPerMinutePerRemoteAddress(t *testing.T) {
	now := fixedNow
	server, _ := newFixture(t, func() time.Time { return now })

	for i := 0; i < 30; i++ {
		response := authenticatedRequestFrom(t, server.Handler(), "192.0.2.1:1234", http.MethodGet, "/healthz", nil, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i+1, response.Code)
		}
	}
	limited := authenticatedRequestFrom(t, server.Handler(), "192.0.2.1:5678", http.MethodGet, "/healthz", nil, nil)
	assertError(t, limited, http.StatusTooManyRequests, "rate_limited")
	if limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q", limited.Header().Get("Retry-After"))
	}

	otherAddress := authenticatedRequestFrom(t, server.Handler(), "192.0.2.2:1234", http.MethodGet, "/healthz", nil, nil)
	if otherAddress.Code != http.StatusOK {
		t.Fatalf("other address status = %d", otherAddress.Code)
	}

	now = now.Add(time.Minute)
	reset := authenticatedRequestFrom(t, server.Handler(), "192.0.2.1:1234", http.MethodGet, "/healthz", nil, nil)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset window status = %d", reset.Code)
	}
}

func TestStartReturnsBindError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server, _ := newFixtureWithConfig(t, func() time.Time { return fixedNow }, config.Config{
		APIAddr:  listener.Addr().String(),
		APIToken: apiToken,
	})
	result := <-server.Start(context.Background())
	if result == nil {
		t.Fatal("Start() error = nil, want bind error")
	}
}

func TestStartShutsDownCleanlyWhenContextIsCanceled(t *testing.T) {
	server, _ := newFixtureWithConfig(t, func() time.Time { return fixedNow }, config.Config{
		APIAddr:  "127.0.0.1:0",
		APIToken: apiToken,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := server.Start(ctx)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start() completion error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start() did not finish after cancellation")
	}
}

func newFixture(t *testing.T, now func() time.Time) (*messageapi.Server, *outbound.Store) {
	t.Helper()
	return newFixtureWithConfig(t, now, config.Config{
		APIAddr:           "127.0.0.1:0",
		APIToken:          apiToken,
		APITargetGroup:    targetGroup,
		APIAllowedOrigins: []string{allowedOrigin},
	})
}

func newFixtureWithConfig(t *testing.T, now func() time.Time, cfg config.Config) (*messageapi.Server, *outbound.Store) {
	t.Helper()
	_, database, err := appdb.SetupDB(context.Background(), filepath.Join(t.TempDir(), "messageapi.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	store := outbound.NewStore(database, cfg.APITargetGroup,
		outbound.WithClock(now),
		outbound.WithMessageIDGenerator(func() (string, error) {
			var id uint64
			id = messageID.Add(1)
			return fmt.Sprintf("%020X", id), nil
		}),
	)
	return messageapi.New(cfg, store, now), store
}

var messageID atomic.Uint64

func submit(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedRequest(t, handler, http.MethodPost, "/v1/messages", strings.NewReader(body),
		map[string]string{"Content-Type": "application/json"})
}

func authenticatedRequest(t *testing.T, handler http.Handler, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return authenticatedRequestFrom(t, handler, "192.0.2.1:1234", method, path, body, headers)
}

func authenticatedRequestFrom(t *testing.T, handler http.Handler, remoteAddress, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	if headers == nil {
		headers = make(map[string]string)
	}
	headers["Authorization"] = "Bearer " + apiToken
	return requestFrom(t, handler, remoteAddress, method, path, body, headers)
}

func request(t *testing.T, handler http.Handler, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return requestFrom(t, handler, "192.0.2.1:1234", method, path, body, headers)
}

func requestFrom(t *testing.T, handler http.Handler, remoteAddress, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = remoteAddress
	for name, value := range headers {
		if value != "" {
			req.Header.Set(name, value)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func assertError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body %q", response.Code, status, response.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error = %#v, want code %q and non-empty message", body.Error, code)
	}
}

func assertJSONContentType(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	assertJSONContentType(t, response)
	decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func getJob(t *testing.T, store *outbound.Store, id string) *outbound.Job {
	t.Helper()
	var numericID int64
	if _, err := fmt.Sscan(id, &numericID); err != nil {
		t.Fatalf("parse job ID %q: %v", id, err)
	}
	job, err := store.Get(context.Background(), numericID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
