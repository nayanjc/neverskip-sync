package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	NeverskipToken string
	NtfyURL        string
	NtfyTopic      string
	ICSToken       string
	CalendarHost   string
	SQLitePath     string
	PollInterval   time.Duration
	ListenAddr     string
	LogLevel       slog.Level
	QuietHours     bool
}

func Load() (Config, error) {
	c := Config{
		NeverskipToken: os.Getenv("NEVERSKIP_TOKEN"),
		NtfyURL:        getenvDefault("NTFY_URL", "https://ntfy.sh"),
		NtfyTopic:      os.Getenv("NTFY_TOPIC"),
		ICSToken:       os.Getenv("ICS_TOKEN"),
		CalendarHost:   getenvDefault("CALENDAR_HOST", "spectretrade.in"),
		SQLitePath:     getenvDefault("SQLITE_PATH", "/var/lib/neverskip-sync/state.db"),
		ListenAddr:     getenvDefault("LISTEN_ADDR", "127.0.0.1:8080"),
		QuietHours:     getenvBool("QUIET_HOURS", false),
	}

	pollRaw := getenvDefault("POLL_INTERVAL", "15m")
	d, err := time.ParseDuration(pollRaw)
	if err != nil {
		return Config{}, fmt.Errorf("POLL_INTERVAL: %w", err)
	}
	if d < time.Minute {
		return Config{}, fmt.Errorf("POLL_INTERVAL must be >= 1m, got %s", d)
	}
	c.PollInterval = d

	lvl, err := parseLogLevel(getenvDefault("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	c.LogLevel = lvl

	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() error {
	var missing []string
	if c.NeverskipToken == "" {
		missing = append(missing, "NEVERSKIP_TOKEN")
	}
	if c.NtfyTopic == "" {
		missing = append(missing, "NTFY_TOPIC")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	return nil
}

func getenvDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func getenvBool(k string, d bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	switch v {
	case "":
		return d
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return d
	}
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("LOG_LEVEL must be one of debug|info|warn|error")
	}
}
