// Package parser turns Neverskip's two response shapes into uniform
// state.Item values. Neverskip's payloads mash section + title + body +
// timestamp + HTML into a single string for lounge, and into a structured
// nested object for dailynotice — the parser is the seam where that
// inconsistency is hidden from the rest of the service.
//
// The parser never panics on malformed input: if the timestamp or title can't
// be extracted, the raw text becomes the title and PostedAt is left nil. A
// slightly ugly notification beats a missed one.
package parser

import (
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/nayan/neverskip-sync/internal/neverskip"
	"github.com/nayan/neverskip-sync/internal/state"
)

const (
	SourceLounge      = "lounge"
	SourceDailyNotice = "dailynotice"
)

var (
	// "[ I - E ] ..."
	sectionRe = regexp.MustCompile(`^\s*\[\s*([^\]]+?)\s*\]\s*`)

	// " - 09:05 AM | 05 May 2026"  (anchored to end of string)
	loungeTimestampRe = regexp.MustCompile(
		`(?i)\s*-\s*(\d{1,2}:\d{2}\s*(?:AM|PM))\s*\|\s*(\d{1,2}\s+[A-Za-z]{3}\s+\d{4})\s*$`)

	// "<br>" in any case
	brRe = regexp.MustCompile(`(?i)<br\s*/?>`)

	// Title/body separator: ":|" with optional whitespace around the pipe.
	// Real payloads use ":|", ": |", and ": | " interchangeably.
	titleBodyRe = regexp.MustCompile(`\s*:\s*\|\s*`)
)

// IST is where the school posts. Times in payloads have no zone; we treat
// them as Asia/Kolkata. Falls back to fixed +5:30 if tzdata is unavailable.
var IST = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return time.FixedZone("IST", int((5*time.Hour + 30*time.Minute) / time.Second))
	}
	return loc
}()

// ParseLounge maps a single lounge envelope to a state.Item. The dedup msg_id
// is taken from the first attachment in the envelope's nested item_list — that
// is the stable identifier Neverskip exposes per message.
//
// Returns ok=false if the envelope has no attachment at all (rare; usually
// indicates a text-only post that we can't dedup reliably).
func ParseLounge(it neverskip.LoungeItem) (state.Item, bool) {
	msgID := ""
	var atts []string
	for _, a := range it.Items {
		if msgID == "" && a.MsgID != "" {
			msgID = a.MsgID
		}
		if a.DownloadURL != "" {
			atts = append(atts, a.DownloadURL)
		}
	}
	if msgID == "" {
		return state.Item{}, false
	}

	section, cleanTitle, body, postedAt := splitLoungeTitle(it.Title)
	out := state.Item{
		Source:      SourceLounge,
		MsgID:       msgID,
		Section:     section,
		CleanTitle:  cleanTitle,
		Body:        body,
		Attachments: atts,
	}
	if !postedAt.IsZero() {
		out.PostedAt = &postedAt
	}
	return out, true
}

// ParseDailyNotice maps a dailynotice item to a state.Item. The dedup key is
// test_tar.msid; the timestamp comes from test_tar.mtsp ("YYYY-MM-DD HH:MM:SS"
// in IST). The HTML body lives at test_tar.mcom.
func ParseDailyNotice(it neverskip.DailyNoticeItem) (state.Item, bool) {
	t := it.TestTar
	if t.MsID == "" {
		return state.Item{}, false
	}
	body := htmlToText(t.MCom)
	if body == "" {
		body = strings.TrimSpace(it.Cont)
	}
	title := strings.TrimSpace(t.MTit)
	if title == "" {
		title = strings.TrimSpace(it.Title)
	}

	out := state.Item{
		Source:     SourceDailyNotice,
		MsgID:      t.MsID,
		CleanTitle: title,
		Body:       body,
	}
	if ts, ok := parseDailyNoticeTime(t.MTSP); ok {
		out.PostedAt = &ts
	}
	return out, true
}

// splitLoungeTitle picks apart the composite lounge title field. Format
// variants observed in the wild:
//
//	[ SECTION ] TITLE:|BODY - HH:MM AM/PM | DD MMM YYYY
//	[ SECTION ] BODY - HH:MM AM/PM | DD MMM YYYY     (no `:|`)
//
// Body may contain `<br>` tags and `\r\n` sequences and HTML entities.
func splitLoungeTitle(raw string) (section, title, body string, posted time.Time) {
	s := raw

	// section prefix
	if m := sectionRe.FindStringSubmatchIndex(s); m != nil {
		section = strings.TrimSpace(s[m[2]:m[3]])
		s = s[m[1]:]
	}

	// trailing timestamp
	if m := loungeTimestampRe.FindStringSubmatchIndex(s); m != nil {
		t := strings.TrimSpace(s[m[2]:m[3]]) + " " + strings.TrimSpace(s[m[4]:m[5]])
		if parsed, err := time.ParseInLocation("3:04 PM 2 Jan 2006", t, IST); err == nil {
			posted = parsed
		}
		s = s[:m[0]]
	}

	// title / body split — first occurrence of ":|" with any whitespace
	if loc := titleBodyRe.FindStringIndex(s); loc != nil {
		title = strings.TrimSpace(s[:loc[0]])
		body = htmlToText(s[loc[1]:])
	} else {
		body = htmlToText(s)
	}

	if title == "" {
		// fall back to first sentence of body so the notification has a
		// reasonable bold header
		title = firstSentence(body)
	}
	return section, title, body, posted
}

func parseDailyNoticeTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, IST)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func htmlToText(s string) string {
	s = brRe.ReplaceAllString(s, "\n")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = html.UnescapeString(s)
	// collapse runs of blank lines but keep paragraph breaks
	lines := strings.Split(s, "\n")
	var out []string
	prevBlank := false
	for _, ln := range lines {
		ln = strings.TrimRight(ln, " \t")
		if ln == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
		} else {
			prevBlank = false
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == '.' || r == '\n' || r == '!' || r == '?' {
			if i > 0 {
				return strings.TrimSpace(s[:i])
			}
		}
		if i >= 120 {
			return strings.TrimSpace(s[:i]) + "…"
		}
	}
	return s
}
