# Massrelay Deployment Guide

This guide covers deploying massrelay relay servers — from local development to a production pool of VPS instances.

## Architecture Overview

```
                    ┌─────────────────────┐
                    │   relay-pool.json   │  (CDN / static file)
                    │   [r01, r02, ...]   │
                    └────────┬────────────┘
                             │
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
         ┌─────────┐   ┌─────────┐   ┌─────────┐
         │  VPS 1  │   │  VPS 2  │   │  VPS 3  │
         │ Caddy   │   │ Caddy   │   │ Caddy   │
         │ Relay   │   │ Relay   │   │ Relay   │
         └─────────┘   └─────────┘   └─────────┘
              │              │              │
              └──────────────┼──────────────┘
                             ▼
                     ┌───────────────┐
                     │ Grafana Cloud │  (optional, OTLP)
                     └───────────────┘
```

Each relay is a self-contained unit: **Caddy** (reverse proxy + auto-TLS) + **Relay** (Go WebSocket server). No coordination between relays. Clients pick a relay, the share code encodes the relay URL, followers connect directly.

## Quick Start

### Prerequisites

- Docker and Docker Compose
- A domain with DNS pointing to your VPS (for production TLS)

### Local Development

Run the relay + Grafana observability stack locally:

```bash
# From the massrelay repo root
make local-up        # builds relay image, starts relay + Grafana LGTM
make local-logs      # tail relay logs
make local-down      # stop everything
```

| Service | URL | Description |
|---------|-----|-------------|
| Relay health | http://localhost:8787/health | Relay status, room/peer counts |
| Relay metrics | http://localhost:8787/metrics | Prometheus scrape endpoint |
| Grafana | http://localhost:3000 | Dashboards (no login, auto-admin) |

To rebuild after code changes:

```bash
make local-rebuild   # rebuild relay image only, restart
```

### Native relay + Docker Grafana

If you prefer running the relay natively (faster iteration, debugger support):

```bash
make local-up        # start just the Grafana stack
make run-otel        # run relay natively with OTEL → local Grafana
```

## Production Deployment

### 1. Provision a VPS

Any cheap Linux VPS with:
- 1GB+ RAM (relay uses ~20-50MB, Caddy ~15MB, Docker ~100MB)
- Ports 80 and 443 open
- Docker installed

Recommended providers:
| Provider | Price | RAM | Notes |
|----------|-------|-----|-------|
| Hetzner CX22 | €3.29/mo | 2GB | Best value, no lock-in |
| IONOS VPS | ~$1-2/mo | 1GB | Year-one promo, renewal ~$6-8/mo |
| Oracle Cloud Free | $0/mo | 1GB | Always-free ARM tier |
| Racknerd | ~$11/yr | 1GB | Black Friday deals |

### 2. Install Docker

```bash
# Ubuntu/Debian
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
# Log out and back in
```

### 3. Clone and Configure

```bash
git clone https://github.com/panyam/massrelay.git
cd massrelay/deploy/production

# Create your .env from the example
cp .env.example .env
```

Edit `.env`:

```bash
# Required: your domain (DNS A record must point to this VPS)
RELAY_DOMAIN=r01.yourdomain.com

# Unique name for this relay instance
OTEL_SERVICE_NAME=massrelay-r01

# Optional: ship metrics to Grafana Cloud or your own collector
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-prod-us-east-0.grafana.net/otlp
```

### 4. Start

```bash
docker compose up -d
```

That's it. Caddy will:
- Automatically obtain a Let's Encrypt TLS certificate for your domain
- Serve the relay at `wss://r01.yourdomain.com/ws/v1/{session_id}/sync`
- Proxy `/health`, `/metrics`, `/api/*` endpoints
- Auto-renew the certificate (checks every 12h, renews ~30 days before expiry)

### 5. Verify

```bash
# Health check
curl https://r01.yourdomain.com/health

# Should return:
# {"status":"ok","uptime_seconds":42,"rooms":0,"peers":0,"goroutines":5}

# Prometheus metrics
curl https://r01.yourdomain.com/metrics

# Logs
docker compose logs -f relay
```

## Operations

### Updating the Relay

```bash
cd massrelay/deploy/production
git pull
docker compose up -d --build
```

The relay restarts with zero downtime for new connections. Active WebSocket sessions will disconnect and clients will reconnect.

### Monitoring

#### Health Endpoint

Every relay exposes `GET /health`:

```json
{
  "status": "ok",
  "uptime_seconds": 86400,
  "rooms": 12,
  "peers": 34,
  "goroutines": 89
}
```

#### Prometheus Metrics

Available at `/metrics`. Key metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `relay_rooms_active` | Gauge | Active rooms |
| `relay_peers_active` | Gauge | Connected peers |
| `relay_connections_total` | Counter | Total WebSocket connections |
| `relay_messages_total` | Counter | Messages relayed (by type) |
| `relay_joins_total` | Counter | Room joins |
| `relay_leaves_total` | Counter | Room leaves |
| `relay_rate_limited_total` | Counter | Rate-limited requests |
| `relay_messages_dropped` | Counter | Messages dropped (full channel) |

#### OTLP Export

Set `OTEL_EXPORTER_OTLP_ENDPOINT` in `.env` to ship metrics to any OTLP-compatible backend:

- **Grafana Cloud** (free tier: 10k metrics series): `https://otlp-gateway-prod-us-east-0.grafana.net/otlp`
- **Self-hosted Grafana LGTM**: your collector endpoint
- **Datadog, New Relic, etc.**: their OTLP intake endpoints

Metrics are pushed every 15 seconds.

### Scaling the Pool

Adding a new relay to the pool:

1. Provision a VPS
2. Point a DNS record to it (e.g. `r02.yourdomain.com`)
3. Clone, configure `.env`, `docker compose up -d`
4. Add the relay URL to your `relay-pool.json`

Removing a relay:

1. Remove from `relay-pool.json` (new sessions won't use it)
2. Wait for active sessions to end (or just stop it — clients will reconnect elsewhere)
3. `docker compose down` and decommission the VPS

### Automation Scripts

Two scripts handle the full lifecycle of relay hosts. Both read from `deploy/inventory.txt`.

#### `setup-host.sh` — Bootstrap a new VPS

```bash
# One command to go from bare VPS to running relay
./deploy/scripts/setup-host.sh <ip> <domain> [service-name]

# Example
./deploy/scripts/setup-host.sh 49.12.1.2 r01.relay.excaliframe.com massrelay-r01
```

What it does:
1. Verifies DNS A record points to the IP
2. SSHs into the host and installs Docker
3. Configures firewall (ufw or firewalld)
4. Clones the massrelay repo to `/opt/massrelay`
5. Writes `.env` with domain and service name
6. Starts Caddy + relay via `docker compose up -d`

Works on Ubuntu 20.04+, Debian 11+, CentOS/RHEL 8+, Fedora, Amazon Linux 2. Provider-agnostic — same script for IONOS, Hetzner, Oracle, Racknerd, etc.

#### `update-pool.sh` — Manage the pool

```bash
# Rolling update all hosts in inventory.txt
./deploy/scripts/update-pool.sh

# Update specific hosts only
./deploy/scripts/update-pool.sh r01.relay.excaliframe.com r02.relay.excaliframe.com

# Health check all hosts
./deploy/scripts/update-pool.sh --health
# HOST                                     STATUS
# ----                                     ------
# r01.relay.excaliframe.com                ✓ up 86400s
# r02.relay.excaliframe.com                ✓ up 43200s
# r03.relay.excaliframe.com                ✗ unreachable

# Pool status (rooms, peers, uptime)
./deploy/scripts/update-pool.sh --status
# HOST                                      ROOMS  PEERS     UPTIME STATUS
# ----                                      -----  -----     ------ ------
# r01.relay.excaliframe.com                     3      8     1d 0h ✓
# r02.relay.excaliframe.com                     1      2     0d 12h ✓
#
# Total: 4 rooms, 10 peers
```

#### `inventory.txt` — Host registry

```
# Format: <domain>  <provider>  <region>  <ip>  <monthly-cost>
r01.relay.excaliframe.com    hetzner     eu-central    49.12.x.x      €3.29
r02.relay.excaliframe.com    ionos       us-east       85.215.x.x     $1.00
r03.relay.excaliframe.com    oracle      ap-south      132.145.x.x    $0.00
```

Also serves as the source for generating `relay-pool.json`:

```bash
awk '/^r[0-9]/ {print "{\"url\":\"wss://"$1"\",\"region\":\""$3"\"}"}' deploy/inventory.txt \
  | jq -s '{version: 1, relays: .}' > relay-pool.json
```

#### Scaling to 20+ hosts: Ansible

When the pool grows beyond what shell scripts comfortably manage, the same operations translate directly to Ansible:

```yaml
# ansible playbook sketch — same commands, parallel + idempotent
- hosts: relays
  tasks:
    - name: Update relay
      shell: cd /opt/massrelay/deploy/production && git pull && docker compose up -d --build
```

## DNS & Host Onboarding

### DNS Strategy

Use a single domain with numbered subdomains for all relays:

```
relay.yourdomain.com        ← not used directly (could serve relay-pool.json)
r01.relay.yourdomain.com    ← Hetzner, Frankfurt
r02.relay.yourdomain.com    ← IONOS, US East
r03.relay.yourdomain.com    ← Oracle Free, Mumbai
...
```

Each relay gets an **A record** pointing to its VPS IP. Using subdomains of a single domain keeps everything under one DNS zone and one Let's Encrypt rate limit scope.

### Adding a New Host

1. **Provision the VPS** at any provider (Hetzner, IONOS, Oracle, etc.)

2. **Create DNS record:**
   ```bash
   # Example with Cloudflare CLI (or use your DNS provider's UI)
   # IMPORTANT: DNS-only mode (no Cloudflare proxy) — Caddy needs direct access for TLS
   cf dns create r04.relay.yourdomain.com A <VPS_IP> --proxy=false

   # Or with Google Cloud DNS
   gcloud dns record-sets create r04.relay.yourdomain.com \
     --zone=yourdomain --type=A --rrdatas=<VPS_IP> --ttl=300
   ```

   Verify propagation:
   ```bash
   dig +short r04.relay.yourdomain.com
   # Should return the VPS IP
   ```

3. **Bootstrap the VPS:**
   ```bash
   ssh root@<VPS_IP> << 'SETUP'
   # Install Docker
   curl -fsSL https://get.docker.com | sh

   # Clone and configure
   git clone https://github.com/panyam/massrelay.git
   cd massrelay/deploy/production
   cp .env.example .env

   # Edit .env — set domain and service name
   sed -i 's/RELAY_DOMAIN=.*/RELAY_DOMAIN=r04.relay.yourdomain.com/' .env
   sed -i 's/OTEL_SERVICE_NAME=.*/OTEL_SERVICE_NAME=massrelay-r04/' .env

   # Start
   docker compose up -d
   SETUP
   ```

4. **Verify:**
   ```bash
   curl https://r04.relay.yourdomain.com/health
   ```

5. **Add to relay pool:**
   Update `relay-pool.json` (on CDN or static host):
   ```json
   {"url": "wss://r04.relay.yourdomain.com", "region": "eu-west"}
   ```

### Removing a Host

1. Remove from `relay-pool.json` (clients stop selecting it for new sessions)
2. Active sessions drain naturally (or instant — clients reconnect elsewhere)
3. SSH in and stop: `docker compose down`
4. Delete DNS record
5. Decommission VPS

### Host Inventory

Maintain a simple inventory file (e.g. `deploy/inventory.txt`) to track your pool:

```
# host                              provider    region     ip              monthly
r01.relay.yourdomain.com            hetzner     eu-central 49.12.x.x       €3.29
r02.relay.yourdomain.com            ionos       us-east    85.215.x.x      $1.00
r03.relay.yourdomain.com            oracle      ap-south   132.145.x.x     $0.00
```

This doubles as the source for generating `relay-pool.json`:

```bash
# Generate relay-pool.json from inventory
awk '/^r[0-9]/ {print "{\"url\":\"wss://"$1"\",\"region\":\""$3"\"}"}' deploy/inventory.txt \
  | jq -s '{version: 1, relays: .}' > relay-pool.json
```

### DNS Provider Recommendations

| Provider | Why | Notes |
|----------|-----|-------|
| **Cloudflare** (free) | Fast propagation, API, free | Set DNS-only (gray cloud) — don't proxy WebSockets through CF unless you're on a paid plan |
| **Your domain registrar** | Simplest if you have few relays | Usually no API, manual UI |
| **Route 53 / Cloud DNS** | If already on AWS/GCP | API-friendly, costs ~$0.50/zone/mo |

**Important:** Whichever DNS provider you use, do NOT proxy WebSocket traffic through a CDN (Cloudflare orange cloud, etc.) unless the CDN plan supports WebSockets. Let Caddy handle TLS directly.

## TLS / Certificates

Caddy handles TLS automatically:

- **First request**: obtains a cert from Let's Encrypt via HTTP-01 challenge
- **Storage**: certs stored in the `caddy_data` Docker volume (persists across restarts)
- **Renewal**: automatic, ~30 days before expiry, checked every 12 hours
- **No cron jobs**: unlike certbot + nginx, there's nothing to maintain
- **Rate limits**: Let's Encrypt allows 50 certs/week/domain — plenty for a relay pool

For local/dev with `RELAY_DOMAIN=localhost`, Caddy serves a self-signed cert.

## Security Notes

- Relay servers are **dumb encrypted pipes** — with E2EE enabled, they never see plaintext
- No authentication on the relay — session IDs are the only access control
- `/metrics` is currently unauthenticated — consider firewall rules or Caddy basic auth for production
- Rate limiting is built in: 100 connections/sec global, 5/sec per IP, 30 msgs/sec per client

## Troubleshooting

**Caddy can't get a certificate:**
- Verify DNS A record points to the VPS IP: `dig r01.yourdomain.com`
- Ensure ports 80 and 443 are open: `sudo ufw allow 80,443/tcp`
- Check Caddy logs: `docker compose logs caddy`

**Relay not accepting WebSocket connections:**
- Check relay health: `curl http://localhost:8787/health`
- Check relay logs: `docker compose logs relay`
- Verify Caddy is proxying: `curl -v https://r01.yourdomain.com/health`

**High memory usage:**
- Each room uses ~1-2KB of state. 1000 rooms ≈ 2MB.
- If memory grows unexpectedly, check for zombie clients: rooms with many peers but no activity
- Relay logs zombie cleanup: `[STREAM] Cleanup: leaving session ...`

## File Reference

```
deploy/
├── DEPLOY_GUIDE.md              ← this file
├── inventory.txt                ← host registry (domain, provider, region, IP, cost)
├── scripts/
│   ├── setup-host.sh            ← bootstrap a new VPS (one-time)
│   └── update-pool.sh           ← rolling update, health check, pool status
├── local/
│   └── docker-compose.yml       ← local dev: relay + Grafana LGTM
└── production/
    ├── docker-compose.yml       ← production: Caddy + relay
    ├── Caddyfile                ← Caddy reverse proxy config
    └── .env.example             ← configuration template
```
