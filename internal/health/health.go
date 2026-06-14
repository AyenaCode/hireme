// Package health exposes process liveness over HTTP. Liveness here means only
// "the polling loop is still turning" — it deliberately ignores upstream
// failures (JSearch outage, quota exhaustion, DB read errors), because a
// liveness probe triggers restarts and restarting the process cannot fix those.
// Beat is called at the end of every cycle, including cycles where every query
// failed; the endpoint goes red only when no cycle has completed within the
// staleness window (i.e. the loop is wedged), which a restart would fix.
package health

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Reporter tracks the time of the last completed cycle and answers liveness
// checks. It is safe for concurrent use.
type Reporter struct {
	mu    sync.RWMutex
	last  time.Time
	stale time.Duration
}

// New returns a Reporter that considers the process healthy until a cycle is
// more than stale old. start seeds the last-cycle time so the process is healthy
// during the window before the first cycle completes.
func New(stale time.Duration, start time.Time) *Reporter {
	return &Reporter{last: start, stale: stale}
}

// Beat records that a cycle just completed.
func (r *Reporter) Beat() {
	r.mu.Lock()
	r.last = time.Now()
	r.mu.Unlock()
}

// Healthy reports whether the last cycle is within the staleness window as of
// now, and the age of that last cycle.
func (r *Reporter) Healthy(now time.Time) (healthy bool, age time.Duration) {
	r.mu.RLock()
	last := r.last
	r.mu.RUnlock()
	age = now.Sub(last)
	return age <= r.stale, age
}

// Handler serves the liveness check: 200 when fresh, 503 when stale, with a
// small JSON body for humans. It reads in-memory state only — no I/O.
func (r *Reporter) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		healthy, age := r.Healthy(time.Now())
		code := http.StatusOK
		if !healthy {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"healthy":                healthy,
			"last_cycle_age_seconds": int(age.Seconds()),
			"stale_after_seconds":    int(r.stale.Seconds()),
		})
	}
}
