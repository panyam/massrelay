package server

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/panyam/massrelay/services"
	"golang.org/x/time/rate"
)

// RateLimitConfig controls connection-level rate limiting.
type RateLimitConfig struct {
	GlobalConnPerSec float64 // max WebSocket connections/sec globally (0 = unlimited)
	PerIPConnPerSec  float64 // max WebSocket connections/sec per IP (0 = unlimited)
	PerIPBurst       int     // burst allowance per IP
	IPLimiterTTL     time.Duration // cleanup interval for per-IP limiters
}

// DefaultRateLimitConfig returns sensible defaults.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		GlobalConnPerSec: 100,
		PerIPConnPerSec:  5,
		PerIPBurst:       3,
		IPLimiterTTL:     5 * time.Minute,
	}
}

// RelayApp is the HTTP application for the relay server.
// It implements http.Handler so it can be used standalone or embedded
// as a sub-handler in another server's mux:
//
//	// Standalone
//	http.ListenAndServe(":8787", relayApp)
//
//	// Embedded in another mux
//	mux.Handle("/relay/", http.StripPrefix("/relay", relayApp))
type RelayApp struct {
	Service       *services.CollabService
	RateLimit     RateLimitConfig
	mux           *http.ServeMux
	globalLimiter *rate.Limiter
	ipLimiters    map[string]*ipLimiterEntry
	ipMu          sync.Mutex
}

type ipLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRelayApp creates a new RelayApp.
func NewRelayApp() *RelayApp {
	cfg := DefaultRateLimitConfig()
	app := &RelayApp{
		Service:       services.NewCollabService(),
		RateLimit:     cfg,
		mux:           http.NewServeMux(),
		globalLimiter: rate.NewLimiter(rate.Limit(cfg.GlobalConnPerSec), int(cfg.GlobalConnPerSec)),
		ipLimiters:    make(map[string]*ipLimiterEntry),
	}
	// Background cleanup of stale per-IP limiters
	go app.cleanupIPLimiters()
	return app
}

// Init sets up routes.
func (a *RelayApp) Init() error {
	h := NewApiHandler(a)
	h.SetupRoutes(a.mux)
	return nil
}

// getIPLimiter returns or creates a per-IP rate limiter.
func (a *RelayApp) getIPLimiter(ip string) *rate.Limiter {
	a.ipMu.Lock()
	defer a.ipMu.Unlock()
	entry, ok := a.ipLimiters[ip]
	if !ok {
		entry = &ipLimiterEntry{
			limiter: rate.NewLimiter(rate.Limit(a.RateLimit.PerIPConnPerSec), a.RateLimit.PerIPBurst),
		}
		a.ipLimiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

// cleanupIPLimiters periodically removes stale per-IP limiters.
func (a *RelayApp) cleanupIPLimiters() {
	ticker := time.NewTicker(a.RateLimit.IPLimiterTTL)
	defer ticker.Stop()
	for range ticker.C {
		a.ipMu.Lock()
		cutoff := time.Now().Add(-a.RateLimit.IPLimiterTTL)
		for ip, entry := range a.ipLimiters {
			if entry.lastSeen.Before(cutoff) {
				delete(a.ipLimiters, ip)
			}
		}
		a.ipMu.Unlock()
	}
}

// clientIP extracts the client IP from the request.
func clientIP(r *http.Request) string {
	// Check X-Forwarded-For for reverse proxy setups
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First IP in the chain is the original client
		if idx := len(xff); idx > 0 {
			for i, c := range xff {
				if c == ',' {
					return xff[:i]
				}
			}
			return xff
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ServeHTTP implements http.Handler.
func (a *RelayApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	a.mux.ServeHTTP(w, r)
}
