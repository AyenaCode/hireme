package jsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient points a Client at a test server with a fast, deterministic
// retry policy so backoff doesn't slow the suite.
func newTestClient(baseURL string) *Client {
	c := New("test-key")
	c.baseURL = baseURL
	c.baseDelay = time.Millisecond
	return c
}

const okBody = `{"status":"OK","data":{"jobs":[{"job_id":"abc","job_title":"DevOps"}],"cursor":"next"}}`

func TestSearch_RetriesThenSucceedsOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(okBody))
	}))
	defer srv.Close()

	jobs, cursor, err := newTestClient(srv.URL).Search(context.Background(), SearchParams{Query: "x"})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
	if len(jobs) != 1 || jobs[0].JobID != "abc" || cursor != "next" {
		t.Fatalf("unexpected result: jobs=%v cursor=%q", jobs, cursor)
	}
}

func TestSearch_DoesNotRetryOn4xx(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(code)
		}))

		_, _, err := newTestClient(srv.URL).Search(context.Background(), SearchParams{Query: "x"})
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: expected error", code)
		}
		if got := atomic.LoadInt32(&calls); got != 1 {
			t.Fatalf("status %d: expected exactly 1 attempt (no retry), got %d", code, got)
		}
	}
}

func TestSearch_RespectsRetryAfter(t *testing.T) {
	var calls int32
	var gap time.Duration
	var last time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		if n := atomic.AddInt32(&calls, 1); n == 1 {
			last = now
			w.Header().Set("Retry-After", "1") // 1s, well over our 1ms baseDelay
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		gap = now.Sub(last)
		w.Write([]byte(okBody))
	}))
	defer srv.Close()

	if _, _, err := newTestClient(srv.URL).Search(context.Background(), SearchParams{Query: "x"}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if gap < 900*time.Millisecond {
		t.Fatalf("expected to honour Retry-After ~1s, waited only %s", gap)
	}
}

func TestSearch_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL) // maxRetries defaults to 3
	_, _, err := c.Search(context.Background(), SearchParams{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "giving up") {
		t.Fatalf("expected a 'giving up' error, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != int32(c.maxRetries+1) {
		t.Fatalf("expected %d attempts, got %d", c.maxRetries+1, got)
	}
}

func TestSearchAll_FollowsCursorUpToMaxPages(t *testing.T) {
	// Three pages exist (cursors ""→"c1"→"c2"→last), but we cap at 2.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		switch r.URL.Query().Get("cursor") {
		case "":
			w.Write([]byte(`{"data":{"jobs":[{"job_id":"a"}],"cursor":"c1"}}`))
		case "c1":
			w.Write([]byte(`{"data":{"jobs":[{"job_id":"b"}],"cursor":"c2"}}`))
		default:
			w.Write([]byte(`{"data":{"jobs":[{"job_id":"c"}],"cursor":""}}`))
		}
	}))
	defer srv.Close()

	jobs, err := newTestClient(srv.URL).SearchAll(context.Background(), SearchParams{Query: "x"}, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 page requests (maxPages cap), got %d", got)
	}
	if len(jobs) != 2 || jobs[0].JobID != "a" || jobs[1].JobID != "b" {
		t.Fatalf("unexpected jobs: %+v", jobs)
	}
}

func TestSearchAll_StopsOnEmptyCursor(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte(`{"data":{"jobs":[{"job_id":"a"}],"cursor":""}}`)) // single page
	}))
	defer srv.Close()

	jobs, err := newTestClient(srv.URL).SearchAll(context.Background(), SearchParams{Query: "x"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 request (empty cursor stops early), got %d", got)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
}

func TestSearchAll_PartialFailureKeepsEarlierPages(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Query().Get("cursor") == "" {
			w.Write([]byte(`{"data":{"jobs":[{"job_id":"a"}],"cursor":"c1"}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest) // page 2 fails permanently
	}))
	defer srv.Close()

	jobs, err := newTestClient(srv.URL).SearchAll(context.Background(), SearchParams{Query: "x"}, 3)
	if err == nil {
		t.Fatal("expected an error from the failing second page")
	}
	if len(jobs) != 1 || jobs[0].JobID != "a" {
		t.Fatalf("expected page 1's job to survive the failure, got %+v", jobs)
	}
}

// Cancelling mid-backoff must interrupt the sleep, not wait it out. The server
// returns a retryable 503 so Search proceeds into the (long) backoff sleep,
// then we cancel during it.
func TestSearch_InterruptsBackoffOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	c.baseDelay = time.Hour // we'd block ~an hour if the sleep weren't interruptible
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, _, err := c.Search(ctx, SearchParams{Query: "x"})
		done <- err
	}()

	time.AfterFunc(50*time.Millisecond, cancel) // cancel while it's sleeping in backoff

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("backoff sleep was not interrupted promptly (took %s)", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after context cancellation")
	}
}
