package messageapi

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"

	"whatsappBot/meetmonitor"
)

const maxMeetStateBodyBytes = 256 * 1024

func (server *Server) submitMeetState(writer http.ResponseWriter, request *http.Request) {
	if server.meet == nil {
		writeError(writer, http.StatusServiceUnavailable, "service_unavailable", "Meet monitor service unavailable")
		return
	}

	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxMeetStateBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var snapshot meetmonitor.Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		writeMeetStateDecodeError(writer, err)
		return
	}
	if err := requireJSONEnd(decoder); err != nil {
		writeMeetStateDecodeError(writer, err)
		return
	}

	result, err := server.meet.Process(request.Context(), snapshot)
	switch {
	case errors.Is(err, meetmonitor.ErrInvalidSnapshot):
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
	case errors.Is(err, meetmonitor.ErrSequenceConflict):
		writeError(writer, http.StatusConflict, "sequence_conflict", "snapshot conflicts with the accepted sequence")
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
	default:
		writeJSON(writer, http.StatusOK, result)
	}
}

func writeMeetStateDecodeError(writer http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		return
	}
	writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request")
}
