package scheduler

import (
	"context"
	"database/sql"
	"time"

	"whatsappBot/outbound"
)

type TaskProvider interface {
	GetDueTasks(ctx context.Context, now time.Time) ([]ScheduledTask, error)
}

type Registry struct {
	Providers []TaskProvider
}

type ReminderProvider struct {
	db *sql.DB
}

type BirthdayProvider struct {
	db *sql.DB
}

func NewReminderProvider(dbConn *sql.DB) TaskProvider {
	return &ReminderProvider{db: dbConn}
}

func NewBirthdayProvider(dbConn *sql.DB) TaskProvider {
	return &BirthdayProvider{db: dbConn}
}

func NewRegistry(dbConn *sql.DB, outboundStores ...*outbound.Store) *Registry {
	providers := []TaskProvider{
		NewReminderProvider(dbConn),
		NewBirthdayProvider(dbConn),
	}
	for _, store := range outboundStores {
		if store != nil {
			providers = append(providers, NewOutboundProvider(store))
		}
	}
	return &Registry{Providers: providers}
}

func (r *Registry) GetAllDueTasks(ctx context.Context, now time.Time) ([]ScheduledTask, error) {
	var all []ScheduledTask
	for _, provider := range r.Providers {
		tasks, err := provider.GetDueTasks(ctx, now)
		if err != nil {
			return nil, err
		}
		all = append(all, tasks...)
	}
	return all, nil
}
