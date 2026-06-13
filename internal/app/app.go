// Package app wires the pieces together and owns the polling loop. One process,
// one internal ticker (no OS cron). Each tick runs a full cycle; failures are
// logged and the loop keeps running so a transient API error never kills the
// service.
package app

import (
	"context"
	"log/slog"
	"time"

	"hireme/internal/config"
	"hireme/internal/filter"
	"hireme/internal/jsearch"
	"hireme/internal/store"
)

// notifier is the behaviour the app needs from a push channel (kept small so it
// can be swapped/mocked — e.g. add Expo push later).
type notifier interface {
	SendJob(ctx context.Context, j jsearch.Job) error
}

// App holds collaborators and config.
type App struct {
	cfg    *config.Config
	client *jsearch.Client
	store  *store.Store
	filter *filter.Filter
	notify notifier
	log    *slog.Logger
}

// New constructs an App from its dependencies.
func New(cfg *config.Config, client *jsearch.Client, st *store.Store, f *filter.Filter, n notifier, log *slog.Logger) *App {
	return &App{cfg: cfg, client: client, store: st, filter: f, notify: n, log: log}
}

// Run executes one cycle immediately, then (unless RunOnce) ticks every
// PollInterval until the context is cancelled.
func (a *App) Run(ctx context.Context) error {
	a.log.Info("starting", "config", a.cfg.Redacted())

	// Run once up front so a fresh start (or a restart) checks immediately
	// instead of waiting a full interval.
	if err := a.runCycle(ctx); err != nil {
		a.log.Error("cycle failed", "err", err)
	}
	if a.cfg.RunOnce {
		return nil
	}

	ticker := time.NewTicker(a.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.log.Info("shutting down", "reason", ctx.Err())
			return nil
		case <-ticker.C:
			if err := a.runCycle(ctx); err != nil {
				a.log.Error("cycle failed", "err", err)
			}
		}
	}
}

// runCycle performs a single poll: fetch → filter → save new → notify → mark.
func (a *App) runCycle(ctx context.Context) error {
	start := time.Now()
	jobs, _, err := a.client.Search(ctx, jsearch.SearchParams{
		Query:      a.cfg.Query,
		RemoteOnly: a.cfg.RemoteOnly,
		DatePosted: a.cfg.DatePosted,
		Country:    a.cfg.Country,
		Language:   a.cfg.Language,
	})
	if err != nil {
		return err
	}

	var matched, pushed int
	for _, j := range jobs {
		if j.JobID == "" {
			continue
		}
		ok, kw := a.filter.Match(j)
		if !ok {
			continue
		}
		matched++

		isNew, err := a.store.SaveNew(ctx, j)
		if err != nil {
			a.log.Error("save failed", "job_id", j.JobID, "err", err)
			continue
		}
		if !isNew {
			continue // already seen on a previous cycle
		}

		if err := a.notify.SendJob(ctx, j); err != nil {
			// Leave notified=0 so it retries on the next cycle.
			a.log.Error("notify failed", "job_id", j.JobID, "title", j.Title, "err", err)
			continue
		}
		if err := a.store.MarkNotified(ctx, j.JobID); err != nil {
			a.log.Error("mark notified failed", "job_id", j.JobID, "err", err)
		}
		pushed++
		a.log.Info("new job pushed", "title", j.Title, "employer", j.Employer, "matched_keyword", kw)

		// Gentle pace under Telegram's per-chat rate limit (~1 msg/sec).
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}

	a.log.Info("cycle complete",
		"fetched", len(jobs), "matched", matched, "new_pushed", pushed,
		"took", time.Since(start).Round(time.Millisecond))
	return nil
}
