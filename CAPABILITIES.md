# MassRelay

## Version
0.1.1

## Provides
- websocket-relay: Stateless WebSocket relay for real-time collaboration
- room-routing: Room-based message routing
- peer-management: Peer tracking with client_hint, session ownership, ownership transfer
- collab-protocol: CollabAction/CollabEvent protobuf messaging protocol
- app-message: generic app-defined pub-sub messages (AppMessage{kind,payload}), room-wide fan-out with no editor semantics — use massrelay as a message bus without the Scene/Text protocol
- admin-api: Token-gated admin endpoints (/admin/status, /admin/rooms)
- security-middleware: Origin allowlist, connection limiting, rate limiting, CSRF, trusted proxy
- observability: OpenTelemetry metrics (Prometheus, OTLP), structured JSON logging (slog)
- e2ee-support: End-to-end encryption support (relay-agnostic)
- typescript-client: @panyam/massrelay npm package (CollabClient, SyncAdapter, CollabEngine)
- docker-deployment: Docker + Caddy deployment ready
- graceful-shutdown: Connection draining via servicekit ListenAndServeGraceful

## Module
github.com/panyam/massrelay

## Location
newstack/massrelay

## Stack Dependencies
- oneauth (github.com/panyam/oneauth) v0.1.36 — uses only `apiauth` (JWT middleware) + `keys` (multi-tenant KeyLookup)
- servicekit (github.com/panyam/servicekit) v0.1.3 — `middleware`, `http` (ListenAndServeGraceful), `grpcws`
- gocurrent (github.com/panyam/gocurrent) v0.1.1 — indirect
- goutils (github.com/panyam/goutils) v0.1.13 — indirect

Requires Go 1.26.4+ (oneauth v0.1.36 minimum).

## Integration

### Go Module
```go
// go.mod
require github.com/panyam/massrelay 0.1.1

// Local development
replace github.com/panyam/massrelay => ~/newstack/massrelay
```

### Key Imports
```go
import "github.com/panyam/massrelay/relay"
```

## Status
Mature

## Conventions
- Stateless relay (no DB)
- Protobuf wire format
- OpenTelemetry instrumentation
- SyncAdapter interface pattern for editor integration
