package messageapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"whatsappBot/config"
	"whatsappBot/outbound"
)

const (
	maxBodyBytes      = 16 * 1024
	maxTextRunes      = 4096
	maxIdempotencyKey = 255
	shutdownTimeout   = 5 * time.Second
)

type Server struct {
	config  config.Config
	store   *outbound.Store
	now     func() time.Time
	limiter *fixedWindowLimiter
	origins map[string]struct{}
}

func New(cfg config.Config, store *outbound.Store, now func() time.Time) *Server {
	if now == nil {
		now = time.Now
	}
	origins := make(map[string]struct{}, len(cfg.APIAllowedOrigins))
	for _, origin := range cfg.APIAllowedOrigins {
		origins[origin] = struct{}{}
	}
	return &Server{
		config:  cfg,
		store:   store,
		now:     now,
		limiter: newFixedWindowLimiter(now),
		origins: origins,
	}
}

func (server *Server) Handler() http.Handler {
	return http.HandlerFunc(server.serveHTTP)
}

func (server *Server) Start(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	listener, err := net.Listen("tcp", server.config.APIAddr)
	if err != nil {
		done <- err
		close(done)
		return done
	}

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		defer close(done)

		stopShutdownWatcher := make(chan struct{})
		shutdownResult := make(chan error, 1)
		go func() {
			select {
			case <-ctx.Done():
				shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
				defer cancel()
				shutdownResult <- httpServer.Shutdown(shutdownContext)
			case <-stopShutdownWatcher:
				shutdownResult <- nil
			}
		}()

		serveErr := httpServer.Serve(listener)
		close(stopShutdownWatcher)
		shutdownErr := <-shutdownResult

		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		if serveErr != nil {
			done <- serveErr
			return
		}
		done <- shutdownErr
	}()
	return done
}

func (server *Server) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if !server.limiter.allow(request) {
		writer.Header().Set("Retry-After", "60")
		writeError(writer, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
		return
	}

	origin := request.Header.Get("Origin")
	if origin != "" {
		writer.Header().Set("Vary", "Origin")
		if _, allowed := server.origins[origin]; !allowed {
			writeError(writer, http.StatusForbidden, "origin_forbidden", "origin not allowed")
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
	}

	if request.Method == http.MethodOptions && origin != "" {
		writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		writer.WriteHeader(http.StatusNoContent)
		return
	}

	if !bearerTokenMatches(request, server.config.APIToken) {
		writeError(writer, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}

	switch {
	case request.URL.Path == "/healthz":
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		writeJSON(writer, http.StatusOK, healthResponse{Status: "ok"})
	case request.URL.Path == "/v1/messages":
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, http.MethodPost)
			return
		}
		server.submitMessage(writer, request)
	case strings.HasPrefix(request.URL.Path, "/v1/messages/"):
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		server.getMessage(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "not_found", "not found")
	}
}

type submitRequest struct {
	Text           string  `json:"text"`
	SendAt         *string `json:"sendAt"`
	IdempotencyKey string  `json:"idempotencyKey"`
}

type submitResponse struct {
	ID     string          `json:"id"`
	Status outbound.Status `json:"status"`
	SendAt string          `json:"sendAt"`
}

func (server *Server) submitMessage(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var input submitRequest
	if err := decoder.Decode(&input); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	if err := requireJSONEnd(decoder); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}

	if strings.TrimSpace(input.Text) == "" || utf8.RuneCountInString(input.Text) > maxTextRunes ||
		strings.TrimSpace(input.IdempotencyKey) == "" || utf8.RuneCountInString(input.IdempotencyKey) > maxIdempotencyKey {
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}

	var sendAt time.Time
	if input.SendAt != nil {
		sendAt, err = time.Parse(time.RFC3339, *input.SendAt)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
			return
		}
		now := server.now()
		if !sendAt.After(now) || sendAt.After(now.AddDate(1, 0, 0)) {
			writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
			return
		}
	}

	job, err := server.store.Create(request.Context(), outbound.CreateRequest{
		IdempotencyKey: input.IdempotencyKey,
		Text:           input.Text,
		SendAt:         sendAt,
	})
	if errors.Is(err, outbound.ErrIdempotencyConflict) {
		writeError(writer, http.StatusConflict, "idempotency_conflict", "idempotency key conflicts with an existing message")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	writeJSON(writer, http.StatusAccepted, submitResponse{
		ID:     strconv.FormatInt(job.ID, 10),
		Status: job.Status,
		SendAt: job.SendAt.Format(time.RFC3339),
	})
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

type messageStatusResponse struct {
	ID        string          `json:"id"`
	Status    outbound.Status `json:"status"`
	Attempts  int             `json:"attempts"`
	SendAt    string          `json:"sendAt"`
	SentAt    *string         `json:"sentAt"`
	LastError *string         `json:"lastError"`
}

func (server *Server) getMessage(writer http.ResponseWriter, request *http.Request) {
	idText := strings.TrimPrefix(request.URL.Path, "/v1/messages/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeError(writer, http.StatusNotFound, "not_found", "not found")
		return
	}

	job, err := server.store.Get(request.Context(), id)
	if errors.Is(err, outbound.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	var sentAt *string
	if job.SentAt != nil {
		formatted := job.SentAt.Format(time.RFC3339)
		sentAt = &formatted
	}
	writeJSON(writer, http.StatusOK, messageStatusResponse{
		ID:        strconv.FormatInt(job.ID, 10),
		Status:    job.Status,
		Attempts:  job.Attempts,
		SendAt:    job.SendAt.Format(time.RFC3339),
		SentAt:    sentAt,
		LastError: job.LastError,
	})
}

type healthResponse struct {
	Status string `json:"status"`
}

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func methodNotAllowed(writer http.ResponseWriter, allowedMethods ...string) {
	writer.Header().Set("Allow", strings.Join(allowedMethods, ", "))
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, errorResponse{
		Error: errorDetail{Code: code, Message: message},
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
