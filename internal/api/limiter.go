package api

import (
	"sync"
	"time"
)

type limitEntry struct {
	expiresAt time.Time
	count     int
}
type limiter struct {
	mu      sync.Mutex
	entries map[string]limitEntry
	max     int
	now     func() time.Time
}

func newLimiter(max int) *limiter {
	return &limiter{entries: make(map[string]limitEntry), max: max, now: time.Now}
}

func (l *limiter) allow(key string, window time.Duration, limit int) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, e := range l.entries {
		if !now.Before(e.expiresAt) {
			delete(l.entries, k)
		}
	}
	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= l.max {
			return false, window
		}
		l.entries[key] = limitEntry{expiresAt: now.Add(window), count: 1}
		return true, 0
	}
	if e.count >= limit {
		return false, e.expiresAt.Sub(now)
	}
	e.count++
	l.entries[key] = e
	return true, 0
}
