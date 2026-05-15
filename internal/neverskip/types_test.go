package neverskip

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoungeFixtureDecodes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "lounge.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var r LoungeResp
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !r.S {
		t.Fatal("expected S=true")
	}
	if len(r.D.ItemList) == 0 {
		t.Fatal("expected at least one lounge item")
	}
	for i, it := range r.D.ItemList {
		if it.Title == "" {
			t.Errorf("item %d: empty title", i)
		}
	}
}

func TestDailyNoticeFixtureDecodes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "dailynotice.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var r DailyNoticeResp
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !r.S {
		t.Fatal("expected S=true")
	}
	if len(r.D.ItemList) == 0 {
		t.Fatal("expected at least one dailynotice item")
	}
	for i, it := range r.D.ItemList {
		if it.TestTar.MsID == "" {
			t.Errorf("item %d: empty test_tar.msid", i)
		}
		if it.TestTar.MTSP == "" {
			t.Errorf("item %d: empty test_tar.mtsp", i)
		}
	}
}
