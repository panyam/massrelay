# Relay Auth Middleware — Implementation Plan

## Context

Massrelay currently has zero authentication. Any client that knows a relay URL can open WebSocket connections freely. We're adding JWT-based authentication to support a federated model where:

- **Hosts** (excaliframe.com, myexcal.com, native apps) register with the relay infrastructure and get client credentials
- **Users** OAuth with Google/GitHub on their host; the host exchanges user identity + its own credentials for a relay JWT
- **Relay servers** verify JWTs and enforce per-user + per-host rate limits
- **neauth** will be the shared Go library for auth logic, but doesn't exist yet — so we build the relay-side middleware with a clean interface that neauth can later provide

This PR implements **relay-side JWT verification + per-subject rate limiting** — the token minting and host registration endpoints will come later (in neauth or a follow-up).

## Files to Modify/Create

| File | Action |
|------|--------|
| `web/middleware/auth.go` | **Create** — JWT authenticator + claims context |
| `web/middleware/auth_test.go` | **Create** — Comprehensive tests |
| `web/middleware/guard.go` | **Modify** — Add `Auth` field |
| `web/middleware/ratelimit.go` | **Modify** — Add per-subject rate limiting using claims |
| `web/middleware/ratelimit_test.go` | **Modify** — Add per-subject rate limit tests |
| `web/server/app.go` | **Modify** — Read auth env vars, create authenticator |
| `go.mod` | **Modify** — Add `github.com/golang-jwt/jwt/v5` dependency |

## Step 1: Add JWT dependency

```bash
go get github.com/golang-jwt/jwt/v5
```

## Step 2: Create `web/middleware/auth.go`

### Claims type and context helpers

```go
type contextKey string
const claimsKey contextKey = "relay_claims"

// RelayClaims represents the verified claims from a relay JWT.
type RelayClaims struct {
    Subject      string  // opaque hash of user ID
    Issuer       string  // e.g. "relay-auth.massrelay.io"
    ClientID     string  // host's client_id
    ClientDomain string  // host's domain (e.g. "myexcal.com")
    ExpiresAt    time.Time
    IssuedAt     time.Time
    TokenID      string  // jti nonce
    MaxRooms     int     // from quota
    MaxMsgRate   float64 // from quota
}

func ClaimsFromContext(ctx context.Context) *RelayClaims
func ContextWithClaims(ctx context.Context, claims *RelayClaims) context.Context
```

### Config

```go
type AuthConfig struct {
    PublicKey  crypto.PublicKey // RSA or ECDSA public key — algorithm auto-detected from key type
    Issuer    string           // expected "iss" claim
    Required  bool             // if false, unauthenticated requests pass (no claims in context)
}
```

Algorithm detection: `*rsa.PublicKey` → RS256, `*ecdsa.PublicKey` → ES256. Reject other key types at construction time.

### JWTAuthenticator struct

```go
type JWTAuthenticator struct {
    config   AuthConfig
    denyList map[string]bool // sub → banned
    mu       sync.RWMutex
    // Callbacks for metrics
    OnAuthenticated func()
    OnRejected      func(reason string)
}
```

Following existing patterns:
- `NewJWTAuthenticator(cfg AuthConfig) *JWTAuthenticator` — returns nil if no public key provided
- `Middleware(next http.Handler) http.Handler` — nil-safe, no-op if nil
- Token extraction: `Authorization: Bearer <token>` header first, then `?token=` query param (WebSocket compatibility)
- Deny-list methods: `DenySub(sub string)`, `AllowSub(sub string)`, `IsDenied(sub string) bool`

### Middleware flow

1. Extract token from header or query param
2. If no token and `Required=false`: pass through (no claims in context)
3. If no token and `Required=true`: 401
4. Parse JWT, verify signature with public key (RS256 or ES256 — auto-detected from key type)
5. Validate `exp` (not expired) and `iss` (matches config)
6. Check `sub` against deny-list
7. Build `RelayClaims`, inject into context via `ContextWithClaims`
8. Call next handler

### Error responses (matching existing pattern)

- No token + required: `401 {"error":"authentication required"}`
- Invalid/expired token: `401 {"error":"invalid token"}`
- Denied subject: `403 {"error":"access denied"}`

## Step 3: Modify `web/middleware/guard.go`

Add `Auth` field to Guard struct and insert into middleware chain:

```go
type Guard struct {
    Origin    *OriginChecker
    Conn      *ConnLimiter
    RateLimit *RateLimiter
    Auth      *JWTAuthenticator  // NEW
}

func (g *Guard) Wrap(h http.Handler) http.Handler {
    if g == nil { return h }
    h = g.Conn.Middleware(h)
    h = g.Auth.Middleware(h)      // NEW — after rate limit, before conn limit
    h = g.RateLimit.Middleware(h)
    h = g.Origin.Middleware(h)
    return h
}
```

Execution order: origin → rate limit → **auth** → conn limit → handler

Rationale: Rate limit runs before auth so unauthenticated flood doesn't burn CPU on JWT verification. Auth runs before conn limit so rejected tokens don't consume connection slots.

## Step 4: Modify `web/server/app.go`

Add env var reading in `NewRelayApp()`:

```go
// Auth configuration
// RELAY_AUTH_PUBLIC_KEY  — PEM-encoded RSA public key (base64 or raw)
// RELAY_AUTH_ISSUER      — expected JWT issuer claim (default: empty = skip issuer check)
// RELAY_AUTH_REQUIRED    — "true" to reject unauthenticated connections (default: "false")
```

Parse PEM public key, create `JWTAuthenticator`, wire to Guard and metrics:

```go
var auth *middleware.JWTAuthenticator
if v := os.Getenv("RELAY_AUTH_PUBLIC_KEY"); v != "" {
    pubKey := parsePublicKey(v) // PEM decode
    authCfg := middleware.AuthConfig{
        PublicKey: pubKey,
        Issuer:   os.Getenv("RELAY_AUTH_ISSUER"),
        Required: os.Getenv("RELAY_AUTH_REQUIRED") == "true",
    }
    auth = middleware.NewJWTAuthenticator(authCfg)
    slog.Info("JWT auth configured", "issuer", authCfg.Issuer, "required", authCfg.Required)
}

guard := &middleware.Guard{
    Origin:    originChecker,
    Conn:      connLimiter,
    RateLimit: rateLimiter,
    Auth:      auth,  // nil-safe if no key configured
}
```

Add a `parsePublicKey` helper in app.go that handles PEM-encoded public keys (RSA or ECDSA, optionally base64-wrapped for env var friendliness). Uses `x509.ParsePKIXPublicKey` which auto-detects the key type.

## Step 5: Create `web/middleware/auth_test.go`

Test cases (using RSA key pair generated in `TestMain` or test helper):

1. **Valid token** — accepted, claims in context
2. **Expired token** — 401
3. **Wrong issuer** — 401
4. **Wrong signing key** — 401 (signature mismatch)
5. **Malformed token** — 401
6. **Token from header** — `Authorization: Bearer <token>`
7. **Token from query param** — `?token=<token>`
8. **Header takes precedence** over query param
9. **No token + required=true** — 401
10. **No token + required=false** — passthrough, no claims in context
11. **Denied subject** — 403
12. **Nil authenticator** — passthrough (middleware is no-op)
13. **Claims extraction** — verify all fields (sub, client_id, quota, etc.) round-trip correctly
14. **Callback hooks** — OnAuthenticated/OnRejected called appropriately
15. **ES256 token** — valid ECDSA-signed token accepted
16. **ES256 wrong key** — ECDSA token with wrong key rejected

Follow existing test patterns: `httptest.NewRequest`, `httptest.NewRecorder`, table-driven.

## Environment Variables Summary

| Variable | Type | Default | Purpose |
|----------|------|---------|---------|
| `RELAY_AUTH_PUBLIC_KEY` | PEM string | (none — auth disabled) | RSA public key for JWT verification |
| `RELAY_AUTH_ISSUER` | string | (empty — skip check) | Expected JWT `iss` claim |
| `RELAY_AUTH_REQUIRED` | bool | false | Reject unauthenticated connections |

## Step 6: Extend `web/middleware/ratelimit.go` — Per-Subject Rate Limiting

Add per-subject (JWT `sub`) rate limiting alongside existing per-IP limits. When a request has claims in context (from auth middleware), use `sub` as an additional rate-limit key.

### Changes to RateLimitConfig

```go
type RateLimitConfig struct {
    GlobalPerSec float64
    PerIPPerSec  float64
    PerIPBurst   int
    PerSubPerSec float64       // NEW — max requests/sec per authenticated subject (0 = unlimited)
    PerSubBurst  int           // NEW — burst allowance per subject
    IPLimiterTTL time.Duration
}
```

Default `PerSubPerSec`: 10, `PerSubBurst`: 5 (more generous than per-IP since authenticated users are trusted more).

### Changes to RateLimiter

```go
type RateLimiter struct {
    Config        RateLimitConfig
    globalLimiter *rate.Limiter
    ipLimiters    map[string]*ipLimiterEntry
    subLimiters   map[string]*ipLimiterEntry  // NEW — reuse same entry struct, keyed by sub
    mu            sync.Mutex
    OnRejected    func()
}
```

- `getSubLimiter(sub string) *rate.Limiter` — same pattern as `getIPLimiter`
- `cleanupIPLimiters()` goroutine extended to also clean stale sub limiters
- `AllowSub(sub string) bool` — NEW method, checks per-sub limit only

### Changes to Middleware method

```go
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
    // ... existing IP check ...
    // NEW: if claims exist in context, also check per-sub limit
    if claims := ClaimsFromContext(r.Context()); claims != nil && rl.Config.PerSubPerSec > 0 {
        if !rl.getSubLimiter(claims.Subject).Allow() {
            // reject with 429
        }
    }
    next.ServeHTTP(w, r)
}
```

**Important**: Per-sub limiting only applies when the auth middleware has already run and injected claims. The middleware ordering (rate limit → auth → ...) means per-sub checking happens in the rate limiter's handler, but claims won't be set yet at that point.

**Solution**: Move the per-sub rate limit check into a separate middleware that runs AFTER auth in the guard chain. Or: have the rate limiter's `Middleware` only do per-IP, and add a `SubMiddleware(next http.Handler) http.Handler` method that does per-sub checks and runs after auth.

Revised guard order:
```
origin → per-IP rate limit → auth → per-sub rate limit → conn limit → handler
```

### Test additions to `ratelimit_test.go`

- Authenticated request with sub in context → per-sub limit enforced
- No claims in context → per-sub limit skipped (only IP limit applies)
- Per-sub and per-IP limits are independent (both must pass)

## What's NOT in This PR

- Token minting (`/auth/token` endpoint) — belongs in neauth or follow-up
- Host registration (`/auth/hosts/register`) — belongs in neauth or follow-up
- JWKS endpoint fetching — will replace `RELAY_AUTH_PUBLIC_KEY` env var later
- Per-host rate limiting — follow-up PR
- Deny-list persistence — in-memory only for now, loaded via future API

## Verification

1. `go build ./...` — compiles
2. `go test ./web/middleware/...` — all auth tests pass
3. `go test ./...` — full test suite passes (no regressions)
4. Manual: start relay without `RELAY_AUTH_PUBLIC_KEY` → auth disabled, existing behavior unchanged
5. Manual: start relay with `RELAY_AUTH_PUBLIC_KEY` + `RELAY_AUTH_REQUIRED=true` → WebSocket connections without valid JWT get 401
