// neverskip-sync: poll the lounge and dailynotice API endpoints, dedup new
// items via SQLite, push them to ntfy, and serve an ICS calendar feed at
// /school/calendar.ics for iOS/macOS/Google Calendar to subscribe to.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nayan/neverskip-sync/internal/calendar"
	"github.com/nayan/neverskip-sync/internal/config"
	"github.com/nayan/neverskip-sync/internal/neverskip"
	"github.com/nayan/neverskip-sync/internal/notifier"
	"github.com/nayan/neverskip-sync/internal/poll"
	"github.com/nayan/neverskip-sync/internal/state"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		once     = flag.Bool("once", false, "run one poll tick (after bootstrap) then exit; for smoke testing")
		noServer = flag.Bool("no-server", false, "don't bind the HTTP listener (useful with -once)")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	store, err := state.Open(cfg.SQLitePath)
	if err != nil {
		return err
	}
	defer store.Close()

	client := neverskip.NewWithProvider(fileTokenProvider(cfg.TokenFile, cfg.NeverskipToken))
	ntfy := notifier.New(cfg.NtfyURL, cfg.NtfyTopic)
	cal := calendar.New(store, cfg.ICSToken, cfg.CalendarHost, logger.With("component", "calendar"))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Sanity-check the token at startup so a bad NEVERSKIP_TOKEN surfaces
	// immediately rather than three tick cycles later.
	probeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
	if err := client.HasAuth(probeCtx); err != nil {
		probeCancel()
		_ = ntfy.Plain(ctx, "Neverskip sync failed to start",
			"Token rejected at startup. Re-pair: capture the 'token' cookie from a fresh browser login.", "5")
		return err
	}
	probeCancel()
	logger.Info("startup auth probe ok")

	loop := poll.New(client, store, ntfy, cal, cfg.PollInterval, cfg.QuietHours, logger.With("component", "poll"))

	var srv *http.Server
	if !*noServer {
		srv = newHTTPServer(cfg.ListenAddr, store, cal, logger)
		go func() {
			logger.Info("http listening", "addr", cfg.ListenAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("http server stopped", "err", err)
				cancel()
			}
		}()
	}

	var loopErr error
	if *once {
		logger.Info("running single tick (-once)")
		loopErr = loop.RunOnce(ctx)
	} else {
		logger.Info("starting poll loop", "interval", cfg.PollInterval, "quiet_hours", cfg.QuietHours)
		loopErr = loop.Run(ctx)
	}

	if srv != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}

	if loopErr != nil && loopErr != context.Canceled {
		return loopErr
	}
	logger.Info("shutdown complete")
	return nil
}

func newHTTPServer(addr string, store *state.Store, cal *calendar.Handler, log *slog.Logger) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /school/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /school/debug/recent", func(w http.ResponseWriter, r *http.Request) {
		items, err := store.RecentSeen(r.Context(), 20)
		if err != nil {
			log.Error("debug/recent failed", "err", err)
			http.Error(w, "internal error", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	})

	mux.Handle("GET /school/calendar.ics", cal)

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
