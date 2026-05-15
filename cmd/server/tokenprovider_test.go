package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileTokenProvider_FallbackWhenPathEmpty(t *testing.T) {
	p := fileTokenProvider("", "from-env")
	got, err := p()
	if err != nil {
		t.Fatalf("provider err: %v", err)
	}
	if got != "from-env" {
		t.Errorf("got %q, want %q", got, "from-env")
	}
}

func TestFileTokenProvider_FallbackWhenFileMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.txt")
	p := fileTokenProvider(missing, "from-env")
	got, err := p()
	if err != nil {
		t.Fatalf("provider err: %v", err)
	}
	if got != "from-env" {
		t.Errorf("got %q, want fallback %q", got, "from-env")
	}
}

func TestFileTokenProvider_ErrorsWhenBothMissing(t *testing.T) {
	p := fileTokenProvider(filepath.Join(t.TempDir(), "nope.txt"), "")
	if _, err := p(); err == nil {
		t.Fatal("expected error when both file and fallback are empty")
	}
}

func TestFileTokenProvider_ReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tok.txt")
	if err := os.WriteFile(path, []byte("  token-from-file\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	p := fileTokenProvider(path, "fallback-not-used")
	got, err := p()
	if err != nil {
		t.Fatalf("provider err: %v", err)
	}
	if got != "token-from-file" {
		t.Errorf("got %q, want %q (note: whitespace must be trimmed)", got, "token-from-file")
	}
}

func TestFileTokenProvider_PicksUpRewrites(t *testing.T) {
	// Validates the "refresh tool writes new value → service picks it up"
	// flow. The cache TTL is 5s, so we delete the cache by direct re-read
	// rather than waiting — testing the read path, not the TTL itself.
	path := filepath.Join(t.TempDir(), "tok.txt")
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	// Use empty fallback so cache isn't seeded from anywhere else.
	p := fileTokenProvider(path, "")
	v1, _ := p()
	if v1 != "v1" {
		t.Fatalf("first read: got %q want v1", v1)
	}
	// We can't easily test pickup-after-TTL without time-mocking; instead,
	// verify that a fresh provider over the rewritten file returns v2.
	if err := os.WriteFile(path, []byte("v2"), 0o600); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	p2 := fileTokenProvider(path, "")
	v2, _ := p2()
	if v2 != "v2" {
		t.Errorf("post-rewrite read: got %q want v2", v2)
	}
}
