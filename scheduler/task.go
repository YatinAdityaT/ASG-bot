package scheduler

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow"
)

type ScheduledTask interface {
	Run(ctx context.Context, client *whatsmeow.Client) error
	Time() time.Time
	ID() string // unique task identifier (for de-duping if needed)
}
