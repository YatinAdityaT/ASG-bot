package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"whatsappBot/utils"
)

type Scheduler struct {
	registry *Registry
	sender   utils.MessageSender
	interval time.Duration
}

func NewScheduler(reg *Registry, sender utils.MessageSender, interval time.Duration) *Scheduler {
	return &Scheduler{
		registry: reg,
		sender:   sender,
		interval: interval,
	}
}

func (s *Scheduler) RunOnce(ctx context.Context, now time.Time) error {
	tasks, err := s.registry.GetAllDueTasks(ctx, now)
	if err != nil {
		return fmt.Errorf("get due tasks: %w", err)
	}

	var taskErrors []error
	for _, task := range tasks {
		if err := task.Run(ctx, s.sender); err != nil {
			taskErrors = append(taskErrors, fmt.Errorf("run task %s: %w", task.ID(), err))
		}
	}
	return errors.Join(taskErrors...)
}

func (s *Scheduler) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := s.RunOnce(ctx, now); err != nil {
					fmt.Printf("Error running scheduler: %v\n", err)
				}
			}
		}
	}()
	return done
}
