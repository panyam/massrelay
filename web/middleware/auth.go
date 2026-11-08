package middleware

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/panyam/oneauth/apiauth"
	"github.com/panyam/oneauth/keys"
)

// RelayClaims holds the validated JWT claims relevant to the relay, read
// from the TokenInfo oneauth returns once it has verified the token.
type RelayClaims struct {
	Subject   string    // JWT "sub" claim (user ID)
	ClientID  string    // custom "client_id" claim (host identifier)
	Scopes    []string  // JWT "scopes" claim
	Rooms     []string  // custom "rooms" claim: sessions this token may join (exact, or prefix ending in "*"); empty = any
	ExpiresAt time.Time // JWT "exp" claim
	IssuedAt  time.Time // JWT "iat" claim
}

// relayContextKey is a private type for relay auth context keys.
type relayContextKey string

const contextKeyRelayClaims relayContextKey = "relay_claims"

// GetRelayClaimsFromContext retrieves the RelayClaims from the request context.
// Returns nil if the request is unauthenticated.
func GetRelayClaimsFromContext(ctx context.Context) *RelayClaims {
	if v := ctx.Value(contextKeyRelayClaims); v != nil {
		if claims, ok := v.(*RelayClaims); ok {
			return claims
		}
	}
	return nil
}

// RelayAuthConfig configures the RelayAuthenticator.
type RelayAuthConfig struct {
	// KeyStore for multi-tenant JWT validation. If nil, auth is disabled.
	KeyStore keys.KeyLookup

	// Required rejects unauthenticated connections when true.
	// When false, unauthenticated requests pass through without claims.
	Required bool

	// Issuer is the expected JWT "iss" claim. Empty = don't validate issuer.
	Issuer string

	// TokenQueryParam is the query parameter name for token extraction fallback.
	// Empty = disabled. Typically "token" for WebSocket clients.
	TokenQueryParam string

	// OnAuthenticated is called after successful authentication.
	OnAuthenticated func(r *http.Request, claims *RelayClaims)

	// OnRejected is called when authentication fails.
	OnRejected func(r *http.Request, err error)
}

// RelayAuthenticator validates relay JWTs using oneauth's APIMiddleware.
// Nil-safe: a nil authenticator is a no-op passthrough.
type RelayAuthenticator struct {
	mw       *apiauth.APIMiddleware
	config   RelayAuthConfig
	denyList map[string]struct{}
	onDeny   []func(sub string)
	denyMu   sync.RWMutex
}

// NewRelayAuthenticator creates a new authenticator. Returns nil if no KeyStore
// is configured, making the caller's nil checks act as a feature flag.
//
// Example flows:
//
//	# Auth disabled (default) — KeyStore is nil, returns nil, all requests pass through:
//	  auth := NewRelayAuthenticator(RelayAuthConfig{})  // nil
//	  auth.Middleware(handler)                           // returns handler unchanged
//
//	# Auth optional — validates tokens when present, allows anonymous otherwise:
//	  auth := NewRelayAuthenticator(RelayAuthConfig{
//	      KeyStore:        keyStore,
//	      TokenQueryParam: "token",
//	  })
//	  // GET /ws                         → 200, no claims in context
//	  // GET /ws?token=<valid-jwt>        → 200, RelayClaims in context
//	  // Authorization: Bearer <valid>    → 200, RelayClaims in context
//	  // Authorization: Bearer <expired>  → 401
//
//	# Auth required — rejects all unauthenticated connections:
//	  auth := NewRelayAuthenticator(RelayAuthConfig{
//	      KeyStore:        keyStore,
//	      Required:        true,
//	      Issuer:          "oneauth.example.com",
//	      TokenQueryParam: "token",
//	  })
//	  // GET /ws (no token)              → 401
//	  // GET /ws?token=<wrong-issuer>    → 401
//	  // GET /ws?token=<valid>           → 200, RelayClaims in context
//
//	# Deny list — ban specific subjects even with valid tokens:
//	  auth.DenySub("user-123")
//	  // Bearer <valid token for user-123> → 403
//	  auth.AllowSub("user-123")
//	  // Bearer <valid token for user-123> → 200
func NewRelayAuthenticator(cfg RelayAuthConfig) *RelayAuthenticator {
	if cfg.KeyStore == nil {
		return nil
	}
	return &RelayAuthenticator{
		mw: &apiauth.APIMiddleware{
			KeyStore:               cfg.KeyStore,
			JWTIssuer:              cfg.Issuer,
			LegacyQueryParamBearer: cfg.TokenQueryParam,
		},
		config:   cfg,
		denyList: make(map[string]struct{}),
	}
}

// DenySub adds a subject to the deny list. Denied subjects get 403 on
// upgrade and have every ReAuth refused, even with valid tokens. It then
// calls each OnDeny listener with sub, every time, so denying a subject
// twice ends whatever it opened in between. The list is in memory only and
// a restarted relay forgets it.
func (a *RelayAuthenticator) DenySub(sub string) {
	if a == nil {
		return
	}
	a.denyMu.Lock()
	a.denyList[sub] = struct{}{}
	listeners := append([]func(string){}, a.onDeny...)
	a.denyMu.Unlock()
	for _, fn := range listeners {
		fn(sub)
	}
}

// OnDeny registers fn to run after each DenySub, outside the authenticator's
// lock. The relay's streams register one that ends the subject's open
// connections, so an embedder calling DenySub directly gets that too.
// Nil-safe.
func (a *RelayAuthenticator) OnDeny(fn func(sub string)) {
	if a == nil {
		return
	}
	a.denyMu.Lock()
	defer a.denyMu.Unlock()
	a.onDeny = append(a.onDeny, fn)
}

// DeniedSubs returns the deny list, sorted. Nil on a nil authenticator.
func (a *RelayAuthenticator) DeniedSubs() []string {
	if a == nil {
		return nil
	}
	a.denyMu.RLock()
	defer a.denyMu.RUnlock()
	subs := make([]string, 0, len(a.denyList))
	for sub := range a.denyList {
		subs = append(subs, sub)
	}
	sort.Strings(subs)
	return subs
}

// AllowSub removes a subject from the deny list.
func (a *RelayAuthenticator) AllowSub(sub string) {
	if a == nil {
		return
	}
	a.denyMu.Lock()
	defer a.denyMu.Unlock()
	delete(a.denyList, sub)
}

// isDenied checks if a subject is on the deny list.
func (a *RelayAuthenticator) isDenied(sub string) bool {
	a.denyMu.RLock()
	defer a.denyMu.RUnlock()
	_, ok := a.denyList[sub]
	return ok
}

// Middleware returns an HTTP middleware that validates relay JWTs.
// On a nil receiver, returns next unchanged (passthrough).
func (a *RelayAuthenticator) Middleware(next http.Handler) http.Handler {
	if a == nil {
		return next
	}

	// Use oneauth's middleware in the appropriate mode
	var oaMiddleware func(http.Handler) http.Handler
	if a.config.Required {
		oaMiddleware = a.mw.ValidateToken
	} else {
		oaMiddleware = a.mw.Optional
	}

	return oaMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := apiauth.GetTokenInfoFromAPIContext(r.Context())

		// A validated token: check the deny list, then build RelayClaims
		if info != nil && info.Subject != "" {
			userID := info.Subject
			// Check deny list
			if a.isDenied(userID) {
				if a.config.OnRejected != nil {
					a.config.OnRejected(r, errDenied)
				}
				slog.Warn("Denied subject attempted connection", "component", "auth", "sub", userID)
				a.errorJSON(w, "forbidden", "access denied", http.StatusForbidden)
				return
			}

			claims := relayClaims(info)
			ctx := context.WithValue(r.Context(), contextKeyRelayClaims, claims)
			r = r.WithContext(ctx)

			if a.config.OnAuthenticated != nil {
				a.config.OnAuthenticated(r, claims)
			}
		}

		next.ServeHTTP(w, r)
	}))
}

// ErrAuthDisabled is ValidateToken's error on a relay without signing keys,
// which has nothing to check a token against.
var ErrAuthDisabled = errors.New("auth is not configured on this relay")

// ValidateToken checks raw the way Middleware checks a token on the upgrade
// (signature, issuer, expiry, the deny list) and returns its claims. A token
// is required even when the authenticator is optional, and a DPoP-bound
// token is refused, since a bare token has no proof to check. The error is
// oneauth's description of why the token was refused, "subject is denied"
// for a denied subject, or ErrAuthDisabled on a nil authenticator.
func (a *RelayAuthenticator) ValidateToken(ctx context.Context, raw string) (*RelayClaims, error) {
	if a == nil {
		return nil, ErrAuthDisabled
	}
	resp, err := a.mw.ValidateBearer(ctx, &apiauth.ValidateBearerRequest{Token: raw})
	if err != nil {
		return nil, err
	}
	if a.isDenied(resp.Info.Subject) {
		return nil, errDenied
	}
	return relayClaims(resp.Info), nil
}

// relayClaims reads RelayClaims from the token oneauth validated. client_id
// comes from TokenInfo, falling back to the custom claim for a token
// oneauth didn't fill it from.
func relayClaims(info *apiauth.TokenInfo) *RelayClaims {
	claims := &RelayClaims{
		Subject:   info.Subject,
		ClientID:  info.ClientID,
		Scopes:    info.Scopes,
		Rooms:     stringOrStrings(info.CustomClaims["rooms"]),
		ExpiresAt: info.ExpiresAt,
		IssuedAt:  info.IssuedAt,
	}
	if claims.ClientID == "" {
		claims.ClientID, _ = info.CustomClaims["client_id"].(string)
	}
	return claims
}

// stringOrStrings reads a claim that may be a single string or a JSON array of
// strings. Non-string array entries are skipped.
func stringOrStrings(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// errorJSON writes a JSON error response.
func (a *RelayAuthenticator) errorJSON(w http.ResponseWriter, code, description string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

// errDenied is a sentinel error for denied subjects.
var errDenied = &denyError{}

type denyError struct{}

func (e *denyError) Error() string { return "subject is denied" }

// SubjectKeyFunc extracts the authenticated subject from the request context
// for use as a rate limiter key. Returns empty string for unauthenticated
// requests, which causes them to share a single rate bucket (effectively
// skipping per-subject limiting for anonymous users).
func SubjectKeyFunc(r *http.Request) string {
	claims := GetRelayClaimsFromContext(r.Context())
	if claims == nil {
		return ""
	}
	return claims.Subject
}

// --- Admin helpers for deny list management via admin API ---

// IsDenied checks if a subject is on the deny list. Nil-safe.
func (a *RelayAuthenticator) IsDenied(sub string) bool {
	if a == nil {
		return false
	}
	return a.isDenied(sub)
}

// constantTimeEqual compares two strings in constant time.
// Exported for use in admin token validation.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
