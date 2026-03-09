package middleware

import (
	"log/slog"
	"net/http"
	"sync/atomic"
)

// ConnLimiter limits the number of concurrent active requests to a handler.
// Designed for WebSocket endpoints where each connection holds resources
// for the lifetime of the session.
//
// When the limit is reached, new requests receive 503 Service Unavailable.
// The counter decrements when the request handler returns (connection closes).
type ConnLimiter struct {
	max     int64
	active  atomic.Int64
}

// NewConnLimiter creates a limiter with the given max concurrent connections.
// Pass 0 for unlimited (returns nil — caller should skip the middleware).
func NewConnLimiter(max int64) *ConnLimiter {
	if max <= 0 {
		return nil
	}
	return &ConnLimiter{max: max}
}

// Active returns the current number of active connections.
func (c *ConnLimiter) Active() int64 {
	if c == nil {
		return 0
	}
	return c.active.Load()
}

// Middleware returns an HTTP middleware that enforces the connection limit.
func (c *ConnLimiter) Middleware(next http.Handler) http.Handler {
	if c == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := c.active.Add(1)
		if current > c.max {
			c.active.Add(-1)
			slog.Warn("Rejected connection", "component", "connlimit", "active", current-1, "max", c.max, "ip", clientIPFromRequest(r))
			http.Error(w, `{"error":"too many connections"}`, http.StatusServiceUnavailable)
			return
		}
		defer c.active.Add(-1)
		next.ServeHTTP(w, r)
	})
}
