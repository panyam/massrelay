# Distributed Relay Architecture: Multi-Instance Session Support

> GitHub Issue: https://github.com/panyam/massrelay/issues/5

## Problem

All participants in a massrelay collab session must connect to the same relay server instance. This means:
- A single server crash takes down all sessions it hosts
- No horizontal scaling for a single session across servers
- Vertical scaling ceiling on peer count per session

Multisynq/Croquet has the same limitation — they pin each session to one "Synchronizer" and scale by isolating sessions. We want to go further: **peers in ONE session should be able to connect to DIFFERENT relay instances**, eliminating the single-server SPOF.

## Current Architecture

- `CollabService.rooms map[string]*CollabRoom` — all rooms in-process memory
- `CollabRoom.BroadcastToAll/Except` — fan-out via non-blocking Go channel sends
- `CollabClient.SendCh chan *pb.CollabEvent` (buffered cap 64) — one channel per WebSocket
- Relay is already nearly stateless (no app data, just peer registry + message routing)
- E2EE means relay can't read content

**Three coupling points that make it single-server:**
1. `rooms` map — room metadata + peer registry in-memory
2. `Clients` map with `SendCh` channels — direct local channel delivery
3. `hintIndex` map — session hint lookup is local

## Alternatives Considered

### A. Redis Pub/Sub + Redis KV (Industry default)
- Redis stores room registry; Redis Pub/Sub forwards events between relays
- **Pros:** Battle-tested (Discord, Slack start here), well-understood failure modes, trivial to implement
- **Cons:** External dependency to operate, extra hop per message (~0.5-1ms), Redis Pub/Sub is at-most-once
- **Verdict:** Solid choice. Offered as an alternative backend for teams that prefer centralized infrastructure.

### B. Custom BGP-inspired mesh protocol
- Relays implement their own gossip, route advertisements, failure detection from scratch
- **Pros:** Zero deps, architecturally elegant
- **Cons:** Rolling your own distributed protocol is a maintenance burden. BGP has 30+ years of RFCs; we'd have none. Split-brain, failure detection, and membership changes are subtle.
- **Verdict:** Too much custom distributed systems code. Good mental model, risky implementation.

### C. Embedded NATS
- Embed NATS server in each relay. Room = NATS subject. Relays auto-cluster.
- **Pros:** NATS was built for exactly this pattern — lightweight, embeddable, clusterable
- **Cons:** Significant dependency. NATS clustering adds its own operational complexity. Overkill for our message volume.
- **Verdict:** Interesting but heavy for a library that values being self-contained.

### D. HashiCorp memberlist + direct gRPC ⭐ Recommended
- Use [`hashicorp/memberlist`](https://github.com/hashicorp/memberlist) (SWIM gossip protocol) for node discovery + failure detection. Direct relay-to-relay gRPC streams for event forwarding.
- **Pros:** Zero external infrastructure (self-contained binary). Gossip is battle-tested (Consul, Nomad, Serf use it). Direct gRPC gives low latency. Failure detection is already solved.
- **Cons:** Still need custom event forwarding logic (but that's the simpler, domain-specific part)
- **Verdict:** Best balance of self-contained deployment, proven infrastructure, and minimal custom protocol.

## Architecture Design

### Approach: memberlist gossip + direct gRPC forwarding

Relays form a **self-organizing mesh** using HashiCorp's `memberlist` library (SWIM gossip protocol) for node discovery and failure detection. Event forwarding uses direct relay-to-relay gRPC streams for low latency.

**Key properties:**
- Zero external infrastructure — relay binary is self-contained
- Node discovery + failure detection via battle-tested SWIM gossip
- Direct relay-to-relay gRPC for event forwarding (no broker hop)
- Self-organizing and self-healing
- Full mesh practical at our scale (10s of relays)
- Client protocol unchanged — distribution is fully transparent

### Three-Layer Architecture

```
┌──────────────────────────────────────────────────────┐
│                    Interfaces                         │
│  RoomRegistry  •  EventBus  •  LocalPeerManager      │
└──────────────────────────────────────────────────────┘
         │                │                │
    ┌────┴────┐    ┌──────┴──────┐   (always local)
    │ InMemory│    │  InMemory   │
    │  (solo) │    │   (solo)    │
    └─────────┘    └─────────────┘
    ┌─────────┐    ┌─────────────┐
    │  Mesh   │    │    Mesh     │
    │(cluster)│    │  (cluster)  │
    └─────────┘    └─────────────┘
    ┌─────────┐    ┌─────────────┐
    │  Redis  │    │ Redis/NATS  │   ← optional
    │(broker) │    │  (broker)   │
    └─────────┘    └─────────────┘
```

### Interface: `RoomRegistry`

Manages room metadata + peer membership. Replaces `CollabService.rooms` map.

```go
type RoomRegistry interface {
    GetOrCreateRoom(sessionId string) (*RoomInfo, error)
    GetRoom(sessionId string) (*RoomInfo, error)
    RemoveRoom(sessionId string) error
    AddPeer(sessionId string, peer PeerRecord) error
    RemovePeer(sessionId string, clientId string) (remaining int, err error)
    GetPeers(sessionId string) ([]PeerRecord, error)
    FindPeerRoom(clientId string) (sessionId string, err error)
    FindByBrowserId(sessionId, browserId, excludeClientId string) (*PeerRecord, error)
    SetRoomOwner(sessionId, ownerClientId, ownerBrowserId string) error
    UpdateRoomMeta(sessionId string, updates RoomMetaUpdate) error
    SetHint(hint, sessionId string) error
    GetHint(hint string) (sessionId string, err error)
    RemoveHintsForSession(sessionId string) error
    PeerCount(sessionId string) (int, error)
    ListRooms() ([]RoomSummary, error)
}
```

`PeerRecord` includes a `NodeId` field — which relay instance holds the WebSocket.

### Interface: `EventBus`

Pub/sub for room events across relay instances. Replaces `BroadcastToAll`/`BroadcastExcept`.

```go
type EventBus interface {
    Publish(ctx context.Context, sessionId string, event *pb.CollabEvent, exceptClientId string) error
    Subscribe(sessionId string, handler EventHandler) (unsubscribe func(), err error)
    Close() error
}

type EventHandler func(event *pb.CollabEvent, targetClientId string)
```

### `LocalPeerManager` (always per-node)

Manages `clientId → SendCh` for WebSocket connections on THIS server only.

```go
type LocalPeerManager struct {
    nodeId  string
    clients map[string]chan *pb.CollabEvent
}
```

### Node Discovery & Failure Detection

Uses `hashicorp/memberlist` with the SWIM protocol:

1. Each relay has a `nodeId` (UUID) and metadata: `{grpcAddr, httpAddr}`
2. On startup, join cluster via seed peers: `--join=relay-a:7946,relay-b:7946`
3. memberlist handles: node discovery, failure detection (ping/indirect-ping/suspect), metadata propagation
4. We implement `memberlist.Delegate` to broadcast route updates via gossip piggyback

**What memberlist gives us for free:**
- Automatic node discovery (new relay joins, all others learn about it)
- Failure detection with configurable timeouts (default ~5s suspect, ~10s dead)
- `NotifyJoin`, `NotifyLeave`, `NotifyUpdate` callbacks
- Metadata propagation (each node's gRPC address, current session count)

**What we build on top:**
- gRPC peering connections (established when memberlist notifies us of a new node)
- Route table: sessionId → [nodeId1, nodeId2, ...] (maintained via route updates)
- Event forwarding over gRPC streams

### Route Advertisements

When a client joins/leaves a room, route updates are sent via gRPC (not gossip — needs reliable delivery):

```protobuf
message RouteUpdate {
    string node_id = 1;
    string session_id = 2;
    int32 peer_count = 3;          // 0 = withdrawal
    RoomInfo room_info = 4;        // included on first advertisement
    int64 timestamp = 5;
}
```

When a client joins a room on relay A:
1. `RoomRegistry.AddPeer()` adds locally
2. Send `RouteUpdate{peer_count: N}` to all peered relays via gRPC
3. Peered relays update their route tables: "session X has peers on relay A"

When last client leaves a room on relay A:
1. `RouteUpdate{peer_count: 0}` — route withdrawal
2. Peers remove relay A from session X's route entry

### Cross-Server Message Flow

```
Client A ──WS──► Relay 1                    Relay 2 ──WS──► Client B
                    │                           │
                    │  1. Client A sends         │
                    │     SceneUpdate            │
                    │                            │
                    │  2. Relay 1 checks route   │
                    │     table: session X also   │
                    │     on Relay 2              │
                    │                            │
                    │  3. Forward via peering ───►│
                    │     gRPC stream             │
                    │                            │
                    │                   4. Relay 2 delivers
                    │                      to Client B's
                    │                      local SendCh
```

### Room State Authority

- The relay hosting the **owner** is authoritative for room metadata (title, encrypted, etc.)
- Ownership transfer across relays: atomic update via the mesh — old owner's relay notifies new owner's relay
- If owner's relay dies: memberlist detects via SWIM, surviving relays trigger ownership transfer

### Server Crash Recovery

1. memberlist detects relay A is dead (~10s default)
2. `NotifyLeave` fires on surviving relays
3. Route tables pruned
4. For sessions where relay A had the owner:
   - Surviving relays with peers coordinate ownership transfer
   - `FindByBrowserId` across local peers, then fallback to any remaining peer
5. Clients auto-reconnect to any healthy relay
6. SceneInit from remaining peers restores state

### Ownership Transfer Protocol (cross-relay)

This is the trickiest operation:

1. Owner disconnects on relay A (crash or graceful leave)
2. Relay A (if alive) or detecting relay sends `OwnerTransferRequest` to mesh
3. Each relay checks local peers for same-browser match (`FindByBrowserId`)
4. First match claims via `OwnerTransferClaim{clientId, nodeId}`
5. No same-browser match within 500ms: any relay with peers can claim
6. First-writer-wins (keyed on session+timestamp)
7. `OwnerChanged` event broadcast to all peers

For Redis/NATS backend: use Lua script or CAS for atomic ownership transfer instead.

## Implementation Phases

### Phase 1: Extract interfaces + InMemory implementations (no behavior change)

All existing tests pass unchanged.

| New File | Contents |
|----------|----------|
| `services/registry.go` | `RoomRegistry` interface + types |
| `services/eventbus.go` | `EventBus` interface + types |
| `services/registry_memory.go` | `InMemoryRoomRegistry` — wraps current `rooms` + `hintIndex` |
| `services/eventbus_memory.go` | `InMemoryEventBus` — wraps current channel broadcast |
| `services/local_peers.go` | `LocalPeerManager` — extracted from `CollabRoom.Clients` |

| Modified File | Change |
|----------------|--------|
| `services/collab_service.go` | Replace `rooms` map with `RoomRegistry`, broadcast with `EventBus` |
| `services/room.go` | Slim or absorb into registry + local peers |
| `web/server/app.go` | Accept optional `RoomRegistry`/`EventBus`; default InMemory |
| `web/server/stream.go` | Use `LocalPeerManager` for channel lookup |

### Phase 2: Mesh networking layer (memberlist + gRPC)

| New File | Contents |
|----------|----------|
| `services/mesh/mesh.go` | `MeshNode` — wraps memberlist, manages gRPC peering, route table |
| `services/mesh/delegate.go` | `memberlist.Delegate` — node metadata, route piggyback |
| `services/mesh/events.go` | `memberlist.EventDelegate` — join/leave handlers, gRPC lifecycle |
| `services/mesh/registry_mesh.go` | `MeshRoomRegistry` — local state + route advertisements |
| `services/mesh/eventbus_mesh.go` | `MeshEventBus` — local delivery + gRPC forwarding |

Proto additions (`protos/massrelay/v1/models/mesh.proto`):
```protobuf
message RouteUpdate {
    string node_id = 1;
    string session_id = 2;
    int32 peer_count = 3;
    RoomInfo room_info = 4;
    int64 timestamp = 5;
}

message OwnerTransferRequest {
    string session_id = 1;
    string requesting_node_id = 2;
    int64 timestamp = 3;
}

message OwnerTransferClaim {
    string session_id = 1;
    string client_id = 2;
    string node_id = 3;
    int64 timestamp = 4;
}

message MeshEvent {
    oneof event {
        RouteUpdate route_update = 1;
        OwnerTransferRequest owner_transfer_req = 2;
        OwnerTransferClaim owner_transfer_claim = 3;
        CollabEvent collab_event = 4;
    }
}
```

New dependency: `go get github.com/hashicorp/memberlist`

### Phase 3: Redis/NATS backends (optional)

| New File | Contents |
|----------|----------|
| `services/redisbackend/registry.go` | Redis-backed `RoomRegistry` |
| `services/redisbackend/eventbus.go` | Redis Pub/Sub `EventBus` |

### Phase 4: Crash recovery, ownership transfer, testing

- Ownership transfer protocol across relays
- Integration tests with 2-3 relay instances in-process
- Chaos testing (kill relay mid-session, verify recovery)

## Transport: Inter-Relay Communication

| Transport | Pros | Cons |
|-----------|------|------|
| **gRPC (HTTP/2)** | Already use protobuf; multiplexed streams; mature Go ecosystem | TCP head-of-line blocking |
| **gRPC over QUIC (HTTP/3)** | No HOL blocking; 0-RTT reconnect | Go support maturing; some firewalls block UDP |
| **Raw WebSocket** | Simple; works everywhere | No multiplexing; custom framing needed |
| **QUIC streams directly** | Maximum control; per-room streams | No protobuf integration; more custom code |

**Recommended path:**
1. **Phase 2:** gRPC (HTTP/2) — works today, multiplexed, proto integration
2. **Phase 4+:** Migrate to gRPC-over-QUIC — eliminates TCP HOL blocking, 0-RTT reconnect

## Key Design Decisions

1. **memberlist over custom gossip:** SWIM protocol is battle-tested (Consul, Nomad, Serf). We focus on domain logic, not distributed protocol fundamentals.
2. **Full mesh (not hierarchical):** At our scale (10s of relays), every relay peers with every other. No route reflectors needed.
3. **Owner-authoritative for metadata:** No distributed consensus (Raft/Paxos). Owner's relay is source of truth.
4. **Interfaces first:** InMemory is default. Mesh/Redis opt-in. Zero overhead for single-server.
5. **Client protocol unchanged:** Distribution transparent to clients.
6. **memberlist + gRPC recommended** (not Redis): Zero external infrastructure, lower latency, self-healing.

## Open Questions

1. **memberlist config tuning:** Default SWIM timings may need tuning for WAN deployments. LAN config fine for same-datacenter.
2. **Event ordering guarantees:** Per-room gRPC stream preserves order per publisher, but cross-relay events may interleave. Excalidraw's version/nonce handles this — do we need stronger guarantees?
3. **Max mesh size:** Full mesh scales to ~50 nodes. Beyond that, need route reflectors. Sufficient?
4. **Graceful relay drain:** `memberlist.Leave()` + transfer sessions, or let clients reconnect?
5. **QUIC readiness:** When to migrate from HTTP/2 to QUIC for peering transport?

## Out of Scope

- Client protocol changes (all distribution is server-side)
- E2EE changes (relay still can't read content)
- Rate limiting changes (stays per-server)
- Changes to excaliframe client code
