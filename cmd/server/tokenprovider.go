package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nayan/neverskip-sync/internal/neverskip"
)

// fileTokenProvider returns the contents of a small file holding the
// Neverskip token, falling back to a static value (typically the env-var
// NEVERSKIP_TOKEN) if the file is missing or empty.
//
// The file is re-read on every call. Its size is tiny (≤2 KB) and reads are
// cached for cacheTTL so the cost is negligible even at a tight poll cadence.
// When the refresh-token CLI rewrites the file, the running service picks up
// the new value within cacheTTL with no restart.
func fileTokenProvider(path, fallback string) neverskip.TokenProvider {
	const cacheTTL = 5 * time.Second
	if path == "" {
		return neverskip.StaticToken(fallback)
	}
	var (
		mu       sync.Mutex
		cached   string
		cachedAt time.Time
	)
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached != "" && time.Since(cachedAt) < cacheTTL {
			return cached, nil
		}
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			val := strings.TrimSpace(string(raw))
			if val == "" {
				val = fallback
			}
			cached, cachedAt = val, time.Now()
			return val, nil
		case os.IsNotExist(err):
			if fallback == "" {
				return "", fmt.Errorf("token file %q missing and NEVERSKIP_TOKEN unset", path)
			}
			cached, cachedAt = fallback, time.Now()
			return fallback, nil
		default:
			return "", fmt.Errorf("read token file %q: %w", path, err)
		}
	}
}
