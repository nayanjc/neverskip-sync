package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMarkSeenDedup(t *testing.T) {
	t.Parallel()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	it := Item{
		Source:     "lounge",
		MsgID:      "34489",
		Section:    "I - E",
		CleanTitle: "Newsletter for May",
		PostedAt:   &now,
	}

	isNew, err := s.MarkSeen(ctx, it)
	if err != nil {
		t.Fatalf("first MarkSeen: %v", err)
	}
	if !isNew {
		t.Fatal("expected first insert to report new")
	}
	isNew, err = s.MarkSeen(ctx, it)
	if err != nil {
		t.Fatalf("second MarkSeen: %v", err)
	}
	if isNew {
		t.Fatal("expected duplicate insert to report not-new")
	}
}

func TestBootstrapFlag(t *testing.T) {
	t.Parallel()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	ok, err := s.IsBootstrapped(ctx)
	if err != nil {
		t.Fatalf("IsBootstrapped: %v", err)
	}
	if ok {
		t.Fatal("expected not bootstrapped on fresh db")
	}
	if err := s.SetBootstrapped(ctx); err != nil {
		t.Fatalf("SetBootstrapped: %v", err)
	}
	ok, err = s.IsBootstrapped(ctx)
	if err != nil {
		t.Fatalf("IsBootstrapped post: %v", err)
	}
	if !ok {
		t.Fatal("expected bootstrapped after SetBootstrapped")
	}
}
