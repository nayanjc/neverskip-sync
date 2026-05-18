package dashboard

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
	return New(store, token, slog.New(slog.NewTextHandler(io.Discard, nil))), store
}

func sampleItem(source, id, section, title string, when time.Time) state.Item {
	return state.Item{
		Source:     source,
		MsgID:      id,
		Section:    section,
		CleanTitle: title,
		Body:       "Body for " + id,
		PostedAt:   &when,
	}
}

func TestDisabledWithoutToken(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/school/dashboard", nil))
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("status: got %d want 501", rr.Code)
	}
}

func TestRejectsMissingOrWrongToken(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t, "secret")
	for _, url := range []string{
		"/school/dashboard",
		"/school/dashboard?token=",
		"/school/dashboard?token=wrong",
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d want 401", url, rr.Code)
		}
	}
}

func TestRendersExpectedRows(t *testing.T) {
	t.Parallel()
	recent := time.Now().Add(-2 * 24 * time.Hour)
	h, _ := newTestHandler(t, "secret",
		sampleItem("lounge", "L1", "I - E", "Newsletter for May", recent),
		sampleItem("dailynotice", "DN1", "", "Vendor visit on Monday", recent.Add(1*time.Hour)),
	)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/school/dashboard?token=secret", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "<!doctype html>") {
		t.Errorf("body should start with doctype, got: %.40s", body)
	}
	for _, want := range []string{
		"Newsletter for May",
		"Vendor visit on Monday",
		"src-lounge",
		"src-dailynotice",
		"I - E",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSourceFilter(t *testing.T) {
	t.Parallel()
	recent := time.Now().Add(-12 * time.Hour)
	h, _ := newTestHandler(t, "secret",
		sampleItem("lounge", "L1", "I - E", "Lounge item", recent),
		sampleItem("dailynotice", "DN1", "", "Notice item", recent.Add(time.Hour)),
	)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/school/dashboard?token=secret&source=lounge", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "Lounge item") {
		t.Errorf("expected Lounge item in filtered output")
	}
	if strings.Contains(body, "Notice item") {
		t.Errorf("source=lounge should hide dailynotice rows")
	}
}

func TestDaysWindow(t *testing.T) {
	t.Parallel()
	old := time.Now().Add(-60 * 24 * time.Hour)
	recent := time.Now().Add(-1 * 24 * time.Hour)
	h, _ := newTestHandler(t, "secret",
		sampleItem("lounge", "old", "I - E", "Old item", old),
		sampleItem("lounge", "new", "I - E", "Fresh item", recent),
	)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/school/dashboard?token=secret&days=7", nil))
	body := rr.Body.String()
	if strings.Contains(body, "Old item") {
		t.Errorf("days=7 should exclude 60-day-old item")
	}
	if !strings.Contains(body, "Fresh item") {
		t.Errorf("days=7 should include 1-day-old item")
	}
}

func TestBodyPreviewTrimsAndEscapes(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("ab ", 200) // 600 chars, well past the 220 cap
	recent := time.Now().Add(-time.Hour)
	h, _ := newTestHandler(t, "secret", state.Item{
		Source:     "lounge",
		MsgID:      "x",
		CleanTitle: "<script>alert(1)</script>",
		Body:       long,
		PostedAt:   &recent,
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/school/dashboard?token=secret", nil))
	body := rr.Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Errorf("title not HTML-escaped — XSS risk: %q", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped title in output")
	}
	if !strings.Contains(body, "…") {
		t.Errorf("expected ellipsis from body truncation")
	}
}
