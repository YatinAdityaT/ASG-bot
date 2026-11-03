package meetmonitor

import (
	"context"
	"errors"
	"time"

	"whatsappBot/outbound"
)

var (
	ErrInvalidSnapshot  = errors.New("invalid Meet snapshot")
	ErrSequenceConflict = errors.New("Meet snapshot sequence conflict")
	ErrNotSeen          = errors.New("Meet monitor not seen")
)

type Participant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Cam  bool   `json:"cam"`
	Mic  string `json:"mic"`
	Self bool   `json:"self"`
	Host bool   `json:"host"`
}

type Snapshot struct {
	ProtocolVersion   int           `json:"protocolVersion"`
	Meeting           string        `json:"meeting"`
	SessionID         string        `json:"sessionId"`
	Sequence          int64         `json:"sequence"`
	CapturedAt        time.Time     `json:"capturedAt"`
	ExtensionVersion  string        `json:"extensionVersion"`
	ForwardingEnabled bool          `json:"forwardingEnabled"`
	Participants      []Participant `json:"participants"`
}

type Result struct {
	Status            string    `json:"status"`
	AcceptedSequence  int64     `json:"acceptedSequence"`
	ServerTime        time.Time `json:"serverTime"`
	MonitorOnline     bool      `json:"monitorOnline"`
	WhatsAppConnected bool      `json:"whatsAppConnected"`
	QueuedChanges     int       `json:"queuedChanges"`
}

type PersistedState struct {
	Meeting           string
	SessionID         string
	Sequence          int64
	Participants      []Participant
	SnapshotHash      string
	ExtensionVersion  string
	ForwardingEnabled bool
	LastHeartbeatAt   time.Time
	UpdatedAt         time.Time
}

type Status struct {
	BotOnline         bool
	WhatsAppConnected bool
	MonitorState      string
	LastHeartbeatAt   *time.Time
	Meeting           string
	Participants      int
	ForwardingEnabled bool
	ExtensionVersion  string
}

type StateStore interface {
	Load(context.Context, string) (PersistedState, error)
	Save(context.Context, PersistedState) error
}

type JobCreator interface {
	Create(context.Context, outbound.CreateRequest) (*outbound.Job, error)
}
