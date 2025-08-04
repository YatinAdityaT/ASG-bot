package scheduler

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
)

type Scheduler struct {
	registry *Registry
	client   *whatsmeow.Client
	interval time.Duration
}

func NewScheduler(reg *Registry, client *whatsmeow.Client, interval time.Duration) *Scheduler {
	return &Scheduler{
		registry: reg,
		client:   client,
		interval: interval,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.interval):
				now := time.Now()
				tasks, err := s.registry.GetAllDueTasks(ctx, now)
				if err != nil {
					fmt.Println("Error getting due tasks:", err)
					continue
				}
				for _, task := range tasks {
					if err := task.Run(ctx, s.client); err != nil {
						fmt.Printf("Error running task %s: %v\n", task.ID(), err)
					}
				}
			}
		}
	}()
}
