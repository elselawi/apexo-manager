package auth

import (
	"sync"
	"time"
)

// LoginLimiter is a small in-memory brute-force guard keyed by client IP.
// It is intentionally local to the process: sessions and audit logs provide
// the durable record, while this guard only needs short-lived state.
type LoginLimiter struct {
	mu      sync.Mutex
	entries map[string]*limitEntry
	now     func() time.Time
}

type limitEntry struct {
	windowStart  time.Time
	failures     int
	blockedUntil time.Time
}

const (
	maxFailures = 8
	window      = 15 * time.Minute
	blockFor    = 15 * time.Minute
)

// NewLoginLimiter creates a limiter.
func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{entries: map[string]*limitEntry{}, now: time.Now}
}

// Allow reports whether a login attempt may proceed.
func (l *LoginLimiter) Allow(key string) (bool, time.Duration) {
	if key == "" {
		key = "unknown"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if !ok {
		return true, 0
	}
	if now.Before(e.blockedUntil) {
		return false, e.blockedUntil.Sub(now)
	}
	if now.Sub(e.windowStart) > window {
		delete(l.entries, key)
		return true, 0
	}
	return true, 0
}

// Failure records a failed attempt and blocks after the threshold.
func (l *LoginLimiter) Failure(key string) {
	if key == "" {
		key = "unknown"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if !ok || now.Sub(e.windowStart) > window {
		e = &limitEntry{windowStart: now}
		l.entries[key] = e
	}
	e.failures++
	if e.failures >= maxFailures {
		e.blockedUntil = now.Add(blockFor)
		e.failures = 0
		e.windowStart = now
	}
	if len(l.entries) > 4096 {
		for k, v := range l.entries {
			if now.Sub(v.windowStart) > 2*window && now.After(v.blockedUntil) {
				delete(l.entries, k)
			}
		}
	}
}

// Success clears the failure state for a key.
func (l *LoginLimiter) Success(key string) {
	if key == "" {
		key = "unknown"
	}
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}
