package auth

import (
	"net"
	"sync"
	"time"
)

const (
	loginFailureWindow = 15 * time.Minute
	loginFailureLimit  = 8
	loginCleanupPeriod = time.Minute
)

type loginFailureEntry struct {
	attempts  int
	expiresAt time.Time
}

var (
	loginFailuresMu  sync.Mutex
	loginFailures    = make(map[string]loginFailureEntry)
	loginLastCleanup time.Time
)

func LoginAllowed(remoteAddr string) (bool, time.Duration) {
	key := loginLimiterKey(remoteAddr)
	now := time.Now()

	loginFailuresMu.Lock()
	defer loginFailuresMu.Unlock()
	cleanupLoginFailuresLocked(now)

	entry, ok := loginFailures[key]
	if !ok || now.After(entry.expiresAt) || entry.attempts < loginFailureLimit {
		return true, 0
	}
	return false, time.Until(entry.expiresAt)
}

func RecordLoginFailure(remoteAddr string) {
	key := loginLimiterKey(remoteAddr)
	now := time.Now()

	loginFailuresMu.Lock()
	defer loginFailuresMu.Unlock()
	cleanupLoginFailuresLocked(now)

	entry := loginFailures[key]
	if entry.expiresAt.IsZero() || now.After(entry.expiresAt) {
		entry = loginFailureEntry{expiresAt: now.Add(loginFailureWindow)}
	}
	entry.attempts++
	loginFailures[key] = entry
}

func ClearLoginFailures(remoteAddr string) {
	loginFailuresMu.Lock()
	delete(loginFailures, loginLimiterKey(remoteAddr))
	loginFailuresMu.Unlock()
}

func cleanupLoginFailuresLocked(now time.Time) {
	if !loginLastCleanup.IsZero() && now.Sub(loginLastCleanup) < loginCleanupPeriod {
		return
	}
	for key, entry := range loginFailures {
		if now.After(entry.expiresAt) {
			delete(loginFailures, key)
		}
	}
	loginLastCleanup = now
}

func loginLimiterKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}
	if remoteAddr == "" {
		return "unknown"
	}
	return remoteAddr
}
