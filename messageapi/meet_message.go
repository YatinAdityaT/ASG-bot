package messageapi

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
)

const maxMeetCommandAckBodyBytes = 4 * 1024

func (server *Server) getMeetCommand(writer http.ResponseWriter, request *http.Request) {
	if server.meet == nil {
		writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "Meet monitor service unavailable")
		return
	}
	message, err := server.meet.PendingMessage(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if message == nil {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(writer, http.StatusOK, message)
}

func (server *Server) acknowledgeMeetCommand(writer http.ResponseWriter, request *http.Request) {
	if server.meet == nil {
		writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "Meet monitor service unavailable")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxMeetCommandAckBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		ID string `json:"id"`
	}
	if err := decoder.Decode(&input); err != nil {
		writeMeetCommandDecodeError(writer, err)
		return
	}
	if err := requireJSONEnd(decoder); err != nil {
		writeMeetCommandDecodeError(writer, err)
		return
	}
	if strings.TrimSpace(input.ID) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}

	acknowledged, err := server.meet.AcknowledgeMessage(request.Context(), input.ID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !acknowledged {
		writeError(writer, http.StatusNotFound, "not_found", "command not found")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "acknowledged"})
}

func writeMeetCommandDecodeError(writer http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		return
	}
	writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
}
