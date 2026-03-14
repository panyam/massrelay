# Massrelay — Project Instructions

## Build & Test

```bash
# Go tests
go test ./...

# TypeScript tests
cd ts && npm test

# All tests (pre-push hook runs this)
make tests
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

## Proto Regeneration

```bash
cd protos && buf generate
```

After regeneration, update both `gen/go/` and `ts/src/gen/`.

## Deployment Checklist

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
| `RELAY_LOG_PAYLOADS` | Log first N chars of payloads | 0 (off) |
| `RELAY_AUTH_REQUIRED` | Reject unauthenticated WebSocket connections | false |
| `RELAY_AUTH_ISSUER` | Expected JWT `iss` claim | (any) |
| `OTEL_SERVICE_NAME` | OTEL service identifier | massrelay |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector URL | (disabled) |
| `OTEL_METRICS_PROMETHEUS` | Serve `/metrics` for Prometheus | false |

### Namecheap DNS (for `add-relay-dns.sh`)
| Variable | Purpose |
|----------|---------|
| `NAMECHEAP_API_USER` | Namecheap username |
| `NAMECHEAP_API_KEY` | Namecheap API key |
| `NAMECHEAP_CLIENT_IP` | Your whitelisted IP (`curl -s4 ifconfig.me`) |

## Code Conventions

- **Structured logging**: Use `slog.Info`/`Warn`/`Error`/`Debug` with `"component"` key for Loki filtering
- **Middleware**: Zero app-specific imports in `web/middleware/` (designed for servicekit lift)
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
- WebSocket keepalive (30s ping, 5min pong) handled by servicekit — don't set `ReadTimeout`/`WriteTimeout` on `http.Server`

## Known Gotchas

- **AlmaLinux/Rocky**: Docker's `get.docker.com` doesn't support them — use CentOS repo directly
- **IONOS minimal images**: May not have `git` or `firewalld` pre-installed
- **Namecheap DNS API**: `setHosts` is replace-all — must send ALL existing records plus new ones
- **`docker compose up -d`**: Won't reload Caddy config if only the mounted file changed — use `docker compose restart caddy`
- **`make prod-up` before deploy**: Always test the full Caddy + relay stack locally to catch routing issues
