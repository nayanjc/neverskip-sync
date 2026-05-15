package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nayan/neverskip-sync/internal/neverskip"
)

func TestSplitLoungeTitle_SpacedSeparator(t *testing.T) {
	// Real-world variant: separator is "  : |  " (with whitespace),
	// not just ":|". Encountered in fixture items 0 and 1.
	in := "[ I - E ] Google form: Students' photo: |Jai Shri Gurudev! Body text - 02:36 PM | 09 May 2026"
	section, title, body, _ := splitLoungeTitle(in)
	if section != "I - E" {
		t.Errorf("section: %q", section)
	}
	if title != "Google form: Students' photo" {
		t.Errorf("title: got %q want %q", title, "Google form: Students' photo")
	}
	if !strings.HasPrefix(body, "Jai Shri Gurudev!") {
		t.Errorf("body: %q", body)
	}
}

func TestSplitLoungeTitle_FullForm(t *testing.T) {
	in := "[ I - E ] NEWSLETTER FOR THE MONTH OF MAY:|Jai Sri Gurudev!\r\nNamaste Dear Parents,\r\nPlease find the newsletter for the month of May.\r\nThank you,\r\nCoordinator - 09:05 AM | 05 May 2026"

	section, title, body, posted := splitLoungeTitle(in)
	if section != "I - E" {
		t.Errorf("section: got %q want %q", section, "I - E")
	}
	if title != "NEWSLETTER FOR THE MONTH OF MAY" {
		t.Errorf("title: got %q", title)
	}
	if !strings.Contains(body, "Jai Sri Gurudev!") || !strings.Contains(body, "Coordinator") {
		t.Errorf("body missing expected content: %q", body)
	}
	if strings.Contains(body, "\r") {
		t.Errorf("body still contains \\r: %q", body)
	}
	want := time.Date(2026, 5, 5, 9, 5, 0, 0, IST)
	if !posted.Equal(want) {
		t.Errorf("posted: got %v want %v", posted, want)
	}
}

func TestSplitLoungeTitle_NoSeparator(t *testing.T) {
	in := "[ I - E ] Dear Parents <br>Jai Shri Gurudev <br>Kindly find the allocation. <br>Regards <br>Coordinator - 09:43 AM | 16 Apr 2026"
	section, title, body, posted := splitLoungeTitle(in)
	if section != "I - E" {
		t.Errorf("section: got %q", section)
	}
	if title == "" {
		t.Errorf("expected fallback title from first sentence")
	}
	if !strings.Contains(body, "Dear Parents") {
		t.Errorf("body wrong: %q", body)
	}
	if strings.Contains(body, "<br>") {
		t.Errorf("<br> not unescaped: %q", body)
	}
	want := time.Date(2026, 4, 16, 9, 43, 0, 0, IST)
	if !posted.Equal(want) {
		t.Errorf("posted: got %v want %v", posted, want)
	}
}

func TestSplitLoungeTitle_MalformedFallback(t *testing.T) {
	in := "Some completely unparseable nonsense"
	section, title, body, posted := splitLoungeTitle(in)
	if section != "" {
		t.Errorf("expected empty section, got %q", section)
	}
	if title == "" && body == "" {
		t.Errorf("expected at least body to be set")
	}
	if !posted.IsZero() {
		t.Errorf("expected zero posted, got %v", posted)
	}
}

func TestParseLoungeFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "neverskip", "testdata", "lounge.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var r neverskip.LoungeResp
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(r.D.ItemList) == 0 {
		t.Fatal("fixture has no items")
	}
	parsedCount := 0
	for i, raw := range r.D.ItemList {
		it, ok := ParseLounge(raw)
		if !ok {
			t.Logf("item %d skipped (no msg_id)", i)
			continue
		}
		parsedCount++
		if it.MsgID == "" {
			t.Errorf("item %d: empty MsgID", i)
		}
		if it.Section != "I - E" {
			t.Errorf("item %d: section %q (expected I - E)", i, it.Section)
		}
		if it.PostedAt == nil {
			t.Errorf("item %d: PostedAt nil for fixture data — every fixture item ends with a parseable timestamp", i)
		}
	}
	if parsedCount == 0 {
		t.Fatal("expected at least one parsed lounge item")
	}
}

func TestParseDailyNoticeFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "neverskip", "testdata", "dailynotice.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var r neverskip.DailyNoticeResp
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(r.D.ItemList) == 0 {
		t.Fatal("fixture has no items")
	}
	for i, raw := range r.D.ItemList {
		it, ok := ParseDailyNotice(raw)
		if !ok {
			t.Errorf("item %d: parse failed", i)
			continue
		}
		if it.MsgID == "" {
			t.Errorf("item %d: empty MsgID", i)
		}
		if it.PostedAt == nil {
			t.Errorf("item %d: PostedAt nil — fixture has mtsp set", i)
		}
		if it.Body == "" {
			t.Errorf("item %d: empty body", i)
		}
		if strings.Contains(it.Body, "<br>") {
			t.Errorf("item %d: <br> not unescaped in body", i)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText("Jai Shri Gurudev!<br>Namaste Dear Parents,<br><br>Regards,<br>Coordinator")
	want := "Jai Shri Gurudev!\nNamaste Dear Parents,\n\nRegards,\nCoordinator"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}
