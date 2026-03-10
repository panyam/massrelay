# Relay Auth — Federated Authentication Plan

## Overview

Massrelay currently has zero authentication. We're adding a federated JWT-based auth system where **oneauth** (`github.com/panyam/oneauth`) is embedded in both Hosts and Relays as the shared auth library.

## Three Personas

1. **Users** — Work locally (IndexedDB, native storage, CLI). No login required for local work. When collaborating, they authenticate via their Host of choice.
2. **Hosts** (excaliframe.com, myexcal.com, native apps, CLI tools) — The app surface where users create/edit documents. Hosts handle user authentication (OAuth2, password, etc.). Each Host registers with Relays and gets client credentials.
3. **Relays** (massrelay) — Stateless WebSocket transport. Verify JWTs, enforce quotas, know nothing about documents. Could also serve multiplayer games or any real-time sync use case.

## Trust Chain

```
Host registers with Relay
  → gets client_id + shared_secret (HS256)
  → or registers public key (RS256/ES256, future)

User logs into Host
  → Host authenticates user (OAuth2, password, etc. via oneauth)
  → Host mints relay-scoped JWT (signed with shared_secret)
  → JWT contains: sub (user), client_id (host), client_domain, quotas

User connects to Relay with JWT
  → Relay extracts client_id from JWT claims
  → Relay looks up signing key via KeyStore (oneauth)
  → Relay verifies signature, checks expiry/issuer/deny-list
  → Relay enforces quotas from claims
```

## oneauth's Role (Embedded in Both)

### On the Host side
- User authentication (OAuth2, local password — already supported)
- Session management, refresh tokens (already supported)
- **New**: `CustomClaimsFunc` to inject relay-specific claims (client_id, quotas) into JWTs
- **New**: `MintRelayToken` convenience function

### On the Relay side
- **New**: `KeyStore` interface for multi-tenant key lookup (by client_id)
- **New**: Multi-tenant `validateJWT` (replaces single `JWTSecretKey`)
- **New**: `HostStore` for managing Host registrations
- **New**: `HostStore`-backed `KeyStore` adapter
- Existing `APIMiddleware` extended to use `KeyStore`

## Signing Algorithm Strategy

### Phase 1: HS256 (Symmetric) — Now
- Each Host gets a `client_id` + `shared_secret` at registration
- Host signs JWTs with the shared secret
- Relay looks up the secret by `client_id` via `KeyStore.GetVerifyKey(clientID)`
- Simple, oneauth already supports HS256

### Phase 2: RS256/ES256 (Asymmetric) — Compatible Extension
- Host generates key pair, registers public key with Relay
- Host signs JWTs with private key
- Relay verifies with public key from `KeyStore`
- `KeyStore.GetVerifyKey` returns `crypto.PublicKey` instead of `[]byte`
- `KeyStore.GetExpectedAlg` prevents algorithm confusion attacks
- Both HS256 and asymmetric hosts coexist — per-host algorithm choice

### Phase 3: JWKS — Future
- Hosts publish JWKS endpoints
- Relay fetches and caches keys periodically
- Just a new `KeyStore` implementation — no interface changes

## Token Refresh Strategy

### Now: Connection-Time Only
- JWT presented at WebSocket connect (`Authorization: Bearer` header or `?token=` query param)
- If token expires mid-session, client disconnects and reconnects with fresh token
- Host refreshes the token via oneauth's `refresh_token` grant
- Works with existing auto-reconnect plans

### Future: In-Session Re-Auth (Backwards-Compatible)
- Add `ReAuthAction` to `CollabAction` oneof in proto
- Client sends new JWT over existing WebSocket before expiry
- Server validates and updates connection claims
- Old servers ignore unknown oneof — old clients just reconnect
- See panyam/massrelay#13

## Implementation — Relay Side (panyam/massrelay#12)

### Files to Modify/Create

| File | Action |
|------|--------|
| `web/middleware/auth.go` | **Create** — RelayAuthenticator wrapping oneauth |
| `web/middleware/auth_test.go` | **Create** — Comprehensive tests |
| `web/middleware/guard.go` | **Modify** — Add `Auth` field |
| `web/middleware/ratelimit.go` | **Modify** — Add per-subject rate limiting |
| `web/middleware/ratelimit_test.go` | **Modify** — Add per-subject rate limit tests |
| `web/server/app.go` | **Modify** — Wire oneauth KeyStore, read config |
| `go.mod` | **Modify** — Add `github.com/panyam/oneauth` dependency |

### Claims Type

```go
type RelayClaims struct {
    Subject      string    // opaque hash of user ID
    Issuer       string    // e.g. "relay-auth.massrelay.io"
    ClientID     string    // host's client_id
    ClientDomain string    // host's domain (e.g. "myexcal.com")
    ExpiresAt    time.Time
    IssuedAt     time.Time
    TokenID      string    // jti nonce
    MaxRooms     int       // from quota
    MaxMsgRate   float64   // from quota
}

func ClaimsFromContext(ctx context.Context) *RelayClaims
func ContextWithClaims(ctx context.Context, claims *RelayClaims) context.Context
```

### RelayAuthenticator

Wraps oneauth's `APIMiddleware` with relay-specific behavior:

```go
type AuthConfig struct {
    KeyStore  oneauth.KeyStore // multi-tenant key lookup
    Issuer    string           // expected "iss" claim
    Required  bool             // if false, unauthenticated requests pass
}

type RelayAuthenticator struct {
    config   AuthConfig
    denyList map[string]bool // sub → banned
    mu       sync.RWMutex
    OnAuthenticated func()
    OnRejected      func(reason string)
}
```

- `NewRelayAuthenticator(cfg AuthConfig) *RelayAuthenticator` — returns nil if no KeyStore
- `Middleware(next http.Handler) http.Handler` — nil-safe, no-op if nil
- Token extraction: `Authorization: Bearer <token>` header first, then `?token=` query param
- Deny-list methods: `DenySub(sub)`, `AllowSub(sub)`, `IsDenied(sub)`

### Middleware Flow

1. Extract token from header or query param
2. If no token and `Required=false`: pass through (no claims in context)
3. If no token and `Required=true`: 401
4. Extract `client_id` from unverified claims
5. Look up signing key via `KeyStore.GetVerifyKey(clientID)`
6. Verify JWT signature (algorithm auto-detected from key type)
7. Validate `exp` (not expired) and `iss` (matches config)
8. Check `sub` against deny-list
9. Build `RelayClaims` from standard + custom claims
10. Inject into context via `ContextWithClaims`
11. Call next handler

### Error Responses

- No token + required: `401 {"error":"authentication required"}`
- Invalid/expired token: `401 {"error":"invalid token"}`
- Unknown client_id: `401 {"error":"unknown host"}`
- Denied subject: `403 {"error":"access denied"}`

### Guard Middleware Order

```go
type Guard struct {
    Origin    *OriginChecker
    Conn      *ConnLimiter
    RateLimit *RateLimiter
    Auth      *RelayAuthenticator  // NEW
}
```

Execution order:
```
origin → per-IP rate limit → auth → per-sub rate limit → conn limit → handler
```

Rationale: Per-IP rate limit before auth prevents unauthenticated flood from burning CPU on JWT verification. Auth before conn limit prevents rejected tokens from consuming connection slots. Per-sub rate limit after auth because it needs claims in context.

### Per-Subject Rate Limiting

Add per-subject (JWT `sub`) rate limiting alongside existing per-IP limits. When a request has claims in context (from auth middleware), use `sub` as an additional rate-limit key.

#### Changes to RateLimitConfig

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

#### Changes to RateLimiter

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

#### Middleware Ordering Subtlety

Per-sub limiting only applies when the auth middleware has already run and injected claims. The existing `Middleware` method does per-IP checks. Since the guard order is `per-IP rate limit → auth → per-sub rate limit`, the per-sub check must be in a separate middleware.

**Solution**: Add a `SubMiddleware(next http.Handler) http.Handler` method on `RateLimiter` that does per-sub checks. This runs after auth in the guard chain:

```go
func (rl *RateLimiter) SubMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if claims := ClaimsFromContext(r.Context()); claims != nil && rl.Config.PerSubPerSec > 0 {
            if !rl.getSubLimiter(claims.Subject).Allow() {
                // reject with 429
                return
            }
        }
        next.ServeHTTP(w, r)
    })
}
```

#### Per-Subject Rate Limit Tests

- Authenticated request with sub in context → per-sub limit enforced
- No claims in context → per-sub limit skipped (only IP limit applies)
- Per-sub and per-IP limits are independent (both must pass)
- Sub limiter cleanup removes stale entries

#### Future: Per-Host Rate Limiting

Per-host (`client_id`) rate limiting is a follow-up — would limit aggregate traffic from all users of a single Host.

### Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `RELAY_AUTH_ISSUER` | (empty — skip check) | Expected JWT `iss` claim |
| `RELAY_AUTH_REQUIRED` | false | Reject unauthenticated connections |
| `RELAY_HOST_STORE_PATH` | (none) | Path to filesystem HostStore |

### Tests

1. Valid token — accepted, claims in context
2. Expired token — 401
3. Wrong issuer — 401
4. Wrong signing key — 401
5. Malformed token — 401
6. Token from header — `Authorization: Bearer <token>`
7. Token from query param — `?token=<token>`
8. Header takes precedence over query param
9. No token + required=true — 401
10. No token + required=false — passthrough
11. Denied subject — 403
12. Nil authenticator — passthrough
13. Claims extraction — all fields round-trip
14. Callback hooks — OnAuthenticated/OnRejected called
15. Multi-tenant — tokens from different hosts verified with different keys
16. Unknown client_id — 401
17. Per-subject rate limiting with/without claims

## Implementation — oneauth Side

### panyam/oneauth#2 — CustomClaimsFunc + KeyStore ✅ DONE (PR panyam/oneauth#6)
- `CustomClaimsFunc` callback on `APIAuth`
- `KeyStore` interface (multi-tenant key lookup)
- `InMemoryKeyStore` implementation
- Multi-tenant `validateJWT` in `APIMiddleware`
- Algorithm confusion prevention
- `ValidateAccessTokenFull` for custom claims extraction
- Exported `CreateAccessToken`
- 16 new tests, all existing tests pass

### panyam/oneauth#3 — Host Registration API + Service Component
- `HostRegistration` type, `HostStore` interface
- In-memory and filesystem `HostStore` implementations
- `HostStore`-backed `KeyStore` adapter
- HTTP handlers for Host CRUD (`/api/hosts/*`)
- `MintRelayToken` convenience function
- Secret rotation support

### panyam/oneauth#4 — Asymmetric Signing (RS256/ES256)
- Asymmetric signing in `createAccessToken`
- Key pair generation helpers
- `HostRegistration` with public key storage
- Both HS256 and asymmetric coexist per-host

## Dependency Order

```
oneauth#2 (KeyStore + CustomClaims) ✅ DONE
    ↓
oneauth#3 (HostStore + service)  ←→  massrelay#12 (relay auth middleware)
    ↓
oneauth#4 (asymmetric signing)
    ↓
massrelay#13 (in-session re-auth)
```

oneauth#2 is complete (PR panyam/oneauth#6). oneauth#3 and massrelay#12 can now be developed in parallel (massrelay#12 uses InMemoryKeyStore for testing until oneauth#3 provides HostStore-backed KeyStore).

## Verification

1. `go build ./...` — compiles
2. `go test ./web/middleware/...` — all auth tests pass
3. `go test ./...` — full test suite passes (no regressions)
4. Manual: start relay without auth config → auth disabled, existing behavior unchanged
5. Manual: start relay with `RELAY_AUTH_REQUIRED=true` + HostStore → unauthenticated connections get 401
6. Manual: Host registers, mints token, user connects with token → authenticated connection
