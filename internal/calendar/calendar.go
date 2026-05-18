// Package calendar serves an ICS (RFC 5545) feed of seen items. iOS Calendar,
// macOS Calendar, and Google Calendar can all subscribe to the URL and pull
// updates automatically.
//
// Auth is a single query-param token, compared with subtle.ConstantTimeCompare.
// The URL is the secret — anyone with it can read the calendar.
//
// Each item becomes one VEVENT. The DTSTART is the parsed event_time if set,
// else posted_at. Phase 1 doesn't extract event_time from prose, so most
// events currently use posted_at — the calendar reads as a chronological diary
// of school communication. Phase 3 will add deadline extraction so dated
// notices show on the right day.
package calendar

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ics "github.com/arran4/golang-ical"

	"github.com/nayan/neverskip-sync/internal/state"
)

const (
	defaultEventDuration = 1 * time.Hour
	cacheTTL             = 60 * time.Second
	maxItemsServed       = 500
)

// Handler renders the calendar feed. It is safe for concurrent use.
type Handler struct {
	store    *state.Store
	token    string
	host     string
	log      *slog.Logger

	mu        sync.RWMutex
	cached    []byte
	cachedAt  time.Time
	cachedTag string
	// invalidated is a generation counter bumped by Invalidate(); reading it
	// is lock-free so the poll loop can call Invalidate() without contending
	// with concurrent calendar reads.
	generation atomic.Uint64
	servedGen  uint64
}

// New constructs a Handler.
//
// token: required query-param secret. If empty, all requests get 501.
// host:  domain used in the UID suffix to keep VEVENT IDs globally unique
//        across hosts (e.g. the public domain you serve the feed from).
func New(store *state.Store, token, host string, log *slog.Logger) *Handler {
	return &Handler{
		store: store,
		token: token,
		host:  host,
		log:   log,
	}
}

// Invalidate marks the cached calendar as stale so the next request
// re-renders. The poll loop calls this when it inserts new items.
func (h *Handler) Invalidate() {
	h.generation.Add(1)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.token == "" {
		http.Error(w, "calendar feed not enabled — set ICS_TOKEN", http.StatusNotImplemented)
		return
	}
	got := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, etag, err := h.renderCached(r.Context())
	if err != nil {
		h.log.Error("render calendar", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=60")

	if match := r.Header.Get("If-None-Match"); match != "" && stripWeak(match) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Handler) renderCached(ctx context.Context) ([]byte, string, error) {
	curGen := h.generation.Load()

	h.mu.RLock()
	if h.cached != nil && curGen == h.servedGen && time.Since(h.cachedAt) < cacheTTL {
		body, etag := h.cached, h.cachedTag
		h.mu.RUnlock()
		return body, etag, nil
	}
	h.mu.RUnlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	// double-check after acquiring write lock
	if h.cached != nil && curGen == h.servedGen && time.Since(h.cachedAt) < cacheTTL {
		return h.cached, h.cachedTag, nil
	}

	items, err := h.store.CalendarItems(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(items) > maxItemsServed {
		items = items[len(items)-maxItemsServed:]
	}
	body, etag := render(items, h.host)
	h.cached = body
	h.cachedAt = time.Now()
	h.cachedTag = etag
	h.servedGen = curGen
	return body, etag, nil
}

func render(items []state.Item, host string) ([]byte, string) {
	cal := ics.NewCalendar()
	cal.SetMethod(ics.MethodPublish)
	cal.SetProductId("-//neverskip-sync//Phase 2//EN")
	cal.SetName("Neverskip")
	cal.SetXWRCalName("Neverskip")
	cal.SetTimezoneId("Asia/Kolkata")

	for _, it := range items {
		start := eventStart(it)
		if start.IsZero() {
			continue
		}
		uid := fmt.Sprintf("%s-%s@%s", it.Source, it.MsgID, host)
		ev := cal.AddEvent(uid)
		ev.SetCreatedTime(time.Now().UTC())
		ev.SetModifiedAt(time.Now().UTC())
		ev.SetStartAt(start)
		ev.SetEndAt(start.Add(defaultEventDuration))
		ev.SetSummary(buildSummary(it))
		ev.SetDescription(buildDescription(it))
		if len(it.Attachments) > 0 {
			ev.SetURL(it.Attachments[0])
		}
	}

	body := []byte(cal.Serialize())
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	return body, etag
}

func eventStart(it state.Item) time.Time {
	if it.EventTime != nil && !it.EventTime.IsZero() {
		return *it.EventTime
	}
	if it.PostedAt != nil && !it.PostedAt.IsZero() {
		return *it.PostedAt
	}
	return time.Time{}
}

func buildSummary(it state.Item) string {
	var b strings.Builder
	if it.Section != "" {
		b.WriteString("[")
		b.WriteString(it.Section)
		b.WriteString("] ")
	}
	title := it.CleanTitle
	if title == "" {
		title = "(no title)"
	}
	b.WriteString(title)
	return b.String()
}

func buildDescription(it state.Item) string {
	var b strings.Builder
	if it.Body != "" {
		b.WriteString(it.Body)
	}
	if len(it.Attachments) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Attachments:\n")
		for _, a := range it.Attachments {
			b.WriteString(a)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func stripWeak(etag string) string {
	return strings.TrimPrefix(etag, "W/")
}
