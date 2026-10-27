package messageapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	key, err := meetIdempotencyKey(event)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	job, err := server.store.Create(request.Context(), outbound.CreateRequest{
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
	timestamp, err := time.Parse(time.RFC3339, event.TS)
	if err != nil {
		return "", errors.New("invalid meet event timestamp")
	}
	event.TS = timestamp.UTC().Format(time.RFC3339Nano)

	text := ""
	if event.Text != nil {
		text = *event.Text
	}
	prefix := fmt.Sprintf("Meet %s: ", event.Meeting)
	switch event.Type {
	case "join":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected event text")
		}
		return "🎥 " + prefix + event.Name + " joined the call.", nil
	case "leave":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected event text")
		}
		return "🎥 " + prefix + event.Name + " left the call.", nil
	case "mic_muted":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected event text")
		}
		return "🎙️ " + prefix + event.Name + " muted their mic.", nil
	case "mic_unmuted":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected event text")
		}
		return "🎙️ " + prefix + event.Name + " unmuted their mic.", nil
	case "cam_on":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected event text")
		}
		return "📷 " + prefix + event.Name + " turned their camera on.", nil
	case "cam_off":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected event text")
		}
		return "📷 " + prefix + event.Name + " turned their camera off.", nil
	case "rename":
		if strings.TrimSpace(text) != "" {
			return "", errors.New("unexpected event text")
		}
		return "✏️ " + prefix + "A participant is now named " + event.Name + ".", nil
	case "chat_message":
		if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > maxMeetChatRunes {
			return "", errors.New("invalid chat text")
		}
		return "💬 " + prefix + event.Name + ` said: "` + text + `"`, nil
	default:
		return "", errors.New("unsupported event type")
	}
}

func meetIdempotencyKey(event meetEvent) (string, error) {
	normalized, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	return "meet-" + hex.EncodeToString(sum[:]), nil
}
