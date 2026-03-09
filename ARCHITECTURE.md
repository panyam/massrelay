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

## Security Model

- No authentication — session IDs are the only access control
- Optional E2EE — relay never sees plaintext; encryption is client-side
- `BrowserId` is server-only (not in `PeerInfo`) — used for ownership transfer, not exposed to peers
- `SessionId` is on `Room`, not `PeerInfo` — redundant per-peer

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
