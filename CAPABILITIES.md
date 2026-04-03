# MassRelay

## Version
0.0.4

## Provides
- websocket-relay: Stateless WebSocket relay for real-time collaboration
- room-routing: Room-based message routing
- peer-management: Peer tracking with client_hint, session ownership, ownership transfer
- collab-protocol: CollabAction/CollabEvent protobuf messaging protocol
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
- oneauth (github.com/panyam/oneauth)
- servicekit (github.com/panyam/servicekit)

## Integration

### Go Module
```go
// go.mod
require github.com/panyam/massrelay 0.0.4

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
