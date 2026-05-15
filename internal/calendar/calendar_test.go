package calendar

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nayan/neverskip-sync/internal/state"
)

func newTestHandler(t *testing.T, token string, items ...state.Item) (*Handler, *state.Store) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	for _, it := range items {
		if _, err := store.MarkSeen(ctx, it); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return New(store, token, "test.example", slog.Default()), store
}

func sampleItem(id string, when time.Time) state.Item {
	return state.Item{
		Source:      "lounge",
		MsgID:       id,
		Section:     "I - E",
		CleanTitle:  "Newsletter " + id,
		Body:        "Body for " + id,
		PostedAt:    &when,
		Attachments: []string{"https://example.com/" + id + ".pdf"},
	}
}

func TestHandler_DisabledWithoutToken(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/school/calendar.ics", nil))
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("status: got %d want 501", rr.Code)
	}
}

func TestHandler_RejectsMissingOrWrongToken(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t, "secret")
	for _, url := range []string{
		"/school/calendar.ics",
		"/school/calendar.ics?token=",
		"/school/calendar.ics?token=wrong",
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d want 401", url, rr.Code)
		}
	}
}

func TestHandler_ServesICSWithExpectedFields(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 5, 9, 9, 6, 0, 0, time.UTC)
	h, _ := newTestHandler(t, "secret", sampleItem("34489", when))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/school/calendar.ics?token=secret", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/calendar") {
		t.Errorf("content-type: %q", got)
	}
	if rr.Header().Get("ETag") == "" {
		t.Errorf("missing ETag")
	}
	body := rr.Body.String()
	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"END:VCALENDAR",
		"BEGIN:VEVENT",
		"END:VEVENT",
		"lounge-34489@test.example",
		"[I - E] Newsletter 34489",
		"https://example.com/34489.pdf",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody: %s", want, body)
		}
	}
}

func TestHandler_ConditionalGetReturns304(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 5, 9, 9, 6, 0, 0, time.UTC)
	h, _ := newTestHandler(t, "secret", sampleItem("34489", when))

	r1 := httptest.NewRecorder()
	h.ServeHTTP(r1, httptest.NewRequest(http.MethodGet, "/school/calendar.ics?token=secret", nil))
	etag := r1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag on first response")
	}

	req := httptest.NewRequest(http.MethodGet, "/school/calendar.ics?token=secret", nil)
	req.Header.Set("If-None-Match", etag)
	r2 := httptest.NewRecorder()
	h.ServeHTTP(r2, req)
	if r2.Code != http.StatusNotModified {
		t.Fatalf("status: got %d want 304", r2.Code)
	}
	if r2.Body.Len() != 0 {
		t.Errorf("304 must have empty body, got %d bytes", r2.Body.Len())
	}
}

func TestHandler_CachesRenderUnlessInvalidated(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 5, 9, 9, 6, 0, 0, time.UTC)
	h, store := newTestHandler(t, "secret", sampleItem("a", when))

	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, httptest.NewRequest(http.MethodGet, "/school/calendar.ics?token=secret", nil))
	body1 := rr1.Body.String()
	etag1 := rr1.Header().Get("ETag")

	// Add a new row without invalidating — cache should hide it.
	if _, err := store.MarkSeen(context.Background(), sampleItem("b", when.Add(time.Hour))); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/school/calendar.ics?token=secret", nil))
	if rr2.Body.String() != body1 || rr2.Header().Get("ETag") != etag1 {
		t.Errorf("cache miss before invalidation: body or etag changed")
	}

	// Invalidate and re-fetch — new row should now appear.
	h.Invalidate()
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, httptest.NewRequest(http.MethodGet, "/school/calendar.ics?token=secret", nil))
	if !strings.Contains(rr3.Body.String(), "lounge-b@test.example") {
		t.Errorf("invalidated render missing new event")
	}
	if rr3.Header().Get("ETag") == etag1 {
		t.Errorf("etag should change after content changes")
	}
}

func TestHandler_RealHTTPRoundTrip(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 5, 9, 9, 6, 0, 0, time.UTC)
	h, _ := newTestHandler(t, "secret", sampleItem("34489", when))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "?token=secret")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d, body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "BEGIN:VCALENDAR") {
		t.Errorf("not an ICS payload")
	}
}
