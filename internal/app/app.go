// Package app wires the pieces together and owns the polling loop. One process,
// one internal ticker (no OS cron). Each tick runs a full cycle; failures are
// logged and the loop keeps running so a transient API error never kills the
// service.
package app

import (
	"context"
	"fmt"
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
	SendText(ctx context.Context, text string) error
}

// searcher is the behaviour the app needs from a job source, returning the jobs
// and the number of API requests consumed against the monthly quota. Kept small
// so a second source can sit behind it later (RemoteOK, ATS endpoints).
type searcher interface {
	SearchAll(ctx context.Context, p jsearch.SearchParams, maxPages int) ([]jsearch.Job, int, error)
}

// compiledSearch pairs a ready-to-send query with the filter for its results.
type compiledSearch struct {
	params jsearch.SearchParams
	filter *filter.Filter
}

// App holds collaborators and config.
type App struct {
	cfg       *config.Config
	client    searcher
	store     *store.Store
	searches  []compiledSearch
	notify    notifier
	log       *slog.Logger
	heartbeat func() // optional liveness signal, fired at the end of every cycle
}

// New constructs an App from its dependencies, compiling one search (query +
// keyword filter) per configured entry; the freshness/remote/locale knobs are
// shared across them.
func New(cfg *config.Config, client searcher, st *store.Store, n notifier, log *slog.Logger) *App {
	searches := make([]compiledSearch, len(cfg.Searches))
	for i, s := range cfg.Searches {
		searches[i] = compiledSearch{
			params: jsearch.SearchParams{
				Query:      s.Query,
				RemoteOnly: cfg.RemoteOnly,
				DatePosted: cfg.DatePosted,
				Country:    cfg.Country,
				Language:   cfg.Language,
			},
			filter: filter.New(s.Keywords),
		}
	}
	return &App{cfg: cfg, client: client, store: st, searches: searches, notify: n, log: log}
}

// SetHeartbeat registers a callback fired at the end of every cycle. Used to
// drive the liveness endpoint; leaving it unset is fine (the app just won't
// signal). Set it before Run.
func (a *App) SetHeartbeat(beat func()) { a.heartbeat = beat }

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

// runCycle performs a single poll: for each configured search, fetch → filter →
// save new → notify → mark. A failed query is logged and the cycle moves to the
// next one. The monthly quota guard is checked before every query, so a cycle
// with several searches stops as soon as the budget is reached.
func (a *App) runCycle(ctx context.Context) error {
	start := time.Now()

	// Quota counter is keyed by calendar (UTC) month — an approximation of the
	// provider's reset, good enough to prevent runaway consumption.
	month := time.Now().UTC().Format("2006-01")
	budget := a.cfg.RequestBudget
	used, warned, qerr := a.store.Quota(ctx, month)
	if qerr != nil {
		// Fail open: a stuck counter must not silently halt alerting. Log loudly
		// and proceed this cycle without enforcing the budget.
		a.log.Error("quota read failed; skipping budget check this cycle", "err", qerr)
		used, warned = 0, true
	}

	var fetched, matched, pushed int
	for _, s := range a.searches {
		if ctx.Err() != nil {
			break // graceful shutdown: stop before starting another query
		}
		if budget > 0 && used >= budget {
			warned = a.warnQuotaOnce(ctx, month, used, budget, warned)
			a.log.Warn("monthly request budget reached; skipping remaining queries",
				"used", used, "budget", budget, "query", s.params.Query)
			break
		}

		jobs, requests, err := a.client.SearchAll(ctx, s.params, a.cfg.MaxPages)
		// Record usage BEFORE branching on err: those requests cost quota whether
		// the query succeeded or failed (retry-heavy queries are the costly ones).
		// Use a detached context so a shutdown mid-cycle still persists what we
		// spent instead of erroring on the cancelled request context.
		if requests > 0 {
			recCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if newUsed, aerr := a.store.AddRequests(recCtx, month, requests); aerr != nil {
				a.log.Error("record request usage failed", "err", aerr)
			} else {
				used = newUsed
			}
			cancel()
		}
		// Warn as soon as usage reaches budget, so the heads-up lands now.
		warned = a.warnQuotaOnce(ctx, month, used, budget, warned)

		if err != nil {
			// Shutdown cancelled the request mid-flight: that's expected, not a
			// failure — stop the cycle quietly rather than logging an error.
			if ctx.Err() != nil {
				a.log.Info("cycle interrupted by shutdown", "query", s.params.Query)
				break
			}
			// A partial failure still hands back the pages we did fetch; use them
			// rather than dropping real matches. With nothing, log and move on to
			// the next query instead of aborting the whole cycle.
			if len(jobs) == 0 {
				a.log.Error("query failed", "query", s.params.Query, "err", err)
				continue
			}
			a.log.Warn("partial fetch; proceeding with pages retrieved",
				"query", s.params.Query, "err", err, "fetched", len(jobs))
		}

		fetched += len(jobs)
		m, p := a.processJobs(ctx, jobs, s.filter)
		matched += m
		pushed += p
	}

	// Liveness: signal unconditionally — the loop turned, even if every query
	// failed. The probe only cares that we're not wedged, not that fetches
	// succeeded (a restart can't fix an upstream outage).
	if a.heartbeat != nil {
		a.heartbeat()
	}

	a.log.Info("cycle complete",
		"queries", len(a.searches), "fetched", fetched, "matched", matched, "new_pushed", pushed,
		"used", used, "budget", budget,
		"took", time.Since(start).Round(time.Millisecond))
	return nil
}

// processJobs filters a page of jobs and pushes genuinely new matches, returning
// how many matched and how many were pushed. Dedup across queries is free: a job
// matched by two searches hits SaveNew twice and the second insert is a no-op.
func (a *App) processJobs(ctx context.Context, jobs []jsearch.Job, f *filter.Filter) (matched, pushed int) {
	for _, j := range jobs {
		if j.JobID == "" {
			continue
		}
		ok, kw := f.Match(j)
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
			continue // already seen on a previous cycle (or an earlier query)
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
			return matched, pushed
		case <-time.After(time.Second):
		}
	}
	return matched, pushed
}

// warnQuotaOnce pushes a single over-budget warning per month. It is a no-op
// when the guard is off, we're under budget, or the warning was already sent.
// It returns the (possibly updated) warned flag. A failed push is not marked,
// so it is retried next cycle.
func (a *App) warnQuotaOnce(ctx context.Context, month string, used, budget int, warned bool) bool {
	if budget <= 0 || used < budget || warned {
		return warned
	}
	msg := fmt.Sprintf("⚠️ JSearch monthly request budget reached: %d/%d used. "+
		"Pausing API calls until the next month's reset.", used, budget)
	if err := a.notify.SendText(ctx, msg); err != nil {
		a.log.Error("quota warning push failed", "err", err)
		return false
	}
	if err := a.store.MarkQuotaWarned(ctx, month); err != nil {
		a.log.Error("mark quota warned failed", "err", err)
	}
	return true
}
