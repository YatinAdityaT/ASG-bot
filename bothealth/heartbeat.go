package bothealth

import (
	"context"
	"fmt"
	"os"
	"time"

	"whatsappBot/outbound"
)

const (
	defaultLocalInterval = time.Minute
	defaultGroupInterval = time.Hour
)

type messageQueue interface {
	Create(context.Context, outbound.CreateRequest) (*outbound.Job, error)
}

type Runner struct {
	store         *Store
	queue         messageQueue
	now           func() time.Time
	localInterval time.Duration
	groupInterval time.Duration
}

type RunnerOption func(*Runner)

func WithClock(now func() time.Time) RunnerOption {
	return func(runner *Runner) {
		runner.now = now
	}
}

func WithIntervals(local, group time.Duration) RunnerOption {
	return func(runner *Runner) {
		runner.localInterval = local
		runner.groupInterval = group
	}
}

func NewRunner(store *Store, queue messageQueue, options ...RunnerOption) *Runner {
	runner := &Runner{
		store:         store,
		queue:         queue,
		now:           time.Now,
		localInterval: defaultLocalInterval,
		groupInterval: defaultGroupInterval,
	}
	for _, option := range options {
		option(runner)
	}
	return runner
}

func (r *Runner) Start(
	ctx context.Context,
	previous *PreviousRun,
	startedAt time.Time,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.queueMessage(ctx, startupKey(startedAt), StartupMessage(previous, startedAt))

		localTicker := time.NewTicker(r.localInterval)
		groupTicker := time.NewTicker(r.groupInterval)
		defer localTicker.Stop()
		defer groupTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-localTicker.C:
				if err := r.store.Heartbeat(ctx, r.now()); err != nil {
					fmt.Fprintf(os.Stderr, "Bot heartbeat persistence error: %v\n", err)
				}
			case <-groupTicker.C:
				now := r.now()
				r.queueMessage(
					ctx,
					fmt.Sprintf("bot-heartbeat-%d", now.UTC().Truncate(time.Hour).Unix()),
					HeartbeatMessage(startedAt, now),
				)
			}
		}
	}()
	return done
}

func (r *Runner) queueMessage(ctx context.Context, key, text string) {
	if r.queue == nil {
		return
	}
	if _, err := r.queue.Create(ctx, outbound.CreateRequest{
		IdempotencyKey: key,
		Text:           text,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Bot heartbeat queue error: %v\n", err)
	}
}

func StartupMessage(previous *PreviousRun, now time.Time) string {
	if previous == nil {
		return "🟢 Bot monitoring started at " + formatTime(now) + "."
	}
	if previous.StoppedGracefully {
		return "🔄 Bot restarted normally at " + formatTime(now) + "."
	}
	outage := now.Sub(previous.LastHeartbeatAt)
	if outage < 0 {
		outage = 0
	}
	return "⚠️ Bot recovered after an unexpected stop.\n" +
		"Last local heartbeat: " + formatTime(previous.LastHeartbeatAt) + "\n" +
		"Recovered: " + formatTime(now) + "\n" +
		"Estimated outage: " + formatDuration(outage)
}

func HeartbeatMessage(startedAt, now time.Time) string {
	uptime := now.Sub(startedAt)
	if uptime < 0 {
		uptime = 0
	}
	return "💚 Bot heartbeat: alive\n" +
		"Time: " + formatTime(now) + "\n" +
		"Uptime: " + formatDuration(uptime)
}

func startupKey(startedAt time.Time) string {
	return fmt.Sprintf("bot-start-%d", startedAt.UTC().UnixNano())
}

func formatTime(value time.Time) string {
	ist := time.FixedZone("IST", 5*60*60+30*60)
	return value.In(ist).Format("2 Jan 2006, 3:04 PM IST")
}

func formatDuration(value time.Duration) string {
	value = value.Round(time.Minute)
	if value < time.Minute {
		return "<1m"
	}
	hours := int(value / time.Hour)
	minutes := int(value%time.Hour) / int(time.Minute)
	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, minutes)
}
