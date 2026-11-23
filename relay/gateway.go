package relay

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	discoveryMaxAge  = 10 * time.Minute
	discoveryRetry   = 10 * time.Second
	gatewayBodyLimit = 32 * 1024
	gatewayShutdown  = 5 * time.Second
)

type endpointState struct {
	mu  sync.RWMutex
	url *url.URL
}

type brokerEvent struct {
	Event   string `json:"event"`
	Message string `json:"message"`
}

func RunWindowsGateway(
	ctx context.Context,
	apiAddr string,
	apiToken string,
	logOutput io.Writer,
) error {
	settings, err := Load(RoleWindows, apiToken)
	if err != nil {
		return err
	}
	if logOutput == nil {
		logOutput = io.Discard
	}
	endpoint := &endpointState{}
	go subscribeToDiscovery(ctx, settings, endpoint, logOutput)

	listener, err := net.Listen("tcp", apiAddr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           gatewayHandler(endpoint, apiToken, settings.BearerToken()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	fmt.Fprintln(logOutput, "Meet relay gateway is listening locally.")

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			gatewayShutdown,
		)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		serverErr := <-done
		return errors.Join(serverErr, shutdownErr)
	}
}

func gatewayHandler(
	endpoint *endpointState,
	localToken string,
	remoteToken string,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !allowExtensionOrigin(writer, request) {
			return
		}
		if request.Method == http.MethodOptions &&
			request.Header.Get("Origin") != "" {
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			writer.Header().Set("Access-Control-Max-Age", "600")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if !tokenMatches(request, localToken) {
			writeGatewayError(writer, http.StatusUnauthorized, "unauthorized")
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, gatewayBodyLimit)
		target := endpoint.get()
		if target == nil {
			writeGatewayError(writer, http.StatusServiceUnavailable, "phone_relay_offline")
			return
		}

		origin := request.Header.Get("Origin")
		writer.Header().Del("Access-Control-Allow-Origin")
		writer.Header().Del("Vary")
		proxy := httputil.NewSingleHostReverseProxy(target)
		originalDirector := proxy.Director
		proxy.Director = func(outbound *http.Request) {
			originalDirector(outbound)
			outbound.Header.Set("Authorization", "Bearer "+remoteToken)
			outbound.Host = target.Host
		}
		proxy.ErrorHandler = func(response http.ResponseWriter, _ *http.Request, err error) {
			if origin != "" {
				response.Header().Set("Vary", "Origin")
				response.Header().Set("Access-Control-Allow-Origin", origin)
			}
			writeGatewayError(response, http.StatusBadGateway, "phone_relay_unreachable")
		}
		proxy.ServeHTTP(writer, request)
	})
}

func subscribeToDiscovery(
	ctx context.Context,
	settings Settings,
	endpoint *endpointState,
	logOutput io.Writer,
) {
	for ctx.Err() == nil {
		err := consumeDiscovery(ctx, settings, endpoint, logOutput)
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(logOutput, "Meet relay discovery disconnected: %v\n", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(discoveryRetry):
		}
	}
}

func consumeDiscovery(
	ctx context.Context,
	settings Settings,
	endpoint *endpointState,
	logOutput io.Writer,
) error {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		settings.SubscribeURL(),
		nil,
	)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ntfy returned %s", response.Status)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 1024), 16*1024)
	for scanner.Scan() {
		var event brokerEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil ||
			event.Event != "message" {
			continue
		}
		announcement, err := settings.OpenAnnouncement(event.Message)
		if err != nil {
			continue
		}
		target, err := validTunnelAnnouncement(announcement, time.Now().UTC())
		if err != nil {
			continue
		}
		if endpoint.set(target) {
			fmt.Fprintln(logOutput, "Meet relay connected to the phone.")
		}
	}
	return scanner.Err()
}

func validTunnelAnnouncement(
	announcement Announcement,
	now time.Time,
) (*url.URL, error) {
	if announcement.Published.IsZero() ||
		announcement.Published.Before(now.Add(-discoveryMaxAge)) ||
		announcement.Published.After(now.Add(time.Minute)) {
		return nil, errors.New("stale relay announcement")
	}
	target, err := url.Parse(announcement.URL)
	if err != nil ||
		target.Scheme != "https" ||
		!strings.HasSuffix(strings.ToLower(target.Hostname()), ".trycloudflare.com") ||
		target.Port() != "" ||
		target.User != nil ||
		(target.Path != "" && target.Path != "/") ||
		target.RawQuery != "" ||
		target.Fragment != "" {
		return nil, errors.New("invalid relay endpoint")
	}
	return target, nil
}

func (state *endpointState) get() *url.URL {
	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.url == nil {
		return nil
	}
	copy := *state.url
	return &copy
}

func (state *endpointState) set(value *url.URL) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	changed := state.url == nil || state.url.String() != value.String()
	copy := *value
	state.url = &copy
	return changed
}

func allowExtensionOrigin(writer http.ResponseWriter, request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	writer.Header().Set("Vary", "Origin")
	parsed, err := url.Parse(origin)
	if err != nil ||
		parsed.Scheme != "chrome-extension" ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Path != "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		writeGatewayError(writer, http.StatusForbidden, "origin_forbidden")
		return false
	}
	writer.Header().Set("Access-Control-Allow-Origin", origin)
	return true
}

func tokenMatches(request *http.Request, expected string) bool {
	const prefix = "Bearer "
	value := request.Header.Get("Authorization")
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	provided := []byte(strings.TrimPrefix(value, prefix))
	wanted := []byte(expected)
	return len(provided) == len(wanted) &&
		subtle.ConstantTimeCompare(provided, wanted) == 1
}

func writeGatewayError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": strings.ReplaceAll(code, "_", " "),
		},
	})
}
