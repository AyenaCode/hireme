// Package jsearch is a thin client for the OpenWebNinja JSearch API
// (search-v2 endpoint). It maps the wire response into the domain Job model.
//
// Note: the field mapping follows the spec in projetct.md. A few enrichment
// fields (seniority, required experience/technologies) are best-effort and may
// be absent or shaped differently on the live API; the MVP never depends on
// them — matching is done on title + description. See roadmap.md.
package jsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const defaultBaseURL = "https://api.openwebninja.com/jsearch"

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
	Location    string `json:"job_location"`
	City        string `json:"job_city"`
	Country     string `json:"job_country"`
	Remote      bool   `json:"job_is_remote"`
	Arrangement string `json:"work_arrangement"`
	EmployType  string `json:"job_employment_type"`

	// links
	ApplyLink  string `json:"job_apply_link"`
	GoogleLink string `json:"job_google_link"`

	// content for filtering / scoring
	Description  string   `json:"job_description"`
	Seniority    string   `json:"seniority_level"`
	ExpYears     int      `json:"required_experience_years"`
	Technologies []string `json:"required_technologies"`
	Function     string   `json:"job_function"`

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

// response is the API envelope: { "status": ..., "data": [...], "cursor": ... }.
type response struct {
	Status string `json:"status"`
	Data   []Job  `json:"data"`
	Cursor string `json:"cursor"`
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
}

// New returns a Client with a sane default timeout.
func New(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Search calls search-v2 once and returns the page of jobs plus the cursor for
// the next page (empty when there are no more pages).
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("call jsearch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // cap at 8 MiB
	if err != nil {
		return nil, "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("jsearch returned %s: %s", resp.Status, truncate(string(body), 300))
	}

	var out response
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}
	return out.Data, out.Cursor, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
