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
It needs the `NPM_TOKEN` repo secret. For a tag pushed before the workflow existed, run it by hand
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

Two missions are active: #53 (auth works on a deployed relay binary, `mission_deployed_auth`) and #54 (a client rejoins on its own after an eviction or relay restart, `mission_resilient_client`). `~/.claude/skills/retriage/queue.sh` prints the queue. A new issue gets a `P0`–`P3` label when it's filed, plus either a mission link (blocked-by, and the `mission_<slug>` label) or `waiting` with its trigger in the body.

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
- Caddy handles TLS (auto Let's Encrypt) and reverse-proxies to relay on port 8787
- WebSocket keepalive (30s ping, 5min pong) handled by servicekit — don't set `ReadTimeout`/`WriteTimeout` on `http.Server`. The per-write timeout (`StreamConfig.WriteTimeout`, 10s) and `CloseOnStreamEnd` go on the grpcws handler in `web/server/api.go` instead
- **Graceful shutdown** uses `servicekit/http.ListenAndServeGraceful` — no manual `signal.Notify`/`srv.Shutdown` boilerplate needed
- **oneauth surface is deliberately thin**: massrelay imports only `oneauth/apiauth` (the JWT-validating `APIMiddleware` + context accessors `GetSubjectFromAPIContext`/`GetScopesFromAPIContext`/`GetCustomClaimsFromContext`) and `oneauth/keys` (`KeyLookup`/`InMemoryKeyStore` for per-`client_id` key resolution). It does not use oneauth's OAuth-server surface. `web/middleware/auth.go` wraps these into `RelayAuthenticator`; `guard.go` composes it into the WebSocket Guard chain. Because massrelay rides oneauth's larger module, oneauth's periodic API refactors (e.g. the 0.0.x→0.1.x `apiauth`/`keys` rewrite) surface here as breaking changes — see Known Gotchas.

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
- **oneauth 0.0.x → 0.1.x API migration** (done at massrelay v0.1.0): `apiauth.APIMiddleware.TokenQueryParam` → `LegacyQueryParamBearer`; `apiauth.GetUserIDFromAPIContext` → `GetSubjectFromAPIContext`; `keys.InMemoryKeyStore.RegisterKey(id, key, alg)` → request/response `PutKey(ctx, &keys.PutKeyRequest{Record: &keys.KeyRecord{ClientID, Key, Algorithm}})`. oneauth v0.3.0 requires **Go 1.26.9+** (v0.1.36 needed 1.26.4+).
