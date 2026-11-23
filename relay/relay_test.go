package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedConfigDecryptsWithInstalledTokens(t *testing.T) {
	tests := []struct {
		role string
		env  string
	}{
		{RoleWindows, "RELAY_WINDOWS_TOKEN"},
		{RoleTermux, "RELAY_TERMUX_TOKEN"},
	}
	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			token := os.Getenv(test.env)
			if token == "" {
				t.Skip(test.env + " is not set")
			}
			settings, err := Load(test.role, token)
			if err != nil {
				t.Fatal(err)
			}
			if len(settings.BearerToken()) != 64 ||
				!strings.HasPrefix(settings.DiscoveryTopic(), "asg-") {
				t.Fatal("decrypted relay settings are invalid")
			}
		})
	}
}

func TestAnnouncementEncryptionRoundTrip(t *testing.T) {
	settings := Settings{
		broker: "https://ntfy.sh",
		secret: []byte("01234567890123456789012345678901"),
	}
	want := Announcement{
		URL:       "https://example.trycloudflare.com",
		Published: time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC),
	}
	sealed, err := settings.SealAnnouncement(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := settings.OpenAnnouncement(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != want.URL || !got.Published.Equal(want.Published) {
		t.Fatalf("announcement = %#v, want %#v", got, want)
	}
}

func TestValidTunnelAnnouncementRejectsOtherHostsAndStaleValues(t *testing.T) {
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	valid, err := validTunnelAnnouncement(Announcement{
		URL:       "https://quiet-field.trycloudflare.com",
		Published: now.Add(-time.Minute),
	}, now)
	if err != nil || valid.Host != "quiet-field.trycloudflare.com" {
		t.Fatalf("valid announcement = %v, %v", valid, err)
	}
	for _, announcement := range []Announcement{
		{URL: "http://quiet-field.trycloudflare.com", Published: now},
		{URL: "https://trycloudflare.com.attacker.test", Published: now},
		{URL: "https://quiet-field.trycloudflare.com/path", Published: now},
		{URL: "https://quiet-field.trycloudflare.com", Published: now.Add(-11 * time.Minute)},
	} {
		if _, err := validTunnelAnnouncement(announcement, now); err == nil {
			t.Fatalf("accepted invalid announcement %#v", announcement)
		}
	}
}

func TestGatewayAuthenticatesLocallyAndRewritesRemoteToken(t *testing.T) {
	var authorization string
	remote := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		authorization = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Access-Control-Allow-Origin", request.Header.Get("Origin"))
		_, _ = io.WriteString(writer, `{"status":"ok"}`)
	}))
	defer remote.Close()
	target, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := &endpointState{}
	endpoint.set(target)
	handler := gatewayHandler(endpoint, "local-token", "remote-token")

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(
		http.MethodGet,
		"http://localhost/healthz",
		nil,
	))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost/healthz", nil)
	request.Header.Set("Authorization", "Bearer local-token")
	request.Header.Set("Origin", "chrome-extension://abcdefghijklmnop")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("gateway status = %d, body %s", response.Code, response.Body)
	}
	if authorization != "Bearer remote-token" {
		t.Fatalf("remote authorization = %q", authorization)
	}
	if response.Header().Get("Access-Control-Allow-Origin") !=
		"chrome-extension://abcdefghijklmnop" {
		t.Fatalf("CORS origin = %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
	if len(response.Header().Values("Access-Control-Allow-Origin")) != 1 {
		t.Fatalf("duplicate CORS headers: %#v", response.Header())
	}
}
