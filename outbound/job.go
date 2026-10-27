package outbound

import "time"

type Status string

const (
	StatusPending Status = "pending"
	StatusSending Status = "sending"
	StatusSent    Status = "sent"
	StatusFailed  Status = "failed"
)

type Job struct {
	ID             int64
	IdempotencyKey string
	ChatID         string
	Message        string
	MessageID      string
	SendAt         time.Time
	NextAttemptAt  time.Time
	Status         Status
	Attempts       int
	LastError      *string
	CreatedAt      time.Time
	SentAt         *time.Time
}

type CreateRequest struct {
	IdempotencyKey string
	Text           string
	SendAt         time.Time
}
