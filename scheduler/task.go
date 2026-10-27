package scheduler

import (
	"context"
	"time"

	"whatsappBot/utils"
)

type ScheduledTask interface {
	Run(ctx context.Context, sender utils.MessageSender) error
	Time() time.Time
	ID() string // unique task identifier (for de-duping if needed)
}
