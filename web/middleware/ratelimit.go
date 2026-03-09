package middleware

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig controls connection-level rate limiting.
type RateLimitConfig struct {
	GlobalPerSec float64       // max requests/sec globally (0 = unlimited)
	PerIPPerSec  float64       // max requests/sec per IP (0 = unlimited)
	PerIPBurst   int           // burst allowance per IP
	IPLimiterTTL time.Duration // cleanup interval for stale per-IP limiters
}

// DefaultRateLimitConfig returns sensible defaults.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		GlobalPerSec: 100,
		PerIPPerSec:  5,
		PerIPBurst:   3,
		IPLimiterTTL: 5 * time.Minute,
	}
}

// RateLimiter enforces global and per-IP rate limits.
type RateLimiter struct {
	Config        RateLimitConfig
	globalLimiter *rate.Limiter
	ipLimiters    map[string]*ipLimiterEntry
	mu            sync.Mutex
	// OnRejected is called when a request is rate-limited (for metrics).
	OnRejected func()
}

type ipLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a rate limiter. Returns nil if both limits are 0.
func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	if cfg.GlobalPerSec <= 0 && cfg.PerIPPerSec <= 0 {
		return nil
	}
	rl := &RateLimiter{
		Config:     cfg,
		ipLimiters: make(map[string]*ipLimiterEntry),
	}
	if cfg.GlobalPerSec > 0 {
		rl.globalLimiter = rate.NewLimiter(rate.Limit(cfg.GlobalPerSec), int(cfg.GlobalPerSec))
	}
	if cfg.IPLimiterTTL > 0 {
		go rl.cleanupIPLimiters()
	}
	return rl
}

func (rl *RateLimiter) getIPLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	entry, ok := rl.ipLimiters[ip]
	if !ok {
		entry = &ipLimiterEntry{
			limiter: rate.NewLimiter(rate.Limit(rl.Config.PerIPPerSec), rl.Config.PerIPBurst),
		}
		rl.ipLimiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

func (rl *RateLimiter) cleanupIPLimiters() {
	ticker := time.NewTicker(rl.Config.IPLimiterTTL)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-rl.Config.IPLimiterTTL)
		for ip, entry := range rl.ipLimiters {
			if entry.lastSeen.Before(cutoff) {
				delete(rl.ipLimiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Allow checks both global and per-IP rate limits. Returns false if rejected.
func (rl *RateLimiter) Allow(ip string) bool {
	if rl == nil {
		return true
	}
	if rl.globalLimiter != nil && !rl.globalLimiter.Allow() {
		return false
	}
	if rl.Config.PerIPPerSec > 0 && !rl.getIPLimiter(ip).Allow() {
		return false
	}
	return true
}

// Middleware returns an HTTP middleware that enforces rate limits.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	if rl == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
		if !rl.Allow(ip) {
			if rl.OnRejected != nil {
				rl.OnRejected()
			}
			slog.Warn("Rate limited request", "component", "ratelimit", "ip", ip, "path", r.URL.Path)
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP extracts the client IP from the request.
// Checks X-Forwarded-For for reverse proxy setups.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	// Handle IPv6 [::1]:port
	addr := r.RemoteAddr
	if strings.HasPrefix(addr, "[") {
		if i := strings.LastIndex(addr, "]"); i >= 0 {
			return addr[1:i]
		}
	}
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}
