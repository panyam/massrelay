package middleware

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	oa "github.com/panyam/oneauth"
)

// RelayClaims holds the validated JWT claims relevant to the relay.
// Parsed from oneauth's custom claims map after token validation.
type RelayClaims struct {
	Subject    string    // JWT "sub" claim (user ID)
	ClientID   string    // custom "client_id" claim (host identifier)
	Scopes     []string  // JWT "scopes" claim
	MaxRooms   int       // custom "max_rooms" claim (0 = unlimited)
	MaxMsgRate float64   // custom "max_msg_rate" claim (0 = unlimited)
	ExpiresAt  time.Time // JWT "exp" claim
	IssuedAt   time.Time // JWT "iat" claim
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
	KeyStore oa.KeyStore

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
	mw       *oa.APIMiddleware
	config   RelayAuthConfig
	denyList map[string]struct{}
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
		mw: &oa.APIMiddleware{
			KeyStore:        cfg.KeyStore,
			JWTIssuer:       cfg.Issuer,
			TokenQueryParam: cfg.TokenQueryParam,
		},
		config:   cfg,
		denyList: make(map[string]struct{}),
	}
}

// DenySub adds a subject to the deny list. Denied subjects get 403 even with valid tokens.
func (a *RelayAuthenticator) DenySub(sub string) {
	if a == nil {
		return
	}
	a.denyMu.Lock()
	defer a.denyMu.Unlock()
	a.denyList[sub] = struct{}{}
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
		userID := oa.GetUserIDFromAPIContext(r.Context())

		// If we got a userID, build RelayClaims
		if userID != "" {
			// Check deny list
			if a.isDenied(userID) {
				if a.config.OnRejected != nil {
					a.config.OnRejected(r, errDenied)
				}
				slog.Warn("Denied subject attempted connection", "component", "auth", "sub", userID)
				a.errorJSON(w, "forbidden", "access denied", http.StatusForbidden)
				return
			}

			claims := a.buildRelayClaims(r.Context(), userID)
			ctx := context.WithValue(r.Context(), contextKeyRelayClaims, claims)
			r = r.WithContext(ctx)

			if a.config.OnAuthenticated != nil {
				a.config.OnAuthenticated(r, claims)
			}
		}

		next.ServeHTTP(w, r)
	}))
}

// buildRelayClaims extracts RelayClaims from oneauth's context values.
func (a *RelayAuthenticator) buildRelayClaims(ctx context.Context, userID string) *RelayClaims {
	claims := &RelayClaims{
		Subject: userID,
		Scopes:  oa.GetScopesFromAPIContext(ctx),
	}

	custom := oa.GetCustomClaimsFromContext(ctx)
	if custom != nil {
		if v, ok := custom["client_id"].(string); ok {
			claims.ClientID = v
		}
		if v, ok := custom["max_rooms"].(float64); ok {
			claims.MaxRooms = int(v)
		}
		if v, ok := custom["max_msg_rate"].(float64); ok {
			claims.MaxMsgRate = v
		}
		if v, ok := custom["exp"].(float64); ok {
			claims.ExpiresAt = time.Unix(int64(v), 0)
		}
		if v, ok := custom["iat"].(float64); ok {
			claims.IssuedAt = time.Unix(int64(v), 0)
		}
	}

	return claims
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
