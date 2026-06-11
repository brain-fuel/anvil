package serve

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// ipLimiter is a per-IP token bucket. Booking is a public, unauthenticated
// POST that writes to real calendars — the one endpoint worth throttling.
type ipLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens added per second
	burst   float64
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(ratePerMinute, burst float64) *ipLimiter {
	return &ipLimiter{
		buckets: map[string]*bucket{},
		rate:    ratePerMinute / 60,
		burst:   burst,
		now:     time.Now,
	}
}

// allow reports whether one request from ip may proceed.
func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[ip]
	if !ok {
		// Lazy prune: cap memory if someone sprays source addresses.
		if len(l.buckets) > 10000 {
			for k, v := range l.buckets {
				if now.Sub(v.last) > time.Hour {
					delete(l.buckets, k)
				}
			}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limit wraps a handler with the per-IP limiter.
func (l *ipLimiter) limit(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !l.allow(ip) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many requests — slow down", http.StatusTooManyRequests)
			return
		}
		h(w, r)
	}
}
