package messageapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"whatsappBot/meetmonitor"
	"whatsappBot/outbound"
)

const (
	maxMeetNameRunes  = 200
	maxMeetFieldRunes = 255
	maxMeetChatRunes  = 3500
)

type meetEvent struct {
	Type    string  `json:"type"`
	ID      string  `json:"id"`
	Meeting string  `json:"meeting"`
	TS      string  `json:"ts"`
	Name    string  `json:"name"`
	Cam     *bool   `json:"cam"`
	Mic     *string `json:"mic"`
	Self    bool    `json:"self"`
	Host    bool    `json:"host"`
	Text    *string `json:"text"`
}

func (server *Server) submitMeetEvent(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var event meetEvent
	if err := decoder.Decode(&event); err != nil {
		writeMeetDecodeError(writer, err)
		return
	}
	if err := requireJSONEnd(decoder); err != nil {
		writeMeetDecodeError(writer, err)
		return
	}

	formatted, err := normalizeAndFormatMeetEvent(&event)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	if event.Type != "join" && event.Type != "leave" && event.Type != "chat_message" {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	key, err := meetIdempotencyKey(event)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	var jobs meetmonitor.JobCreator = server.store
	if event.Type == "join" || event.Type == "leave" {
		jobs = server.presenceJobs
	}
	job, err := jobs.Create(request.Context(), outbound.CreateRequest{
		IdempotencyKey: key,
		Text:           formatted,
	})
	if errors.Is(err, outbound.ErrIdempotencyConflict) {
		writeError(writer, http.StatusConflict, "idempotency_conflict", "event conflicts with an existing message")
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

func writeMeetDecodeError(writer http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		return
	}
	writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
}

func normalizeAndFormatMeetEvent(event *meetEvent) (string, error) {
	event.Type = strings.TrimSpace(event.Type)
	event.ID = strings.TrimSpace(event.ID)
	event.Meeting = strings.TrimSpace(event.Meeting)
	event.Name = strings.TrimSpace(event.Name)
	event.TS = strings.TrimSpace(event.TS)

	if event.ID == "" || event.Meeting == "" || event.Name == "" || event.TS == "" ||
		utf8.RuneCountInString(event.ID) > maxMeetFieldRunes ||
		utf8.RuneCountInString(event.Meeting) > maxMeetFieldRunes ||
		utf8.RuneCountInString(event.Name) > maxMeetNameRunes {
		return "", errors.New("invalid meet event")
	}
	event.Meeting = sanitizeMeetText(event.Meeting)
	event.Name = sanitizeMeetText(event.Name)
	if event.Meeting == "" || event.Name == "" {
		return "", errors.New("invalid meet event")
	}
	timestamp, err := time.Parse(time.RFC3339, event.TS)
	if err != nil {
		return "", errors.New("invalid meet event timestamp")
	}
	event.TS = timestamp.UTC().Format(time.RFC3339Nano)

	text := ""
	if event.Text != nil {
		text = *event.Text
	}
	if event.Type == "chat_message" {
		if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > maxMeetChatRunes {
			return "", errors.New("invalid chat text")
		}
		text = sanitizeMeetText(text)
		if text == "" {
			return "", errors.New("invalid chat text")
		}
		event.Text = &text
	}

	return meetmonitor.FormatEvent(event.Type, event.Name, text)
}

func meetIdempotencyKey(event meetEvent) (string, error) {
	identityTS := event.TS
	if event.Type == "chat_message" {
		identityTS = ""
	}
	identity := struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Meeting string `json:"meeting"`
		TS      string `json:"ts,omitempty"`
	}{
		Type:    event.Type,
		ID:      event.ID,
		Meeting: event.Meeting,
		TS:      identityTS,
	}
	normalized, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	return "meet-" + hex.EncodeToString(sum[:]), nil
}

func sanitizeMeetText(value string) string {
	value = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r):
			return ' '
		case unicode.In(r, unicode.Cf):
			return -1
		default:
			return r
		}
	}, value)
	return strings.Join(strings.Fields(value), " ")
}
