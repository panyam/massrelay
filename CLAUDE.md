# Massrelay — Project Instructions

## Build & Test

```bash
# Go tests
go test ./...

# TypeScript tests
cd ts && npm test

# All tests (pre-push hook runs this)
make tests

# TS client against the real relay binary (Go + Node 22+)
make test-interop

# Mission exercises against the relay binary
make exercise-auth
make exercise-reconnect
make exercise-token-refresh
make exercise-revoke

# All of them through the prod-up stack (Caddy, TLS, relay container, auth from
# env). Needs Docker and free ports 80/443; replaces a running prod-up stack.
make exercise-prod
```

## Local Development

```bash
# Relay + Grafana LGTM (metrics, logs, dashboards)
make dev-up          # start
make dev-logs        # tail relay logs
make dev-rebuild     # rebuild relay image only
make dev-down        # stop

# Native relay → dev Grafana
make run-otel

# Full production stack locally (Caddy + relay, self-signed cert)
make prod-up         # start
make prod-down       # stop
make prod-logs       # tail logs
```

## Releases

The Go module and `@panyam/massrelay` share one version, under `## Version` in CAPABILITIES.md. A release bumps it there, in `ts/package.json` and in `ts/package-lock.json`; `scripts/check-versions.sh` fails CI when they drift. Pushing tag `vX.Y.Z` runs `publish-npm.yml`, which tests and then calls servicekit's shared `npm-publish.yml` (npm trusted publishing, no token). Manual fallback: `cd ts && rm -rf dist && npm run build && npm run check:pack && npm publish --access public --otp=<code>`.

Before tagging, ask whether a client one release back still works against this relay. A new event field broke v0.3.3 clients until #71 caught it before release (see Known Gotchas). `ts/interop/compat.interop.ts` checks the published v0.3.3 client in CI. A release that adds wire fields gets a `docs/releases/vX.Y.Z.md` write-up, which is also the GitHub Release body. A small patch reuses its CHANGELOG entry instead. Additive features have gone out as patch bumps (v0.3.4 to v0.3.6).

## Proto Regeneration

```bash
cd protos && buf generate
```

After regeneration, update both `gen/go/` and `ts/src/gen/`.

## Deployment Checklist

### Releasing the TS package
Bump `ts/package.json` to the release version before tagging. Pushing a `v*` tag runs
`.github/workflows/publish-npm.yml`, which refuses a tag that doesn't match `ts/package.json`, runs
the TS and interop tests, and publishes `@panyam/massrelay` (skipping a version already on npm).
It uses npm trusted publishing, so no token secret. For a tag pushed before the workflow existed, run it by hand
with that tag as the ref. Until 2026-10 nothing published automatically, so npm sat at 0.1.1 while
the Go side reached v0.2.1, and mcpkit's room page client couldn't depend on the addressed
`sendAppMessage` that 0.2.x added.

### Adding new env vars
1. Add to `web/server/app.go` `NewRelayApp()` with `os.Getenv`
2. Add to `deploy/production/docker-compose.yml` environment block
3. Add to `deploy/production/.env.example` with documentation
4. Document in `README.md` Server Configuration section

**Forgetting step 2 means the env var never reaches the container.**

### Adding new HTTP route prefixes
1. Add the handler/route in `web/server/api.go`
2. Add `reverse_proxy /<prefix>/* relay:8787` to `deploy/production/Caddyfile`
3. Test locally with `make prod-up` before deploying

**Caddy returns empty 200 for unmatched routes — easy to miss.**

### Deploying to a relay
```bash
ssh root@<ip> "cd /opt/massrelay && git pull && cd deploy/production && docker compose up -d --build"
```

For Caddyfile-only changes, `docker compose restart caddy` (not just `up -d`).

### Adding a new relay to the pool
```bash
# 1. DNS (requires NAMECHEAP_API_USER, NAMECHEAP_API_KEY, NAMECHEAP_CLIENT_IP)
./deploy/scripts/add-relay-dns.sh relay02 <VPS_IP>

# 2. Bootstrap (SSH key + harden + Docker + firewall + deploy)
./deploy/scripts/setup-host.sh <VPS_IP> relay02.excaliframe.com
```

## Environment Variables

### Relay server
| Variable | Purpose | Default |
|----------|---------|---------|
| `RELAY_ADMIN_TOKEN` | Bearer token for `/admin/*` endpoints | (disabled) |
| `RELAY_ALLOWED_ORIGINS` | Comma-separated origin allowlist | (allow all) |
| `RELAY_TRUSTED_PROXIES` | Comma-separated trusted proxy CIDRs | (trust all) |
| `RELAY_MAX_CONNECTIONS` | Max concurrent WebSocket connections | 500 |
| `RELAY_GLOBAL_RATE` | Global connections/sec | 100 |
| `RELAY_PER_IP_RATE` | Per-IP connections/sec | 5 |
| `RELAY_PER_IP_BURST` | Connections one IP may open at once | max(3, rate) |
| `RELAY_PER_SUB_RATE` | Per-subject connections/sec (requires auth) | 0 (disabled) |
| `RELAY_LOG_PAYLOADS` | Log first N chars of payloads | 0 (off) |
| `RELAY_AUTH_REQUIRED` | Reject unauthenticated WebSocket connections | false |
| `RELAY_AUTH_ISSUER` | Expected JWT `iss` claim | (any) |
| `RELAY_AUTH_KEYS_FILE` | JSON array of `{client_id, alg, key}` signing keys | (none) |
| `RELAY_AUTH_JWKS_URL` | JWKS endpoint for signing keys (instead of a keys file) | (none) |
| `RELAY_RESOURCE_URL` | Resource server base URL for RFC 9728 PRM | (disabled) |
| `RELAY_AUTH_SERVERS` | Comma-separated trusted auth server URLs for PRM | (disabled) |
| `OTEL_SERVICE_NAME` | OTEL service identifier | massrelay |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector URL | (disabled) |
| `OTEL_METRICS_PROMETHEUS` | Serve `/metrics` for Prometheus | false |

### Namecheap DNS (for `add-relay-dns.sh`)
| Variable | Purpose |
|----------|---------|
| `NAMECHEAP_API_USER` | Namecheap username |
| `NAMECHEAP_API_KEY` | Namecheap API key |
| `NAMECHEAP_CLIENT_IP` | Your whitelisted IP (`curl -s4 ifconfig.me`) |

## Issues and missions

No active mission, by choice: massrelay waits until mcpkit or a deployment turns up the next gap. #72 (`make exercise-revoke`, an admin deny ends a user's open connections) closed on 2026-11-08. Earlier: #68 (`make exercise-prod`, the three exercises through the `make prod-up` stack) closed on 2026-11-08, #66 (`make exercise-token-refresh`) the same day, #53 (`make exercise-auth`) on 2026-10-25 and #54 (`make exercise-reconnect`) on 2026-10-24. All four exercises run in CI. `~/.claude/skills/retriage/queue.sh` prints the queue. A new issue gets a `P0`–`P3` label when it's filed, plus either a mission link (blocked-by, and the `mission_<slug>` label) or `waiting` with its trigger in the body.

## Code Conventions

- **Structured logging**: Use `slog.Info`/`Warn`/`Error`/`Debug` with `"component"` key for Loki filtering
- **Middleware**: Generic middleware (rate limit, origin, CORS, recovery, logging, conn limit, Guard) lives in `servicekit/middleware`. Relay-specific middleware (auth, guard composition) stays in `web/middleware/`.
- **ESM**: TypeScript package is `"type": "module"` — relative imports must use `.js` extensions
- **Proto JSON**: Wire format uses camelCase field names (protobuf JSON mapping)
- **Tests**: Go tests split by concern (service_core, broadcast, owner, query, limits_encryption, log_payloads, room). TS tests have `@flow`/`@browser`/`@e2e` JSDoc tags.

## Architecture Notes

- Relay is stateless — rooms exist only in memory, no database
- `CollabService` core has no HTTP/OTEL dependencies; instrumentation via callbacks
- `Guard` composes origin check → rate limit → auth → connection limit (wraps WebSocket handler only)
- CORS middleware reuses same `OriginChecker` as WebSocket guard
- Admin endpoints gated by bearer token with constant-time comparison
- **Revocation**: `DenySub` calls its `OnDeny` listeners; `api.go` registers the stream registry's `endSubject` (`web/server/registry.go`), which ends each of the subject's streams with `SessionEnded{denied}` in its own goroutine, so a deny call never waits on a stuck client. The deny list is in memory only, like rooms
- Caddy handles TLS (auto Let's Encrypt) and reverse-proxies to relay on port 8787
- WebSocket keepalive (30s ping, 5min pong) handled by servicekit — don't set `ReadTimeout`/`WriteTimeout` on `http.Server`. The per-write timeout (`StreamConfig.WriteTimeout`, 10s) and `CloseOnStreamEnd` go on the grpcws handler in `web/server/api.go` instead
- **Token lifetime**: a stream ends at its token's `exp` (`SessionEnded{token_expired}`, via the same `endSession` as a slow-consumer eviction). A `ReAuthAction` moves that timer to a fresh token's `exp`. The stream checks it with `StreamConfig.Auth`, which `api.go` takes from `Guard.Authenticator()`. Only a stream with that set fills `RoomJoined.token_expires_at`, and `CollabClient` refreshes only when it's there, since an older relay's protojson rejects an unknown action. `CollabClient` reconnects on `token_expired` only with `getToken`, and stops after two lifetimes in a row whose refresh failed. Every rejoin sends `JoinRoom.must_exist` so the relay answers `ROOM_NOT_FOUND` rather than recreating a lost room
- **Config errors surface from `Init`**, not `NewRelayApp` (whose signature mcpkit depends on): `ErrAuthRequiredNoKeys`, a bad keys file, an unreachable JWKS URL. Call `RelayApp.Close()` after serving to stop the JWKS refresh
- **Graceful shutdown** uses `servicekit/http.ListenAndServeGraceful` — no manual `signal.Notify`/`srv.Shutdown` boilerplate needed
- **oneauth surface is deliberately thin**: massrelay imports only `oneauth/apiauth` (the JWT-validating `APIMiddleware`, its `ValidateBearer` for a bare token, and `GetTokenInfoFromAPIContext`) and `oneauth/keys` (`KeyLookup`/`InMemoryKeyStore` for per-`client_id` key resolution). It does not use oneauth's OAuth-server surface. `web/middleware/auth.go` wraps these into `RelayAuthenticator`; `guard.go` composes it into the WebSocket Guard chain. Because massrelay rides oneauth's larger module, oneauth's periodic API refactors (e.g. the 0.0.x→0.1.x `apiauth`/`keys` rewrite) surface here as breaking changes — see Known Gotchas. `relayClaims` builds `RelayClaims` from oneauth's `TokenInfo` for both the upgrade (`Middleware`) and ReAuth (`ValidateToken`, via `ValidateBearer`), including `exp`/`iat` (oneauth v0.3.1, panyam/oneauth#456). It passes no `ConfirmBinding`, so a DPoP-bound token is refused on ReAuth. oneauth's `ConfirmBindingMatches` needs the key a DPoP proof established on the upgrade, and the relay never checks one there (browsers can't send DPoP headers on a WebSocket upgrade).

## Known Gotchas

- **AlmaLinux/Rocky**: Docker's `get.docker.com` doesn't support them — use CentOS repo directly
- **IONOS minimal images**: May not have `git` or `firewalld` pre-installed
- **Namecheap DNS API**: `setHosts` is replace-all — must send ALL existing records plus new ones
- **`docker compose up -d`**: Won't reload Caddy config if only the mounted file changed — use `docker compose restart caddy`
- **`make prod-up` before deploy**: Always test the full Caddy + relay stack locally to catch routing issues
- **WebSocket upgrades need a hijackable writer all the way down**: any middleware wrapping `http.ResponseWriter` in front of `/ws/` must implement `http.Hijacker`, since gorilla/websocket asserts it directly. servicekit's `RequestLogger` didn't until v0.1.7, which broke every upgrade in the relay binary from v0.1.0 to v0.2.0. Handler-level Go tests can't see this; `TestRelayHandlerAcceptsWebSocket` and `make test-interop` (real binary) can.
- **A paused Node socket never sees the peer hang up**: `socket.pause()` stops reading, so the FIN behind the unread bytes never arrives and `close` doesn't fire. `ts/interop/slow-consumer.interop.ts` stalls the relay that way, then reads and discards to observe whether the relay already dropped the connection.
- **`check:pack` fails locally on a stale `dist/`**: `tsc` never deletes old output, so test files compiled before `tsconfig.build.json` existed linger. `rm -rf dist` before `npm run build`. CI checks out clean.
- **npm 12 changed `npm pack --json`** from an array to an object keyed by package name. `scripts/check-pack.mjs` and servicekit's shared workflow accept both; anything else parsing it must too.
- **`npm trust` needs a newer npm than Node 22 ships**: run `npx npm@latest trust github <pkg> --repo panyam/<repo> --file publish-npm.yml --allow-publish`.
- **Releases: never chain `gh release create` after a check with `;`**. A failed check once let the release tag the wrong commit. Use `set -e` / `&&`.
- **npm lags the publish by about a minute**: after `publish-npm.yml` succeeds (its log shows `+ @panyam/massrelay@X.Y.Z`), `npm view @panyam/massrelay@X.Y.Z --prefer-online` still 404s for a while. Poll rather than re-running the workflow.
- **mcpkit tests its room bridge against an in-process relay, not the binary**: its `experimental/ext/rooms/massrelay/internal/relaytest` builds `RelayApp` with a hand-built `Guard` and key store. The binary's env-loaded auth path (identity, addressed calls) is covered here by `ts/interop/identity.interop.ts`. mcpkit's Go side pins an exact massrelay version, so each release needs a bump there; its npm side takes a caret range.
- **oneauth's custom claims leave out `exp`/`iat`** (and every other standard claim). Read them from `TokenInfo.ExpiresAt`/`IssuedAt` (oneauth v0.3.1+). `RelayClaims.ExpiresAt` was silently zero until #62. Read the client from `TokenInfo.ClientID`, not `CustomClaims["client_id"]`: since v0.3.2 it falls back to `azp`, which is where Keycloak user tokens name their client.
- **`keys.JWKSKeyStore.Stop()` panics if called twice** (it closes a channel). `RelayApp.Close` guards it with a `sync.Once`.
- **`buf generate` needs `protoc-gen-go-grpc` v1.5.1 and `protoc-gen-es` v2.9.0 on PATH**, the versions the checked-in code was generated with. Without them it fails with `context canceled`. Install them somewhere temporary (`GOBIN=... go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1`, `npm install @bufbuild/protoc-gen-es@2.9.0`) so the diff stays to your proto change.
- **A Go stream test must drain every stream it writes to.** `CollabBidiStream.reply` blocks on a full 64-slot queue, which servicekit drains in production but a test doesn't. `TestStreamEvictsSlowConsumer` hung CI that way from #44 to #62 (`go test -cpu 1 -count=200` reproduced it).
- **Exercises take an external relay**: with `MASSRELAY_RELAY_URL` set, `relayForExercise` in `ts/interop/relay.ts` uses that relay instead of building the binary, and `restart()` runs `MASSRELAY_RELAY_RESTART`. It ignores the env an exercise asks for, so `scripts/exercise-prod.sh` starts the stack with the one config all three need (auth required, `exercise-app`/`exercise-issuer`). Unlike `startRelay`, it leaves the rate limits at the stack's values. Node trusts Caddy's local CA through `NODE_EXTRA_CA_CERTS`, and the stall proxy forwards TLS bytes, so `relayCloses` sees nothing on `wss://`.
- **The relay orders messages per sender only.** A test that sends from two peers at once and asserts the receive order flakes. `identity.interop.ts` failed 2 of 20 runs on main that way until #67 made it wait for the first message before sending the second.
- **Unknown JSON fields break both directions.** An older relay's protojson rejects an unknown `CollabAction` field. A `CollabClient` up to v0.3.3 rejects an unknown field anywhere in a `CollabEvent` (protobuf-es `fromJson` without `ignoreUnknownFields`), so it never sees that event. A new action waits for something the relay advertises (ReAuth waits for `RoomJoined.token_expires_at`). A new event field goes only to a join whose `protocol_version` says the client knows it (3 for `token_expires_at`). `ts/interop/compat.interop.ts` runs the published v0.3.3 client against the binary to catch this.
- **`npm install` with npm 10 strips the lockfile's `libc` fields.** Use `npx npm@latest install` so the `package-lock.json` diff is only your change.
- **The keys file is mounted as a directory** (`deploy/production/secrets` → `/etc/massrelay`) because docker turns a missing single-file mount into an empty directory.
- **oneauth 0.0.x → 0.1.x API migration** (done at massrelay v0.1.0): `apiauth.APIMiddleware.TokenQueryParam` → `LegacyQueryParamBearer`; `apiauth.GetUserIDFromAPIContext` → `GetSubjectFromAPIContext`; `keys.InMemoryKeyStore.RegisterKey(id, key, alg)` → request/response `PutKey(ctx, &keys.PutKeyRequest{Record: &keys.KeyRecord{ClientID, Key, Algorithm}})`. oneauth v0.3.0 through v0.3.2 require **Go 1.26.9+** (v0.1.36 needed 1.26.4+).
