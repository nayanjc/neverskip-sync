package notifier

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nayan/neverskip-sync/internal/state"
)

func TestNotifyPostsExpectedHeadersAndBody(t *testing.T) {
	var got struct {
		method string
		path   string
		title  string
		click  string
		tags   string
		prio   string
		body   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.title = r.Header.Get("Title")
		got.click = r.Header.Get("Click")
		got.tags = r.Header.Get("Tags")
		got.prio = r.Header.Get("Priority")
		b, _ := io.ReadAll(r.Body)
		got.body = string(b)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	n := New(srv.URL, "secret-topic")
	err := n.Notify(context.Background(), state.Item{
		Source:      "lounge",
		Section:     "I - E",
		CleanTitle:  "Newsletter for May",
		Body:        "Body text\nwith two lines",
		Attachments: []string{"https://example.com/file.pdf"},
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got.method != http.MethodPost {
		t.Errorf("method: %q", got.method)
	}
	if got.path != "/secret-topic" {
		t.Errorf("path: %q", got.path)
	}
	if got.title != "[I - E] Newsletter for May" {
		t.Errorf("title: %q", got.title)
	}
	if got.click != "https://example.com/file.pdf" {
		t.Errorf("click: %q", got.click)
	}
	if got.tags != "books" {
		t.Errorf("tags: %q", got.tags)
	}
	if got.prio != "3" {
		t.Errorf("priority: %q", got.prio)
	}
	if !strings.Contains(got.body, "Body text") {
		t.Errorf("body: %q", got.body)
	}
}

func TestNotifyHeaderSanitisesNewlines(t *testing.T) {
	var titleHdr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		titleHdr = r.Header.Get("Title")
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	n := New(srv.URL, "t")
	err := n.Notify(context.Background(), state.Item{
		Source:     "dailynotice",
		CleanTitle: "Line one\nLine two\rLine three",
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if strings.ContainsAny(titleHdr, "\r\n") {
		t.Errorf("title still contains CR/LF: %q", titleHdr)
	}
}
