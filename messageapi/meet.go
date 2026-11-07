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
	"unicode"
	"unicode/utf8"

	"whatsappBot/meetmonitor"
	"whatsappBot/meetreminder"
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
	text := ""
	if event.Text != nil {
		text = *event.Text
	}
	if isMeetReminderCommand(text) {
		if server.meet == nil || server.reminders == nil {
			writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "Meet reminders unavailable")
			return
		}
		if err := server.processMeetReminder(request, event, key, text); err != nil {
			writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
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

func isMeetReminderCommand(text string) bool {
	fields := strings.Fields(text)
	return len(fields) > 0 && strings.EqualFold(fields[0], "/remind")
}

func (server *Server) processMeetReminder(
	request *http.Request,
	event meetEvent,
	sourceKey string,
	text string,
) error {
	result, err := server.reminders.CreateFromMeet(request.Context(), meetreminder.CommandRequest{
		SourceKey:      "meet-reminder-" + strings.TrimPrefix(sourceKey, "meet-"),
		MeetingCode:    event.Meeting,
		SenderMeetName: event.Name,
		Text:           text,
		WhatsAppChatID: server.config.AccountabilityGroup,
	})

	var acknowledgement string
	switch {
	case errors.Is(err, meetreminder.ErrUnknownSender):
		acknowledgement = "I couldn't identify your Meet name. Ask an admin to add your Meet mapping."
	case errors.Is(err, meetreminder.ErrUnknownTarget):
		acknowledgement = "I couldn't find that username. Ask an admin to add its identity mapping."
	case errors.Is(err, meetreminder.ErrInvalidCommand):
		acknowledgement = `I couldn't understand that reminder. Try /remind disha to join in 30 minutes.`
	case errors.Is(err, meetreminder.ErrServiceDisabled):
		acknowledgement = "Meet reminders are not configured."
	case err != nil:
		return err
	default:
		location := server.now().Location()
		acknowledgement = fmt.Sprintf(
			"Reminder set for @%s at %s: %q",
			result.Reminder.Target.Username,
			result.Reminder.RemindAt.In(location).Format("3:04 PM on Jan 2"),
			result.Reminder.Body,
		)
	}

	_, queueErr := server.meet.QueueMessageWithID(
		request.Context(),
		"meet-reminder-ack-"+strings.TrimPrefix(sourceKey, "meet-"),
		acknowledgement,
	)
	return queueErr
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
