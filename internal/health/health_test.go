package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthy_FreshVsStale(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	r := New(time.Hour, now)

	if ok, _ := r.Healthy(now.Add(59 * time.Minute)); !ok {
		t.Fatal("within the staleness window should be healthy")
	}
	if ok, age := r.Healthy(now.Add(2 * time.Hour)); ok || age != 2*time.Hour {
		t.Fatalf("past the window should be unhealthy; ok=%t age=%s", ok, age)
	}
}

func TestHandler_StatusCodes(t *testing.T) {
	// Fresh: last cycle is "now", so the handler (which uses real time) sees a
	// tiny age and returns 200.
	fresh := New(time.Hour, time.Now())
	if rec := serve(fresh); rec.Code != http.StatusOK {
		t.Fatalf("fresh: got %d, want 200", rec.Code)
	}

	// Stale: last cycle is well beyond the window → 503.
	stale := New(time.Minute, time.Now().Add(-time.Hour))
	if rec := serve(stale); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale: got %d, want 503", rec.Code)
	}
}

func TestBeat_RefreshesLiveness(t *testing.T) {
	r := New(time.Minute, time.Now().Add(-time.Hour)) // starts stale
	if ok, _ := r.Healthy(time.Now()); ok {
		t.Fatal("precondition: should start stale")
	}
	r.Beat()
	if ok, _ := r.Healthy(time.Now()); !ok {
		t.Fatal("Beat should restore liveness")
	}
}

func serve(r *Reporter) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.Handler()(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	return rec
}
