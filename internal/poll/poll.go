// Package poll wires the neverskip client, parser, state store and notifier
// into a single loop. One tick:
//
//  1. fetch lounge + dailynotice concurrently
//  2. parse each response into state.Item
//  3. INSERT OR IGNORE — collect items that were genuinely new
//  4. for each new item, push to ntfy
//
// Errors in one source do not kill the loop. Three failed cycles in a row
// emit an operator notification via ntfy so a broken sync can't be silent.
package poll

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/nayan/neverskip-sync/internal/neverskip"
	"github.com/nayan/neverskip-sync/internal/notifier"
	"github.com/nayan/neverskip-sync/internal/parser"
	"github.com/nayan/neverskip-sync/internal/state"
)

// Invalidator is the slice of the calendar handler the poll loop calls when
// new items appear. Kept as an interface so the poll package doesn't import
// the calendar package.
type Invalidator interface {
	Invalidate()
}

type Loop struct {
	Client       *neverskip.Client
	Store        *state.Store
	Notifier     *notifier.Ntfy
	Calendar     Invalidator
	Interval     time.Duration
	QuietHours   bool
	Log          *slog.Logger
	consecBad    int
	healthAlerts map[int]bool
}

func New(c *neverskip.Client, s *state.Store, n *notifier.Ntfy, cal Invalidator, interval time.Duration, quietHours bool, log *slog.Logger) *Loop {
	return &Loop{
		Client:       c,
		Store:        s,
		Notifier:     n,
		Calendar:     cal,
		Interval:     interval,
		QuietHours:   quietHours,
		Log:          log,
		healthAlerts: map[int]bool{},
	}
}

// Run blocks until ctx is cancelled. It performs an initial tick immediately,
// then ticks at `Interval ± 30s` jitter.
func (l *Loop) Run(ctx context.Context) error {
	if err := l.bootstrapIfNeeded(ctx); err != nil {
		return err
	}

	for {
		if !inQuietWindow(l.QuietHours, time.Now()) {
			l.tick(ctx)
		} else {
			l.Log.Debug("skipping tick — quiet hours")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jittered(l.Interval)):
		}
	}
}

// RunOnce performs bootstrap (if needed) plus a single tick, then returns.
// Intended for smoke-testing the full pipeline without leaving the service
// running.
func (l *Loop) RunOnce(ctx context.Context) error {
	if err := l.bootstrapIfNeeded(ctx); err != nil {
		return err
	}
	l.tick(ctx)
	return nil
}

// bootstrapIfNeeded does a one-shot fetch with notifications suppressed on
// a fresh database, so we don't push-notify a year of backlog on first run.
func (l *Loop) bootstrapIfNeeded(ctx context.Context) error {
	done, err := l.Store.IsBootstrapped(ctx)
	if err != nil {
		return err
	}
	if done {
		return nil
	}
	l.Log.Info("bootstrap: fresh database, marking existing items as seen without notifying")
	items, err := l.fetchAll(ctx)
	if err != nil {
		l.Log.Warn("bootstrap fetch failed; will retry on next tick", "err", err)
		return nil
	}
	newCount := 0
	for _, it := range items {
		ok, err := l.Store.MarkSeen(ctx, it)
		if err != nil {
			l.Log.Error("bootstrap mark seen failed", "err", err, "msg_id", it.MsgID)
			continue
		}
		if ok {
			newCount++
		}
	}
	if err := l.Store.SetBootstrapped(ctx); err != nil {
		return err
	}
	l.Log.Info("bootstrap complete", "items", newCount)
	return nil
}

func (l *Loop) tick(ctx context.Context) {
	items, err := l.fetchAll(ctx)
	if err != nil {
		l.handleTickFailure(ctx, err)
		return
	}
	l.consecBad = 0

	var newItems []state.Item
	for _, it := range items {
		isNew, err := l.Store.MarkSeen(ctx, it)
		if err != nil {
			l.Log.Error("mark seen failed", "err", err, "source", it.Source, "msg_id", it.MsgID)
			continue
		}
		if isNew {
			newItems = append(newItems, it)
		}
	}
	if len(newItems) == 0 {
		l.Log.Debug("tick: no new items")
		return
	}
	l.Log.Info("tick: new items", "count", len(newItems))

	if l.Calendar != nil {
		l.Calendar.Invalidate()
	}

	for _, it := range newItems {
		if err := l.Notifier.Notify(ctx, it); err != nil {
			l.Log.Warn("ntfy push failed", "err", err, "source", it.Source, "msg_id", it.MsgID)
		}
	}
}

// fetchAll calls both endpoints concurrently and merges the parsed items.
// An error in one source does not kill the other.
func (l *Loop) fetchAll(ctx context.Context) ([]state.Item, error) {
	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		out         []state.Item
		loungeErr   error
		noticeErr   error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		r, err := l.Client.Lounge(ctx)
		if err != nil {
			loungeErr = err
			return
		}
		mu.Lock()
		for _, raw := range r.D.ItemList {
			if it, ok := parser.ParseLounge(raw); ok {
				out = append(out, it)
			}
		}
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		r, err := l.Client.DailyNotice(ctx)
		if err != nil {
			noticeErr = err
			return
		}
		mu.Lock()
		for _, raw := range r.D.ItemList {
			if it, ok := parser.ParseDailyNotice(raw); ok {
				out = append(out, it)
			}
		}
		mu.Unlock()
	}()
	wg.Wait()

	if loungeErr != nil && noticeErr != nil {
		return nil, errors.Join(loungeErr, noticeErr)
	}
	if loungeErr != nil {
		l.Log.Warn("lounge fetch failed (continuing)", "err", loungeErr)
	}
	if noticeErr != nil {
		l.Log.Warn("dailynotice fetch failed (continuing)", "err", noticeErr)
	}
	return out, nil
}

func (l *Loop) handleTickFailure(ctx context.Context, err error) {
	l.consecBad++
	l.Log.Warn("tick failed", "consecutive", l.consecBad, "err", err)

	// Specific message + priority bump on auth failures so the human knows
	// it's a "re-pair the token" job, not a transient outage.
	if errors.Is(err, neverskip.ErrUnauthenticated) && !l.healthAlerts[1] {
		l.healthAlerts[1] = true
		_ = l.Notifier.Plain(ctx,
			"Neverskip token expired",
			"The captured 'token' cookie no longer works. Re-pair: log in to parent.neverskip.com in Chrome, run scripts/refresh_token.sh, and restart the service.",
			"5",
		)
		return
	}
	if l.consecBad == 3 && !l.healthAlerts[3] {
		l.healthAlerts[3] = true
		_ = l.Notifier.Plain(ctx, "Neverskip sync is unhealthy", err.Error(), "4")
	}
	if l.consecBad == 10 && !l.healthAlerts[10] {
		l.healthAlerts[10] = true
		_ = l.Notifier.Plain(ctx, "Neverskip sync still broken (10x)", err.Error(), "5")
	}
}

func jittered(d time.Duration) time.Duration {
	jitter := time.Duration(rand.Int64N(60_000)) * time.Millisecond
	return d - 30*time.Second + jitter
}

func inQuietWindow(enabled bool, now time.Time) bool {
	if !enabled {
		return false
	}
	t := now.In(parser.IST)
	h := t.Hour()
	return h >= 23 || h < 6
}
