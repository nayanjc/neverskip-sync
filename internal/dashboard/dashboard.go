// Package dashboard serves a small HTML view of recently-seen items, gated
// by the same query-param token as the ICS feed (reusing ICS_TOKEN keeps the
// secret inventory at one entry).
//
// Server-rendered, no client-side framework. Query params control the
// view: `days` (default 30), `sort` (posted_at | source | section, default
// posted_at), `dir` (asc | desc, default desc), `source` filter.
package dashboard

import (
	"crypto/subtle"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nayan/neverskip-sync/internal/parser"
	"github.com/nayan/neverskip-sync/internal/state"
)

type Handler struct {
	store *state.Store
	token string
	log   *slog.Logger
	tmpl  *template.Template
}

func New(store *state.Store, token string, log *slog.Logger) *Handler {
	return &Handler{
		store: store,
		token: token,
		log:   log,
		tmpl:  template.Must(template.New("dash").Funcs(funcMap()).Parse(pageHTML)),
	}
}

type viewItem struct {
	Source         string
	Section        string
	CleanTitle     string
	BodyPreview    string
	PostedDisplay  string
	PostedISO      string
	AttachmentURLs []string
}

type viewData struct {
	Title       string
	Days        int
	Sort        string
	Dir         string
	Source      string
	GeneratedAt string
	Items       []viewItem
	Total       int
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.token == "" {
		http.Error(w, "dashboard not enabled — set ICS_TOKEN", http.StatusNotImplemented)
		return
	}
	got := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	days := clamp(intParam(r, "days", 30), 1, 365)
	sortKey := strings.ToLower(r.URL.Query().Get("sort"))
	if sortKey == "" {
		sortKey = "posted_at"
	}
	dir := strings.ToLower(r.URL.Query().Get("dir"))
	if dir == "" {
		dir = "desc"
	}
	sourceFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))

	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	rows, err := h.store.ItemsSince(r.Context(), since)
	if err != nil {
		h.log.Error("dashboard query failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if sourceFilter != "" {
		filtered := rows[:0]
		for _, it := range rows {
			if it.Source == sourceFilter {
				filtered = append(filtered, it)
			}
		}
		rows = filtered
	}

	sortItems(rows, sortKey, dir)

	view := viewData{
		Title:       "Neverskip — last " + strconv.Itoa(days) + " days",
		Days:        days,
		Sort:        sortKey,
		Dir:         dir,
		Source:      sourceFilter,
		GeneratedAt: time.Now().In(parser.IST).Format("Mon, 02 Jan 2006 15:04 IST"),
		Total:       len(rows),
		Items:       make([]viewItem, 0, len(rows)),
	}
	for _, it := range rows {
		vi := viewItem{
			Source:         it.Source,
			Section:        it.Section,
			CleanTitle:     it.CleanTitle,
			BodyPreview:    bodyPreview(it.Body),
			AttachmentURLs: it.Attachments,
		}
		if it.PostedAt != nil {
			vi.PostedDisplay = it.PostedAt.In(parser.IST).Format("02 Jan 15:04")
			vi.PostedISO = it.PostedAt.UTC().Format(time.RFC3339)
		}
		view.Items = append(view.Items, vi)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=60")
	if err := h.tmpl.Execute(w, view); err != nil {
		h.log.Error("dashboard render failed", "err", err)
	}
}

func sortItems(rows []state.Item, key, dir string) {
	less := func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch key {
		case "source":
			if a.Source != b.Source {
				return a.Source < b.Source
			}
		case "section":
			if a.Section != b.Section {
				return a.Section < b.Section
			}
		}
		// fall through to posted_at as the secondary / default key
		var ta, tb time.Time
		if a.PostedAt != nil {
			ta = *a.PostedAt
		}
		if b.PostedAt != nil {
			tb = *b.PostedAt
		}
		return ta.Before(tb)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if dir == "asc" {
			return less(i, j)
		}
		return less(j, i)
	})
}

func bodyPreview(body string) string {
	body = strings.TrimSpace(body)
	body = strings.ReplaceAll(body, "\n", " ")
	for strings.Contains(body, "  ") {
		body = strings.ReplaceAll(body, "  ", " ")
	}
	const max = 220
	if len(body) > max {
		return body[:max] + "…"
	}
	return body
}

func intParam(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"hasAttach": func(items []viewItem) bool {
			for _, it := range items {
				if len(it.AttachmentURLs) > 0 {
					return true
				}
			}
			return false
		},
	}
}

// Template is intentionally compact and styled inline so the page is one
// self-contained response. Mobile-friendly because the user is likely to
// hit this from their phone.
const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
:root { color-scheme: light dark; }
body { font: 14px/1.45 system-ui, -apple-system, sans-serif; max-width: 1100px; margin: 1.5rem auto; padding: 0 1rem; }
h1 { font-size: 1.2rem; margin: 0 0 0.3rem 0; }
.meta { color: #888; font-size: 0.85rem; margin-bottom: 1rem; }
form { margin: 0 0 1rem 0; display: flex; gap: 0.5rem; flex-wrap: wrap; align-items: end; }
form label { display: flex; flex-direction: column; font-size: 0.75rem; color: #888; }
form input, form select { padding: 0.25rem 0.4rem; font: inherit; }
table { width: 100%; border-collapse: collapse; }
th, td { padding: 0.5rem 0.6rem; vertical-align: top; border-bottom: 1px solid #2222; text-align: left; }
th { white-space: nowrap; font-weight: 600; }
th a { color: inherit; text-decoration: none; }
th a:hover { text-decoration: underline; }
tr:hover td { background: #0001; }
.src { display: inline-block; padding: 0.05rem 0.4rem; border-radius: 0.25rem; font-size: 0.75rem; }
.src-lounge { background: #d4eaff; color: #03478a; }
.src-dailynotice { background: #ffe4d4; color: #7c3300; }
.title { font-weight: 600; }
.body { color: #666; font-size: 0.85rem; margin-top: 0.15rem; }
.atts { font-size: 0.75rem; margin-top: 0.25rem; }
.atts a { margin-right: 0.5rem; }
.posted { white-space: nowrap; color: #666; font-size: 0.85rem; }
.empty { color: #888; padding: 1rem; text-align: center; }
@media (prefers-color-scheme: dark) {
  body { background: #111; color: #ddd; }
  .src-lounge { background: #0d3a66; color: #cbe4ff; }
  .src-dailynotice { background: #5a2400; color: #ffdcbc; }
}
</style>
</head>
<body>
<h1>{{.Title}}</h1>
<div class="meta">{{.Total}} items · generated {{.GeneratedAt}}</div>

<form method="get">
  <input type="hidden" name="token" value="">
  <label>Days
    <input type="number" name="days" min="1" max="365" value="{{.Days}}">
  </label>
  <label>Source
    <select name="source">
      <option value="" {{if eq .Source ""}}selected{{end}}>all</option>
      <option value="lounge" {{if eq .Source "lounge"}}selected{{end}}>lounge</option>
      <option value="dailynotice" {{if eq .Source "dailynotice"}}selected{{end}}>dailynotice</option>
    </select>
  </label>
  <label>Sort
    <select name="sort">
      <option value="posted_at" {{if eq .Sort "posted_at"}}selected{{end}}>posted_at</option>
      <option value="source" {{if eq .Sort "source"}}selected{{end}}>source</option>
      <option value="section" {{if eq .Sort "section"}}selected{{end}}>section</option>
    </select>
  </label>
  <label>Dir
    <select name="dir">
      <option value="desc" {{if eq .Dir "desc"}}selected{{end}}>desc</option>
      <option value="asc"  {{if eq .Dir "asc" }}selected{{end}}>asc</option>
    </select>
  </label>
  <button type="submit">Apply</button>
</form>

{{if .Items}}
<table>
<thead>
<tr>
  <th>When</th>
  <th>Source</th>
  <th>Section</th>
  <th>Title &amp; body</th>
</tr>
</thead>
<tbody>
{{range .Items}}
<tr>
  <td class="posted" title="{{.PostedISO}}">{{.PostedDisplay}}</td>
  <td><span class="src src-{{.Source}}">{{.Source}}</span></td>
  <td>{{.Section}}</td>
  <td>
    <div class="title">{{.CleanTitle}}</div>
    {{if .BodyPreview}}<div class="body">{{.BodyPreview}}</div>{{end}}
    {{if .AttachmentURLs}}
      <div class="atts">
        {{range .AttachmentURLs}}<a href="{{.}}" target="_blank" rel="noopener">attachment</a>{{end}}
      </div>
    {{end}}
  </td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p class="empty">No items in this range.</p>
{{end}}

<script>
// The form needs the same token in the URL after submit. Pull it from
// the current URL once on load and stash it into the hidden field.
(function() {
  var t = new URLSearchParams(window.location.search).get('token');
  if (t) document.querySelector('input[name=token]').value = t;
})();
</script>
</body>
</html>
`
