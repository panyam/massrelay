package middleware

import "net/http"

// Guard composes all hardening middleware into a single wrapper.
// Each component is nil-safe — if not configured, it's a no-op.
type Guard struct {
	Origin    *OriginChecker
	Conn      *ConnLimiter
	RateLimit *RateLimiter
	Auth      *RelayAuthenticator
}

// Wrap applies all configured hardening middleware to a handler.
// Order: origin check → rate limit → auth → connection limit → handler.
func (g *Guard) Wrap(h http.Handler) http.Handler {
	if g == nil {
		return h
	}
	h = g.Conn.Middleware(h)
	h = g.Auth.Middleware(h)
	h = g.RateLimit.Middleware(h)
	h = g.Origin.Middleware(h)
	return h
}
