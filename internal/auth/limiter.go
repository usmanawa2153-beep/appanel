package auth

import (
	"sync"
	"time"
)

type attempt struct {
	fails       int
	windowStart time.Time
	lockedUntil time.Time
}

// Limiter locks a key (usually a client IP) after repeated failures.
type Limiter struct {
	mu     sync.Mutex
	m      map[string]*attempt
	max    int
	window time.Duration
	lock   time.Duration
}

// NewLimiter allows max failures per window, then blocks the key for lock.
func NewLimiter(max int, window, lock time.Duration) *Limiter {
	return &Limiter{m: map[string]*attempt{}, max: max, window: window, lock: lock}
}

// Allowed reports whether key may try again, and how long to wait if not.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.m[key]
	if !ok {
		return true, 0
	}
	if wait := time.Until(a.lockedUntil); wait > 0 {
		return false, wait
	}
	return true, 0
}

// Fail records a failed attempt for key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.m) > 10_000 { // bound memory under a distributed spray
		for k, v := range l.m {
			if now.After(v.lockedUntil) && now.Sub(v.windowStart) > l.window {
				delete(l.m, k)
			}
		}
	}
	a, ok := l.m[key]
	if !ok || now.Sub(a.windowStart) > l.window {
		a = &attempt{windowStart: now}
		l.m[key] = a
	}
	a.fails++
	if a.fails >= l.max {
		a.lockedUntil = now.Add(l.lock)
		a.fails = 0
		a.windowStart = now
	}
}

// Reset clears failures for key after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}
