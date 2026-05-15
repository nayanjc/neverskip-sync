// Package notifier sends ntfy.sh push notifications. The push is best-effort:
// if it fails, the item is already persisted in state and will appear in the
// ICS feed (Phase 2) — push is "nice to have first alert", not the source of
// truth.
package notifier

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nayan/neverskip-sync/internal/state"
)

type Ntfy struct {
	baseURL string
	topic   string
	http    *http.Client
}

func New(baseURL, topic string) *Ntfy {
	return &Ntfy{
		baseURL: strings.TrimRight(baseURL, "/"),
		topic:   topic,
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify posts a single item to ntfy. Headers carry the title and (if there
// is an attachment) a click URL that opens the PDF directly when the
// notification is tapped on iOS.
func (n *Ntfy) Notify(ctx context.Context, it state.Item) error {
	body := buildBody(it)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.baseURL+"/"+n.topic, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", headerSafe(buildTitle(it)))
	req.Header.Set("Tags", tagFor(it))
	if len(it.Attachments) > 0 {
		req.Header.Set("Click", it.Attachments[0])
	}
	req.Header.Set("Priority", "3")

	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy: status %d", resp.StatusCode)
	}
	return nil
}

// Plain pushes a free-text alert (for health/operator messages, not items).
func (n *Ntfy) Plain(ctx context.Context, title, body, priority string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.baseURL+"/"+n.topic, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", headerSafe(title))
	req.Header.Set("Tags", "warning")
	if priority != "" {
		req.Header.Set("Priority", priority)
	}
	resp, err := n.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy: status %d", resp.StatusCode)
	}
	return nil
}

func buildTitle(it state.Item) string {
	var b strings.Builder
	if it.Section != "" {
		b.WriteString("[")
		b.WriteString(it.Section)
		b.WriteString("] ")
	}
	t := it.CleanTitle
	if t == "" {
		t = "(no title)"
	}
	b.WriteString(t)
	return b.String()
}

func buildBody(it state.Item) string {
	body := it.Body
	if len(body) > 1500 {
		body = body[:1500] + "…"
	}
	if len(it.Attachments) > 1 {
		body += fmt.Sprintf("\n\n(%d attachments)", len(it.Attachments))
	}
	return body
}

func tagFor(it state.Item) string {
	switch it.Source {
	case "lounge":
		return "books"
	case "dailynotice":
		return "loudspeaker"
	default:
		return "school"
	}
}

// HTTP headers must be ASCII; ntfy's Title header in particular gets confused
// by newlines or CRLF. Strip aggressively.
func headerSafe(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	// collapse multiple spaces
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}
