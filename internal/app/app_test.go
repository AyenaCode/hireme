package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"hireme/internal/config"
	"hireme/internal/filter"
	"hireme/internal/jsearch"
	"hireme/internal/store"
)

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

// fakeSearcher returns canned jobs and a request count without any network.
type fakeSearcher struct {
	jobs     []jsearch.Job
	requests int
	err      error
	calls    int
}

func (f *fakeSearcher) SearchAll(_ context.Context, _ jsearch.SearchParams, _ int) ([]jsearch.Job, int, error) {
	f.calls++
	return f.jobs, f.requests, f.err
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

	cfg := &config.Config{
		Query:         "devops",
		Keywords:      []string{"devops"},
		MaxPages:      1,
		RequestBudget: budget,
	}
	src := &fakeSearcher{jobs: []jsearch.Job{{JobID: "x", Title: "DevOps"}}, requests: 1}
	mock := &mockNotifier{}
	a := New(cfg, src, st, filter.New(cfg.Keywords),
		mock, slog.New(slog.NewTextHandler(io.Discard, nil)))

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

	cfg := &config.Config{Query: "devops", Keywords: []string{"devops"}, MaxPages: 2, RequestBudget: budget}
	src := &fakeSearcher{jobs: []jsearch.Job{{JobID: "x", Title: "DevOps"}}, requests: 2} // crosses to exactly budget
	mock := &mockNotifier{}
	a := New(cfg, src, st, filter.New(cfg.Keywords),
		mock, slog.New(slog.NewTextHandler(io.Discard, nil)))

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
