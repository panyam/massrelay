# Massrelay Roadmap

## Completed

### Proto & Type Safety (PR #7, #8)
- [x] Extract shared `Room` proto message (reduces duplication between `RoomJoined` and `GetRoomResponse`)
- [x] Embed `*pb.PeerInfo` in Go `CollabClient` struct (field promotion, zero-copy peer snapshots)
- [x] Move `Metadata` from server-only to `PeerInfo` proto (peers see each other's app-defined metadata)
- [x] `Room.peers`: `repeated PeerInfo` -> `map<string, PeerInfo>` (O(1) lookup by client ID)
- [x] `Room.created_at`: `int64` -> `google.protobuf.Timestamp` (proper time semantics)
- [x] `RoomSummary.created_at`: `int64` -> `google.protobuf.Timestamp`
- [x] Enable `json_types=true` in protoc-gen-es — TS client uses generated `CollabEventJson`, `PeerInfoJson` etc. instead of `any`
- [x] Split monolithic Go test file into 7 focused test files
- [x] GitHub Actions CI (Go + TypeScript)
- [x] Git pre-push hook running all tests

### Infrastructure
- [x] Rate limiting (global, per-IP, per-client message)
- [x] Room participant limits with graceful `ROOM_FULL` error
- [x] Optional E2EE (AES-256-GCM, PBKDF2 key derivation, client-side only)
- [x] Title sync (owner sets, relay stores, new joiners receive)
- [x] Ownership transfer (same-browser tab via `browserId`)
- [x] Payload logging for E2EE debugging

## In Progress

### Issue #3: CollabClient proto alignment
- PR #8 addresses this — embed PeerInfo, typed TS, map peers, Timestamp
- Remaining: consider embedding `*pb.Room` in `CollabRoom` (now more viable with map peers + Timestamp)

## Next Up

### Issue #9: Canonical protobuf-es Message types
- Switch TS from JSON types (`CollabEventJson`) to Message types (`CollabEvent`) with `fromJson()`/`toJson()` at the transport boundary
- Enables exhaustive `switch` on oneof, validates incoming messages, prepares for binary transport

### Issue #2: Use gocurrent.FanOut for room broadcasting
- Replace manual `BroadcastToAll`/`BroadcastExcept` with `gocurrent.FanOut`

### Issue #4: Relay Pool (client-side)
- Client-side relay selection, health probing, auto-reconnect

### Issue #6: Distributed Relay Architecture
- Multi-instance session support via memberlist + gRPC
- See `docs/DISTRIBUTED_RELAY_ARCHITECTURE.md` for full design

## Recently Completed

### OpenTelemetry Instrumentation
- [x] OTEL metrics setup (`otel/` package) with OTLP + Prometheus exporters
- [x] Relay metrics: rooms, peers, connections, messages, rate limits, dropped messages
- [x] Enriched `/health` endpoint (uptime, rooms, peers, goroutines)
- [x] Service callbacks for metric instrumentation (decoupled from core service)
- [x] Zero-config: no-op when OTEL env vars not set

## Future

- Auto-reconnect with session validation (currently disabled to prevent phantom sessions)
- `ListRooms` REST endpoint (exists in code, intentionally not registered to prevent session enumeration)
- Binary protobuf transport (instead of JSON over WebSocket)
- Python/Node client libraries
- OTEL tracing (message flow through relay)
