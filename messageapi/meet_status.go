package messageapi

import (
	"net/http"
	"time"
)

type meetStatusResponse struct {
	BotOnline         bool       `json:"botOnline"`
	WhatsAppConnected bool       `json:"whatsAppConnected"`
	MonitorState      string     `json:"monitorState"`
	LastHeartbeatAt   *time.Time `json:"lastHeartbeatAt"`
	Meeting           string     `json:"meeting"`
	Participants      int        `json:"participants"`
	ForwardingEnabled bool       `json:"forwardingEnabled"`
	ExtensionVersion  string     `json:"extensionVersion"`
}

func (server *Server) getMeetStatus(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if server.meet == nil {
		writeError(
			writer,
			http.StatusServiceUnavailable,
			"meet_monitor_unavailable",
			"Meet monitor is unavailable",
		)
		return
	}
	status, err := server.meet.Status(request.Context())
	if err != nil {
		writeError(
			writer,
			http.StatusInternalServerError,
			"internal_error",
			"internal server error",
		)
		return
	}
	writeJSON(writer, http.StatusOK, meetStatusResponse{
		BotOnline:         status.BotOnline,
		WhatsAppConnected: status.WhatsAppConnected,
		MonitorState:      status.MonitorState,
		LastHeartbeatAt:   status.LastHeartbeatAt,
		Meeting:           status.Meeting,
		Participants:      status.Participants,
		ForwardingEnabled: status.ForwardingEnabled,
		ExtensionVersion:  status.ExtensionVersion,
	})
}
