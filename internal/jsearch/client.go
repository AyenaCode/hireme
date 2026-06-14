// Package jsearch is a thin client for the OpenWebNinja JSearch API
// (search-v2 endpoint). It maps the wire response into the domain Job model.
//
// Field names are verified against the official OpenAPI spec. The search-v2
// response wraps jobs in data.jobs (an object, not an array). Enrichment fields
// such as seniority, required experience/technologies and work arrangement are
// NOT returned by search-v2 — they live only on the /job-details endpoint — so
// they are intentionally absent here. Matching is done on title + description.
package jsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultBaseURL = "https://api.openwebninja.com/jsearch"

// Retry defaults. Search-v2 GETs are idempotent, so retrying is safe. We retry
// transport errors and 5xx aggressively; a 429 (rate limit or monthly-quota
// exhaustion) is retried too, but only by honouring a sane Retry-After — see
// maxBackoff. baseDelay grows exponentially per attempt with jitter.
const (
	defaultMaxRetries = 3
	defaultBaseDelay  = 500 * time.Millisecond
	// maxBackoff caps any single wait, including a server-sent Retry-After. The
	// free tier's 429 can mean "come back next month"; we must never block the
	// whole process honouring a multi-minute/hour Retry-After — give up instead
	// and let the next poll cycle try again.
	maxBackoff = 60 * time.Second
)

// Job is the domain model. JSON tags map directly from the JSearch response so
// the same struct is used for decoding and for the rest of the app.
type Job struct {
	// identity (from JSearch) — job_id is stable and used as the primary key
	JobID        string `json:"job_id"`
	Title        string `json:"job_title"`
	Employer     string `json:"employer_name"`
	EmployerSite string `json:"employer_website"`
	Publisher    string `json:"job_publisher"`

	// where / how
	Location   string `json:"job_location"`
	City       string `json:"job_city"`
	Country    string `json:"job_country"`
	Remote     bool   `json:"job_is_remote"`
	EmployType string `json:"job_employment_type"`

	// links
	ApplyLink  string `json:"job_apply_link"`
	GoogleLink string `json:"job_google_link"`

	// content for filtering
	Description string `json:"job_description"`

	// salary (often null)
	MinSalary    *float64 `json:"job_min_salary"`
	MaxSalary    *float64 `json:"job_max_salary"`
	SalaryPeriod string   `json:"job_salary_period"`

	// timing (from JSearch)
	PostedAtUnix int64     `json:"job_posted_at_timestamp"`
	PostedAtUTC  time.Time `json:"job_posted_at_datetime_utc"`

	// local fields (not from JSearch)
	SeenAt   time.Time `json:"-"`
	Notified bool      `json:"-"`
}

// response is the API envelope. Jobs are nested under data.jobs, and the
// pagination cursor under data.cursor: { "status": ..., "data": { "jobs": [...], "cursor": ... } }.
type response struct {
	Status    string `json:"status"`
	RequestID string `json:"request_id"`
	Data      struct {
		Jobs   []Job  `json:"jobs"`
		Cursor string `json:"cursor"`
	} `json:"data"`
}

// SearchParams describes one search-v2 query.
type SearchParams struct {
	Query      string
	RemoteOnly bool
	DatePosted string // all|today|3days|week|month
	Country    string
	Language   string
	Cursor     string // pagination cursor returned by a previous call
}

// Client talks to the JSearch API. It is safe for concurrent use.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client

	maxRetries int           // retries after the first attempt (0 = single attempt)
	baseDelay  time.Duration // first backoff; doubles per attempt, capped at maxBackoff
}

// New returns a Client with a sane default timeout and retry policy.
func New(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		http:       &http.Client{Timeout: 30 * time.Second},
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
	}
}

// Search calls search-v2 and returns the page of jobs plus the cursor for the
// next page (empty when there are no more pages). Transient failures (transport
// errors, 5xx, 429) are retried with exponential backoff up to maxRetries;
// permanent failures (4xx other than 429) and context cancellation return at
// once.
func (c *Client) Search(ctx context.Context, p SearchParams) (jobs []Job, nextCursor string, err error) {
	q := url.Values{}
	q.Set("query", p.Query)
	if p.RemoteOnly {
		q.Set("work_from_home", "true")
	}
	if p.DatePosted != "" {
		q.Set("date_posted", p.DatePosted)
	}
	if p.Country != "" {
		q.Set("country", p.Country)
	}
	if p.Language != "" {
		q.Set("language", p.Language)
	}
	if p.Cursor != "" {
		q.Set("cursor", p.Cursor)
	}
	endpoint := c.baseURL + "/search-v2?" + q.Encode()

	var lastErr error
	var retryAfter time.Duration // server-sent hint from the previous attempt
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			wait := backoff(attempt, c.baseDelay) // always <= maxBackoff
			if retryAfter > 0 {                   // a server hint overrides our schedule
				if retryAfter > maxBackoff {
					// "Much later" (e.g. monthly quota): don't block the process —
					// bail and let the next poll cycle retry.
					return nil, "", fmt.Errorf("jsearch asked to wait %s (> %s cap): %w", retryAfter, maxBackoff, lastErr)
				}
				wait = retryAfter
			}
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(wait):
			}
		}

		var retryable bool
		jobs, nextCursor, retryAfter, retryable, err = c.doSearch(ctx, endpoint)
		if err == nil {
			return jobs, nextCursor, nil
		}
		lastErr = err
		// Never spend a retry on a cancelled/expired context.
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if !retryable {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("jsearch: giving up after %d retries: %w", c.maxRetries, lastErr)
}

// SearchAll fetches up to maxPages pages by following the search-v2 cursor,
// concatenating the jobs. It stops early when the cursor is empty (last page),
// a page returns no jobs, or maxPages is reached. maxPages <= 1 means first
// page only (the quota-safe default).
//
// On a page failure it returns the jobs gathered from earlier pages together
// with the error, never discarding results already paid for in API quota — the
// store dedups, so re-fetching those pages next cycle costs no extra alerts.
func (c *Client) SearchAll(ctx context.Context, p SearchParams, maxPages int) (jobs []Job, err error) {
	if maxPages < 1 {
		maxPages = 1
	}
	for page := 0; page < maxPages; page++ {
		batch, next, serr := c.Search(ctx, p)
		jobs = append(jobs, batch...)
		if serr != nil {
			return jobs, fmt.Errorf("page %d: %w", page+1, serr)
		}
		if next == "" || len(batch) == 0 {
			break // last page or empty page: nothing more to follow
		}
		p.Cursor = next
	}
	return jobs, nil
}

// doSearch performs a single attempt. retryAfter is the server-sent Retry-After
// (0 if absent/unparseable); retryable says whether the caller should back off
// and try again.
func (c *Client) doSearch(ctx context.Context, endpoint string) (jobs []Job, nextCursor string, retryAfter time.Duration, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", 0, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// Transport errors are transient unless the context was cancelled.
		return nil, "", 0, !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded), fmt.Errorf("call jsearch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // cap at 8 MiB
	if err != nil {
		return nil, "", 0, true, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// 429 and 5xx are transient; other 4xx (bad key, bad query) are not.
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		ra := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, "", ra, retry, fmt.Errorf("jsearch returned %s: %s", resp.Status, truncate(string(body), 300))
	}

	var out response
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", 0, false, fmt.Errorf("decode response: %w", err)
	}
	return out.Data.Jobs, out.Data.Cursor, 0, false, nil
}

// backoff returns the exponential delay for a given retry attempt (1-indexed),
// capped at maxBackoff, with up to 25% jitter to avoid synchronised retries.
func backoff(attempt int, base time.Duration) time.Duration {
	d := base << (attempt - 1) // base * 2^(attempt-1)
	if d <= 0 || d > maxBackoff {
		d = maxBackoff
	}
	d += time.Duration(rand.Int63n(int64(d)/4 + 1)) // up to 25% jitter
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// parseRetryAfter reads a Retry-After header in delta-seconds form (the form
// gateways use for 429s). HTTP-date form and garbage both yield 0 (= ignore).
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
