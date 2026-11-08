package middleware

import (
	"net/http"

	mw "github.com/panyam/servicekit/middleware"
)

// Guard composes all relay hardening middleware into a single wrapper.
// Order: origin check → IP rate limit → auth → subject rate limit → connection limit → handler.
type Guard struct {
	chain *mw.Guard
	auth  *RelayAuthenticator
}

// NewGuard builds a relay Guard from the given components.
// Each component is nil-safe — if not configured, it's skipped.
func NewGuard(
	origin *mw.OriginChecker,
	ipRateLimit *mw.RateLimiter,
	auth *RelayAuthenticator,
	subRateLimit *mw.RateLimiter,
	conn *mw.ConnLimiter,
) *Guard {
	g := &mw.Guard{}
	g.Use(origin.Middleware)
	if ipRateLimit != nil {
		g.Use(ipRateLimit.Middleware(nil)) // nil KeyFunc = default ClientIP
	}
	g.Use(auth.Middleware)
	if subRateLimit != nil {
		g.Use(subRateLimit.Middleware(SubjectKeyFunc))
	}
	g.Use(conn.Middleware)
	return &Guard{chain: g, auth: auth}
}

// Authenticator returns the authenticator the Guard checks upgrades with, or
// nil when auth is off. Streams check a ReAuthAction's token with it, so a
// refreshed token passes the same checks as the one on the upgrade.
func (g *Guard) Authenticator() *RelayAuthenticator {
	if g == nil {
		return nil
	}
	return g.auth
}

// Wrap applies all configured hardening middleware to a handler.
func (g *Guard) Wrap(h http.Handler) http.Handler {
	if g == nil {
		return h
	}
	return g.chain.Wrap(h)
}
