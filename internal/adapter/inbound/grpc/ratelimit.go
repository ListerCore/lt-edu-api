package grpc

import (
	"sync"
	"time"
)

// rateLimiter is a fixed window per key, in memory (one API instance, like Fiber's limiter on the HTTP side)
type rateLimiter struct {
	max    int
	window time.Duration

	mu        sync.Mutex
	hits      map[string]*windowHits
	lastSweep time.Time
}

type windowHits struct {
	start time.Time
	count int
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, hits: map[string]*windowHits{}}
}

func (l *rateLimiter) allow(key string) bool {
	return l.allowAt(key, time.Now())
}

func (l *rateLimiter) allowAt(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Forget finished windows now and then so the map cannot grow without bound
	if now.Sub(l.lastSweep) > l.window {
		for k, h := range l.hits {
			if now.Sub(h.start) >= l.window {
				delete(l.hits, k)
			}
		}
		l.lastSweep = now
	}

	h, ok := l.hits[key]
	if !ok || now.Sub(h.start) >= l.window {
		l.hits[key] = &windowHits{start: now, count: 1}
		return true
	}
	if h.count >= l.max {
		return false
	}
	h.count++
	return true
}
