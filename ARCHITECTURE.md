# Massrelay Architecture

## Overview

Massrelay is a stateless WebSocket relay for real-time collaboration. It routes messages between peers in named rooms with no persistent state, no database, and no knowledge of application-specific data formats.

## Key Components

### Go Server (`services/`, `web/`)

```
CollabService          ← manages rooms, peer lifecycle, action routing
  ├── CollabRoom       ← in-memory room (peers, ownership, metadata)
  │   ├── CollabClient ← embeds *pb.PeerInfo + server-only fields
  │   └── ...
  └── hintIndex        ← client_hint → sessionId mapping
RelayApp               ← http.Handler wrapping CollabService + WebSocket
```

### TypeScript Client (`ts/src/`)

```
CollabClient           ← WebSocket transport (GRPCWSClient wrapper)
CollabEngine           ← State machine, sync orchestration, encryption
SyncAdapter            ← Tool-agnostic interface for editor-specific logic
```

## Proto Design

All wire types are defined in `protos/massrelay/v1/models/collab.proto`.

### Key design decisions

- **`CollabAction` / `CollabEvent`**: Client→Server and Server→Client messages use `oneof` for action/event variants. The Go server uses `protojson` for JSON marshaling; the TS client uses raw JSON (protobuf JSON format with camelCase).

- **`Room` message**: Shared between `RoomJoined` (WebSocket) and `GetRoomResponse` (REST) to avoid field duplication. Contains `map<string, PeerInfo> peers` (keyed by client_id) and `google.protobuf.Timestamp created_at`.

- **`PeerInfo` embedding**: Go `CollabClient` embeds `*pb.PeerInfo` for field promotion. Server-only fields (`SessionId`, `BrowserId`, `SendCh`) remain as separate struct fields. This means `GetPeerInfo()` and `ToProto()` can reuse the embedded proto directly without manual field copying.

- **`CollabRoom` vs `pb.Room`**: `CollabRoom` is NOT embedded from `pb.Room` because `CollabRoom.Clients` is `map[string]*CollabClient` (includes `SendCh`, `BrowserId`) while `pb.Room.Peers` is `map[string]*PeerInfo` (wire-visible only). `ToProto()` produces a `pb.Room` snapshot on demand.

- **JSON types in TypeScript**: `buf.gen.yaml` uses `json_types=true` to generate `CollabEventJson`, `PeerInfoJson`, etc. — types that match the raw JSON wire format. This eliminates `any` in the TS client without requiring `fromJson()`/`toJson()` deserialization. See Issue #9 for the future migration to canonical Message types.

### Security Middleware (`web/middleware/`)

```
Guard                  ← composes all security middleware into single Wrap(handler)
  ├── OriginChecker    ← WebSocket origin allowlist (exact, wildcard, localhost)
  ├── ConnLimiter      ← atomic counter, max concurrent connections (503 when full)
  └── RateLimiter      ← global + per-IP rate limiting with OnRejected callback
```

Zero app-specific imports — designed for future lift to servicekit.

## Security Model

- No authentication — session IDs are the only access control
- Optional E2EE — relay never sees plaintext; encryption is client-side
- `BrowserId` is server-only (not in `PeerInfo`) — used for ownership transfer, not exposed to peers
- `SessionId` is on `Room`, not `PeerInfo` — redundant per-peer
- Origin allowlist for WebSocket connections (`RELAY_ALLOWED_ORIGINS`)
- Connection limits (`RELAY_MAX_CONNECTIONS`, default 500)
- Rate limiting: global (`RELAY_GLOBAL_RATE`, default 100/s), per-IP (`RELAY_PER_IP_RATE`, default 5/s), per-client messages (30/s)

## Observability (OpenTelemetry)

The relay supports OTEL metrics, configured entirely via environment variables:

- `OTEL_EXPORTER_OTLP_ENDPOINT` — enables OTLP export (e.g. Grafana Cloud)
- `OTEL_SERVICE_NAME` — defaults to "massrelay"
- `OTEL_METRICS_PROMETHEUS=true` — serves `/metrics` for Prometheus scraping

If unconfigured, OTEL is a no-op (zero overhead).

### Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `relay.rooms.active` | UpDownCounter | Active rooms |
| `relay.peers.active` | UpDownCounter | Connected peers |
| `relay.connections.total` | Counter | WebSocket connections |
| `relay.messages.total` | Counter | Messages relayed (with `type` attribute) |
| `relay.joins.total` / `relay.leaves.total` | Counter | Room joins/leaves |
| `relay.rate_limited.total` | Counter | Rate-limited requests |
| `relay.messages.dropped` | Counter | Dropped messages (full channel) |
| `relay.message.size` | Histogram | Message payload size |

### Architecture

Metrics are wired via callbacks on `CollabService` (`OnRoomCreated`, `OnRoomRemoved`, `OnPeerJoined`, `OnPeerLeft`, `OnMessageRelay`), keeping the OTEL dependency in the server layer (`web/server/app.go`) rather than the core service. The `otel/` package provides setup and metric instrument creation.

The `/health` endpoint returns enriched stats: `status`, `uptime_seconds`, `rooms`, `peers`, `goroutines`.

### Structured Logging

All logging uses `log/slog` with JSON output to stdout. Each log entry includes a `component` key (`relay`, `stream`, `http`, `origin`, `connlimit`, `ratelimit`, `otel`) for Grafana/Loki filtering. Loki ingests logs via the Docker log driver — no OTLP log bridge needed.

### Dev Observability Stack

`deploy/dev/docker-compose.yml` runs:
- **Relay** with OTLP → Grafana LGTM
- **Grafana LGTM** (Loki + Grafana + Tempo + Mimir) with pre-provisioned dashboards

Dashboard JSON is provisioned via `deploy/dev/grafana/dashboards/relay.json`.

## Deployment

### Architecture

Stateless relay pool — each VPS runs Caddy (auto-TLS) + relay binary. No coordination between relays. Client-side relay selection via static `relay-pool.json`.

### Docker Packaging

Multi-stage build produces ~35MB image with built-in healthcheck. Two compose stacks:
- `deploy/dev/` — relay + Grafana LGTM for local development
- `deploy/production/` — Caddy + relay for VPS deployment

### Automation

- `deploy/scripts/setup-host.sh` — bootstrap new VPS (Docker, firewall, clone, configure, start)
- `deploy/scripts/update-pool.sh` — rolling updates, health checks, pool status
- `deploy/inventory.txt` — host registry (domain, provider, region, IP, cost)

## Broadcast Model

- `BroadcastToAll` / `BroadcastExcept` — non-blocking channel sends (cap 64)
- Events dropped silently if channel full
- `watchClose` goroutine detects ungraceful disconnects

## Test Organization

Go tests (`services/`):
- `helpers_test.go` — shared test utilities
- `service_core_test.go` — join/leave, presence, service creation
- `broadcast_test.go` — scene/text/cursor relay
- `owner_test.go` — ownership lifecycle
- `query_test.go` — GetRoom, ListRooms, ToProto, PeerInfo embedding
- `limits_encryption_test.go` — room full, E2EE, protocol checks
- `log_payloads_test.go` — payload logging
- `room_test.go` — room unit tests (add/remove, broadcast, peer info)

TypeScript tests (`ts/src/`):
- `CollabEngine.test.ts` — 33 tests covering lifecycle, sync, encryption, cursors, peers
- `crypto.test.ts` — 12 tests for AES-256-GCM encrypt/decrypt
- `peerColors.test.ts` — 8 tests for deterministic peer color assignment
