package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"hireme/internal/config"
	"hireme/internal/jsearch"
	"hireme/internal/store"
)

var errFake = errors.New("fake search failure")

// mockNotifier records what was pushed without hitting the network.
type mockNotifier struct {
	jobs  []jsearch.Job
	texts []string
}

func (m *mockNotifier) SendJob(_ context.Context, j jsearch.Job) error {
	m.jobs = append(m.jobs, j)
	return nil
}
func (m *mockNotifier) SendText(_ context.Context, t string) error {
	m.texts = append(m.texts, t)
	return nil
}

// fakeSearcher returns canned jobs and a request count without any network. If
// byQuery is set it returns per-query results; otherwise the flat jobs slice.
type fakeSearcher struct {
	jobs     []jsearch.Job
	byQuery  map[string][]jsearch.Job
	requests int
	err      error
	calls    int
	queries  []string // queries seen, in call order
}

func (f *fakeSearcher) SearchAll(_ context.Context, p jsearch.SearchParams, _ int) ([]jsearch.Job, int, error) {
	f.calls++
	f.queries = append(f.queries, p.Query)
	if f.byQuery != nil {
		return f.byQuery[p.Query], f.requests, f.err
	}
	return f.jobs, f.requests, f.err
}

func oneSearch(query string, keywords ...string) []config.Search {
	return []config.Search{{Query: query, Keywords: keywords}}
}

// When the month is already at/over budget, a cycle must make no API request
// (so the counter does not move past budget) and must push exactly one warning,
// even across repeated cycles.
func TestRunCycle_SkipsAndWarnsWhenBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const budget = 200
	month := time.Now().UTC().Format("2006-01")
	if _, err := st.AddRequests(ctx, month, budget); err != nil { // start exactly at budget
		t.Fatalf("seed quota: %v", err)
	}

	cfg := &config.Config{Searches: oneSearch("devops", "devops"), MaxPages: 1, RequestBudget: budget}
	src := &fakeSearcher{jobs: []jsearch.Job{{JobID: "x", Title: "DevOps"}}, requests: 1}
	mock := &mockNotifier{}
	a := New(cfg, src, st, mock, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for i := 0; i < 2; i++ {
		if err := a.runCycle(ctx); err != nil {
			t.Fatalf("cycle %d returned error: %v", i, err)
		}
	}

	if src.calls != 0 {
		t.Fatalf("guard should make zero API calls when over budget, made %d", src.calls)
	}
	used, warned, err := st.Quota(ctx, month)
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if used != budget {
		t.Fatalf("counter moved past budget (%d); a request was made despite the guard", used)
	}
	if !warned {
		t.Fatal("expected the month to be marked warned")
	}
	if len(mock.texts) != 1 {
		t.Fatalf("expected exactly one warning push, got %d", len(mock.texts))
	}
}

// An under-budget cycle records its requests; the cycle whose usage reaches the
// budget pushes exactly one warning, and the next cycle is then skipped.
func TestRunCycle_CountsUsageAndWarnsOnCrossing(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const budget = 200
	month := time.Now().UTC().Format("2006-01")
	if _, err := st.AddRequests(ctx, month, budget-2); err != nil { // two requests below budget
		t.Fatalf("seed quota: %v", err)
	}

	cfg := &config.Config{Searches: oneSearch("devops", "devops"), MaxPages: 2, RequestBudget: budget}
	src := &fakeSearcher{jobs: []jsearch.Job{{JobID: "x", Title: "DevOps"}}, requests: 2} // crosses to exactly budget
	mock := &mockNotifier{}
	a := New(cfg, src, st, mock, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Cycle 1: under budget at the top → fetches, records 2, crosses to budget, warns.
	if err := a.runCycle(ctx); err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	if used, _, _ := st.Quota(ctx, month); used != budget {
		t.Fatalf("after cycle 1 used=%d, want %d", used, budget)
	}
	if src.calls != 1 || len(mock.jobs) != 1 || len(mock.texts) != 1 {
		t.Fatalf("cycle 1: calls=%d jobs=%d warnings=%d", src.calls, len(mock.jobs), len(mock.texts))
	}

	// Cycle 2: now at budget → skipped, no extra request, no second warning.
	if err := a.runCycle(ctx); err != nil {
		t.Fatalf("cycle 2: %v", err)
	}
	if src.calls != 1 {
		t.Fatalf("cycle 2 should not call the API, total calls=%d", src.calls)
	}
	if len(mock.texts) != 1 {
		t.Fatalf("warning must fire once, got %d", len(mock.texts))
	}
}

// The heartbeat fires once per cycle even when every query fails — liveness
// reflects that the loop turned, not that fetches succeeded.
func TestRunCycle_HeartbeatFiresEvenOnFailure(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{Searches: oneSearch("devops", "devops"), MaxPages: 1}
	src := &fakeSearcher{requests: 1, err: errFake} // every query fails
	a := New(cfg, src, st, &mockNotifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var beats int
	a.SetHeartbeat(func() { beats++ })

	if err := a.runCycle(ctx); err != nil {
		t.Fatalf("runCycle: %v", err)
	}
	if beats != 1 {
		t.Fatalf("expected 1 heartbeat despite query failure, got %d", beats)
	}
}

// Each query is filtered by its own keyword set: a DevOps-titled job returned
// for the React-Native query must be dropped, and both queries run in a cycle.
func TestRunCycle_PerQueryKeywordFiltering(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		Searches: []config.Search{
			{Query: "devops remote", Keywords: []string{"devops"}},
			{Query: "react native remote", Keywords: []string{"react native"}},
		},
		MaxPages:      1,
		RequestBudget: 0, // guard off
	}
	src := &fakeSearcher{
		requests: 1,
		byQuery: map[string][]jsearch.Job{
			"devops remote":       {{JobID: "a", Title: "Senior DevOps Engineer"}},
			"react native remote": {{JobID: "b", Title: "React Native Developer"}, {JobID: "c", Title: "DevOps SRE"}},
		},
	}
	mock := &mockNotifier{}
	a := New(cfg, src, st, mock, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := a.runCycle(ctx); err != nil {
		t.Fatalf("runCycle: %v", err)
	}
	if len(src.queries) != 2 {
		t.Fatalf("expected both queries run, got %v", src.queries)
	}
	// "a" (devops query) and "b" (RN query) push; "c" is a DevOps job returned
	// for the RN query, so the RN keyword filter must drop it.
	var ids []string
	for _, j := range mock.jobs {
		ids = append(ids, j.JobID)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("expected pushes [a b] (c filtered out by RN keywords), got %v", ids)
	}
}

// With several queries, the budget is checked before each: once a query pushes
// usage to the budget, the remaining queries are skipped within the same cycle.
func TestRunCycle_BudgetStopsRemainingQueries(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const budget = 200
	month := time.Now().UTC().Format("2006-01")
	if _, err := st.AddRequests(ctx, month, budget-1); err != nil { // one below budget
		t.Fatalf("seed quota: %v", err)
	}

	cfg := &config.Config{
		Searches: []config.Search{
			{Query: "q1", Keywords: []string{"devops"}},
			{Query: "q2", Keywords: []string{"devops"}},
		},
		MaxPages:      1,
		RequestBudget: budget,
	}
	src := &fakeSearcher{jobs: []jsearch.Job{{JobID: "x", Title: "DevOps"}}, requests: 1}
	mock := &mockNotifier{}
	a := New(cfg, src, st, mock, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := a.runCycle(ctx); err != nil {
		t.Fatalf("runCycle: %v", err)
	}
	if src.calls != 1 {
		t.Fatalf("expected only the first query to run before budget hit, got %d calls (%v)", src.calls, src.queries)
	}
	if used, _, _ := st.Quota(ctx, month); used != budget {
		t.Fatalf("used=%d, want %d", used, budget)
	}
	if len(mock.texts) != 1 {
		t.Fatalf("expected one budget warning, got %d", len(mock.texts))
	}
}
