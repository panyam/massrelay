# Massrelay

A stateless WebSocket relay server for real-time collaboration. Massrelay routes messages between peers in named rooms — it holds no persistent state, stores no data, and requires no database. Any client that speaks the protocol (browser, CLI, agent) can join a session.

**Go module:** `github.com/panyam/massrelay`
**npm package:** `@panyam/massrelay`

---

## Table of Contents

- [Quick Start](#quick-start)
- [Architecture](#architecture)
- [HTTP API](#http-api)
  - [Health Check](#health-check)
  - [WebSocket Endpoint](#websocket-endpoint)
  - [Get Room](#get-room)
- [Protocol Reference](#protocol-reference)
  - [Client → Server (CollabAction)](#client--server-collabaction)
  - [Server → Client (CollabEvent)](#server--client-collabevent)
  - [Shared Message Types](#shared-message-types)
- [Message Flows](#message-flows)
  - [Join a Session](#join-a-session)
  - [Send an Update](#send-an-update)
  - [Cursor Broadcasting](#cursor-broadcasting)
  - [Scene Init (New Joiner)](#scene-init-new-joiner)
  - [Leave / Disconnect](#leave--disconnect)
  - [Owner Lifecycle](#owner-lifecycle)
  - [Encrypted Rooms](#encrypted-rooms)
  - [Title Sync](#title-sync)
  - [Credentials Change](#credentials-change)
- [Server Configuration](#server-configuration)
  - [Rate Limiting](#rate-limiting)
  - [Room Limits](#room-limits)
  - [Payload Logging](#payload-logging)
  - [CORS](#cors)
- [Embedding in Another Server](#embedding-in-another-server)
- [TypeScript Client](#typescript-client)
  - [Installation](#installation)
  - [Package Exports](#package-exports)
  - [CollabClient](#collabclient)
  - [SyncAdapter Interface](#syncadapter-interface)
  - [URL Helpers](#url-helpers)
- [Go API](#go-api)
  - [CollabService](#collabservice)
  - [CollabRoom](#collabroom)
  - [RelayApp](#relayapp)
- [Wire Format](#wire-format)
- [Security](#security)
- [Limitations](#limitations)

---

## Quick Start

### Standalone Server

```bash
go build -o relay .
./relay -port 8787
```

The relay is now accepting WebSocket connections at `ws://localhost:8787/ws/v1/{session_id}/sync` and REST queries at `http://localhost:8787/api/v1/rooms/{session_id}`.

### Browser Client

```bash
npm install @panyam/massrelay
```

```typescript
import { CollabClient } from '@panyam/massrelay/client';

const client = new CollabClient({
  onConnect: (clientId) => console.log('Connected as', clientId),
  onEvent: (event) => console.log('Event:', event),
  onPeerJoined: (peer) => console.log('Peer joined:', peer.username),
  onPeerLeft: (clientId) => console.log('Peer left:', clientId),
  onDisconnect: () => console.log('Disconnected'),
});

client.connect(
  'ws://localhost:8787',         // relayUrl
  '',                            // sessionId (empty = relay generates one)
  'Alice',                       // username
  { tool: 'whiteboard' },        // metadata (application-defined key-value pairs)
  true,                          // isOwner
);

// Send a scene update to all peers
client.send({
  sceneUpdate: {
    elements: [
      { id: 'el1', version: 1, versionNonce: 42, data: '{"type":"rectangle",...}' }
    ]
  }
});

// Later
client.disconnect();
```

---

## Architecture

```
┌─────────────────────┐     ┌─────────────────────┐     ┌──────────────┐
│   Browser Tab A     │     │   Browser Tab B      │     │   CLI / API  │
│                     │     │                      │     │              │
│ CollabClient ←──────┼─WS──┼──→ CollabClient      │     │ CollabClient │
└─────────────────────┘     └──────────────────────┘     └──────┬───────┘
          │                           │                         │
          └────────┐    ┌─────────────┘       ┌────────────────┘
                   ▼    ▼                     ▼
          ┌─────────────────────────────────────┐
          │          Relay Server               │
          │                                     │
          │  Room "abc123"                      │
          │    ├── Client A (owner, browser)    │
          │    ├── Client B (browser)           │
          │    └── Client C (cli)               │
          │                                     │
          │  Room "def456"                      │
          │    └── Client D (owner, browser)    │
          │                                     │
          │  No database. No persistence.       │
          │  Pure message routing.              │
          └─────────────────────────────────────┘
```

**Key design decisions:**

- **Stateless**: Rooms exist only in memory. Server restart = all sessions end.
- **Tool-agnostic**: The relay has no knowledge of Excalidraw, Mermaid, or any specific editor. It routes opaque payloads between peers.
- **Owner model**: One peer is the session owner. When the owner disconnects, ownership transfers to a same-browser tab or the session ends.
- **No authentication**: Anyone with a session ID can join. Access control is the application's responsibility.
- **Optional E2EE**: Clients can declare rooms as encrypted. The relay enforces protocol version checks but never sees plaintext — encryption/decryption is client-side.

---

## HTTP API

### Health Check

```
GET /health
```

**Response** `200 OK`:
```json
{ "status": "ok" }
```

---

### WebSocket Endpoint

```
GET /ws/v1/{session_id}/sync
```

Upgrades to a WebSocket connection for bidirectional streaming. The `{session_id}` in the URL is informational — the actual session is determined by the `JoinRoom` action sent over the WebSocket.

**Transport:** [servicekit](https://github.com/panyam/servicekit) envelope protocol. Each WebSocket frame is a JSON object:

```json
{ "type": "data", "data": <protobuf-JSON-payload> }
```

The inner `data` field contains `CollabAction` (client→server) or `CollabEvent` (server→client) in protobuf JSON format with **camelCase** field names.

**Rate limiting** is applied at two levels:

| Level | Limit | Behavior |
|-------|-------|----------|
| Global connections | 100/sec | HTTP 429 before WS upgrade |
| Per-IP connections | 5/sec, burst 3 | HTTP 429 before WS upgrade |
| Per-client messages | 30/sec | Silently dropped (join/leave exempt) |

**Message size limit:** 1 MB per message.

---

### Get Room

```
GET /api/v1/rooms/{session_id}
```

Returns information about an active room.

**Response** `200 OK`:
```json
{
  "room": {
    "sessionId": "a1b2c3d4-e5f6-...",
    "peers": {
      "peer-uuid": {
        "clientId": "peer-uuid",
        "username": "Alice",
        "avatarUrl": "",
        "clientType": "browser",
        "isActive": true,
        "isOwner": true,
        "metadata": { "tool": "whiteboard" }
      }
    },
    "createdAt": "2024-03-10T00:00:00Z",
    "ownerClientId": "peer-uuid",
    "metadata": { "tool": "whiteboard" },
    "encrypted": false,
    "title": "My Drawing"
  }
}
```

**Response** `404 Not Found`:
```json
{ "error": "room not found" }
```

> **Note:** A `ListRooms` endpoint exists in the code but is intentionally **not registered** in routes to prevent session enumeration.

---

## Protocol Reference

All messages use [Protocol Buffers](https://protobuf.dev/) definitions serialized as JSON over WebSocket. Source: [`protos/massrelay/v1/models/collab.proto`](protos/massrelay/v1/models/collab.proto).

### Client → Server (CollabAction)

Every message from client to server is a `CollabAction` with one of the following action types set via the `oneof action` field.

#### Envelope Fields

| Field | Type | Description |
|-------|------|-------------|
| `actionId` | string | Optional client-assigned action ID |
| `clientId` | string | Client's assigned ID (set after join) |
| `timestamp` | int64 | Client-side timestamp (ms since epoch) |

#### JoinRoom

Sent once after WebSocket connection to join or create a session.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `sessionId` | string | (generated) | Session to join. Empty = relay generates a new UUID. |
| `username` | string | `"Anonymous"` | Display name for this peer. |
| `metadata` | map\<string, string\> | `{}` | Application-defined key-value pairs (e.g. `{"tool": "whiteboard"}`). |
| `clientType` | string | `"browser"` | Client kind: `"browser"`, `"cli"`, `"api"`. |
| `avatarUrl` | string | | Profile picture URL. |
| `isOwner` | bool | `false` | `true` if this client owns the session. |
| `browserId` | string | | localStorage UUID for same-browser ownership transfer. |
| `clientHint` | string | | Opaque session reuse hint (e.g. `"browserId:drawingId"`). Used to find an existing session without knowing the session ID. |
| `protocolVersion` | int32 | `1` | Client protocol version. Must be `2` to join encrypted rooms. |
| `encrypted` | bool | `false` | Owner declares room as encrypted (E2EE). |
| `title` | string | | Drawing title. Relay stores and echoes to joiners. |

**Session resolution order:**
1. If `sessionId` is provided, use it directly.
2. If `clientHint` is provided and maps to a known session, reuse that session.
3. Otherwise, generate a new UUID.

#### LeaveRoom

Graceful disconnect. Also sent automatically by `CollabClient` on `beforeunload`.

| Field | Type | Description |
|-------|------|-------------|
| `reason` | string | Human-readable reason (e.g. `"user disconnected"`). |

#### PresenceUpdate

Online/offline status change.

| Field | Type | Description |
|-------|------|-------------|
| `isActive` | bool | `true` = online, `false` = idle/away. |
| `username` | string | Username at time of update. |

#### SceneUpdate

Element-level update for visual editors (e.g. Excalidraw). Broadcast to all peers except the sender.

| Field | Type | Description |
|-------|------|-------------|
| `elements` | ElementUpdate[] | Array of changed elements. |

Each `ElementUpdate`:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique element ID. |
| `version` | int32 | Monotonic version counter. |
| `versionNonce` | int32 | Random nonce for conflict detection. |
| `data` | string | Full element as JSON string. May be base64-encrypted ciphertext when E2EE is active. |
| `deleted` | bool | `true` if the element was deleted. |

#### CursorUpdate

Pointer position for live cursor tracking. Broadcast to all peers except the sender.

| Field | Type | Description |
|-------|------|-------------|
| `x` | float | Pointer X coordinate (canvas space). |
| `y` | float | Pointer Y coordinate (canvas space). |
| `tool` | string | Active tool name (e.g. `"selection"`, `"rectangle"`, `"text"`). |
| `button` | string | Mouse button state (e.g. `"down"`, `"up"`). |
| `selectedElementIds` | map\<string, bool\> | Currently selected element IDs. |

#### TextUpdate

Text content update for text-based editors (e.g. Mermaid). Broadcast to all peers except the sender.

| Field | Type | Description |
|-------|------|-------------|
| `text` | string | Full text content. May be base64-encrypted ciphertext when E2EE is active. |
| `version` | int32 | Version counter for last-writer-wins. |
| `cursorPosition` | int32 | Caret position (char offset). |

#### SceneInitRequest

Empty message sent by a new joiner to request a full scene snapshot from an existing peer. The relay broadcasts this to all peers except the sender, and the first peer to respond sends a `SceneInitResponse`.

#### SceneInitResponse

Full scene snapshot sent in response to a `SceneInitRequest`.

| Field | Type | Description |
|-------|------|-------------|
| `payload` | string | JSON blob (schema is tool-specific). May be base64-encrypted when E2EE is active. |

#### CredentialsChanged

Owner notifies peers that the session password has changed.

| Field | Type | Description |
|-------|------|-------------|
| `reason` | string | `"password_changed"`, `"password_removed"`, or `"key_rotated"`. |

When `reason` is `"password_removed"`, the relay marks the room as unencrypted.

#### TitleChanged

Owner broadcasts a title rename to all peers.

| Field | Type | Description |
|-------|------|-------------|
| `title` | string | New drawing title. |

The relay stores the updated title so new joiners receive it via `RoomJoined.title`.

---

### Server → Client (CollabEvent)

Every message from server to client is a `CollabEvent` with one of the following event types set via the `oneof event` field.

#### Envelope Fields

| Field | Type | Description |
|-------|------|-------------|
| `eventId` | string | Server-generated UUID for this event. |
| `fromClientId` | string | The client that originated this event (empty for server-initiated events). |
| `serverTimestamp` | int64 | Server timestamp (Unix seconds). |

#### RoomJoined

Sent to the joining client as confirmation of a successful join.

| Field | Type | Description |
|-------|------|-------------|
| `clientId` | string | Relay-assigned client ID (UUID) for this connection. |
| `room` | Room | Nested room state (see Room below). |
| `maxPeers` | int32 | Relay's participant limit (informational). |
| `protocolVersion` | int32 | Relay protocol version (currently `2`). |

#### Room

Canonical room state, shared by `RoomJoined` and `GetRoomResponse`.

| Field | Type | Description |
|-------|------|-------------|
| `sessionId` | string | Session ID (may differ from what was requested if relay generated one). |
| `peers` | map\<string, PeerInfo\> | **Existing peers only** — keyed by client ID. Does NOT include the joining client itself. |
| `ownerClientId` | string | Client ID of the session owner. |
| `createdAt` | Timestamp | Room creation time (RFC 3339 / `google.protobuf.Timestamp`). |
| `metadata` | map | Application-defined key-value pairs. |
| `encrypted` | bool | `true` if the room has E2EE enabled. |
| `title` | string | Drawing title set by the owner. |

#### PeerJoined

Broadcast to **existing** peers when a new client joins. NOT sent to the joining client.

| Field | Type | Description |
|-------|------|-------------|
| `peer` | PeerInfo | Info about the new peer. |

#### PeerLeft

Broadcast to remaining peers when a client disconnects.

| Field | Type | Description |
|-------|------|-------------|
| `clientId` | string | Client ID of the peer that left. |
| `reason` | string | Disconnect reason. |
| `peerCount` | int32 | Number of remaining peers. |

#### ErrorEvent

Graceful error — the relay sends this instead of closing the stream, so the client can display a user-friendly message.

| Field | Type | Description |
|-------|------|-------------|
| `code` | string | Error code (see below). |
| `message` | string | Human-readable error description. |

**Error codes:**

| Code | Cause |
|------|-------|
| `ROOM_FULL` | Room has reached `MaxPeersPerRoom` limit. |
| `PROTOCOL_VERSION_TOO_OLD` | Encrypted room requires `protocolVersion >= 2`. |

#### SessionEnded

Broadcast when the owner disconnects and no same-browser successor exists. All remaining peers are disconnected.

| Field | Type | Description |
|-------|------|-------------|
| `reason` | string | Always `"owner_disconnected"`. |

#### OwnerChanged

Broadcast when ownership transfers to another tab of the same browser.

| Field | Type | Description |
|-------|------|-------------|
| `newOwnerClientId` | string | Client ID of the new owner. |

#### SceneUpdate, CursorUpdate, TextUpdate, SceneInitRequest, SceneInitResponse, CredentialsChanged, TitleChanged

These are relayed as-is from the originating client to all other peers in the room. See the [Client → Server](#client--server-collabaction) section for field details. The `fromClientId` envelope field identifies the sender.

---

### Shared Message Types

#### PeerInfo

Describes a connected peer.

| Field | Type | Description |
|-------|------|-------------|
| `clientId` | string | Relay-assigned UUID. |
| `username` | string | Display name. |
| `avatarUrl` | string | Profile picture URL. |
| `clientType` | string | `"browser"`, `"cli"`, `"api"`. |
| `isActive` | bool | Online status. |
| `isOwner` | bool | `true` if this peer owns the session. |
| `metadata` | map | Application-defined key-value pairs (from JoinRoom). |

---

## Message Flows

### Join a Session

```
Client A                          Relay                          Client B
   │                                │                                │
   │── CollabAction { join: {...} }──▶                               │
   │                                │                                │
   │◀── CollabEvent { roomJoined }──│                                │
   │    (clientId, room: {          │                                │
   │      sessionId, peers, ...})   │                                │
   │                                │── CollabEvent { peerJoined }──▶│
   │                                │   (peer: {A's info})           │
```

The joining client receives `RoomJoined` with existing peers listed. Existing peers each receive a `PeerJoined` broadcast.

### Send an Update

```
Client A                          Relay                          Client B
   │                                │                                │
   │── { sceneUpdate: {elements} }──▶                               │
   │                                │── { sceneUpdate: {elements} }──▶
   │                                │   fromClientId: A              │
```

Updates are broadcast to all peers **except** the sender. The relay adds `fromClientId` so receivers know the origin.

### Cursor Broadcasting

```
Client A                          Relay                          Client B
   │                                │                                │
   │── { cursorUpdate: {x,y,...} }──▶                               │
   │                                │── { cursorUpdate: {x,y,...} }──▶
```

Same pattern as updates. Clients typically throttle outgoing cursor updates to ~50ms.

### Scene Init (New Joiner)

When a new peer joins a non-empty room, they need the current scene state:

```
Client C (new)                    Relay                   Client A (existing)
   │                                │                                │
   │── { sceneInitRequest: {} } ────▶                               │
   │                                │── { sceneInitRequest: {} } ───▶│
   │                                │                                │
   │                                │◀── { sceneInitResponse:       │
   │                                │      {payload: "..."} } ──────│
   │◀── { sceneInitResponse:       │                                │
   │      {payload: "..."} } ──────│                                │
```

### Leave / Disconnect

**Graceful leave:**
```
Client A                          Relay                          Client B
   │                                │                                │
   │── { leave: {reason} } ─────────▶                               │
   │                                │── { peerLeft: {A, count} } ───▶│
```

**Ungraceful disconnect (tab close, network drop):**
The relay's `watchClose` goroutine detects WebSocket context cancellation and automatically sends a synthetic `LeaveRoom` action, so peers still receive `PeerLeft`.

### Owner Lifecycle

When the owner disconnects:

1. **Same-browser tab exists** (matching `browserId`):
   - Ownership transfers to that tab.
   - All peers receive `OwnerChanged { newOwnerClientId }`.

2. **No same-browser successor**:
   - All peers receive `SessionEnded { reason: "owner_disconnected" }`.
   - All client channels are closed, room is deleted.

```
Owner disconnects                 Relay                    Remaining peers
   │                                │                                │
   │── [WebSocket closes] ──────────▶                               │
   │                                │  (no same-browser tab found)   │
   │                                │── SessionEnded ───────────────▶│
   │                                │  (close all channels)          │
   │                                │  (delete room)                 │
```

### Encrypted Rooms

1. Owner sends `JoinRoom` with `encrypted: true` and `protocolVersion: 2`.
2. Relay marks the room as encrypted.
3. Joiners with `protocolVersion < 2` receive `ErrorEvent { code: "PROTOCOL_VERSION_TOO_OLD" }`.
4. Joiners with `protocolVersion >= 2` receive `RoomJoined { encrypted: true }` and must derive the encryption key client-side (the relay never sees the password or key).
5. Encrypted fields (`ElementUpdate.data`, `TextUpdate.text`, `SceneInitResponse.payload`) contain base64-encoded ciphertext instead of plaintext JSON.

### Title Sync

1. Owner includes `title` in `JoinRoom`. Relay stores it.
2. New joiners receive the title in `RoomJoined.title`.
3. Owner renames: sends `TitleChanged { title: "New Name" }`. Relay updates stored title and broadcasts to peers.
4. `GetRoom` REST API also returns the current `title`.

### Credentials Change

1. Owner broadcasts `CredentialsChanged { reason: "password_changed" }`.
2. All peers receive the event and must re-derive their encryption key.
3. If `reason` is `"password_removed"`, the relay marks the room as unencrypted.

---

## Server Configuration

### Rate Limiting

Connection-level rate limits are configured via `RateLimitConfig`:

```go
app := server.NewRelayApp()
app.RateLimit = server.RateLimitConfig{
    GlobalConnPerSec: 100,          // Max WebSocket connections/sec globally
    PerIPConnPerSec:  5,            // Max connections/sec per IP address
    PerIPBurst:       3,            // Burst allowance per IP
    IPLimiterTTL:     5 * time.Minute, // Cleanup interval for stale per-IP limiters
}
```

Per-client message rate limiting (applied after WebSocket upgrade):

```go
// Configured via StreamConfig in the WebSocket handler
cfg := server.StreamConfig{
    MaxMessageRate: 30,         // Messages/sec per client (0 = unlimited)
    MaxMessageSize: 1 << 20,    // 1 MB max message payload
}
```

`JoinRoom` and `LeaveRoom` are always exempt from per-client rate limiting.

### Room Limits

```go
app.Service.MaxPeersPerRoom = 10  // 0 = unlimited
```

When a room is full, the joining client receives `ErrorEvent { code: "ROOM_FULL" }` instead of a stream error, so the client can display a friendly message.

### Payload Logging

For debugging E2EE or verifying that encrypted data is opaque:

```bash
RELAY_LOG_PAYLOADS=200 ./relay
```

Or programmatically:

```go
app.Service.LogPayloads = 200  // Log first 200 chars of content payloads
```

Logs the first N characters of `SceneUpdate.elements[].data`, `TextUpdate.text`, and `SceneInitResponse.payload`.

### CORS

The relay uses origin-aware CORS that reflects allowed origins instead of `Access-Control-Allow-Origin: *`.

```bash
# Allow specific origins (comma-separated)
RELAY_ALLOWED_ORIGINS=excaliframe.com,*.excaliframe.com,localhost
```

| Origin config | Behavior |
|---------------|----------|
| Not set / empty | Reflects any `Origin` header (allow-all, suitable for development) |
| Set | Only reflects origins matching the allowlist; disallowed origins get no CORS headers |

Supports exact domains (`excaliframe.com`), wildcard subdomains (`*.excaliframe.com`), and `localhost` (matches any port, including `127.0.0.1`).

Preflight `OPTIONS` requests return `204 No Content` with appropriate headers. Non-preflight requests from disallowed origins still execute (CORS is advisory — the browser enforces).

### Trusted Proxies

When deploying behind a reverse proxy (Caddy, nginx, etc.), configure trusted proxies so the relay uses the real client IP for rate limiting instead of the proxy IP:

```bash
# Trust Docker bridge network and localhost
RELAY_TRUSTED_PROXIES=172.17.0.0/16,127.0.0.1,::1
```

The relay checks `X-Forwarded-For` and `X-Real-IP` headers only when `RemoteAddr` is from a trusted CIDR. This prevents direct clients from spoofing their IP to bypass rate limits. If not set, all proxies are trusted (backwards-compatible for single-proxy deployments).

---

## Embedding in Another Server

Massrelay implements `http.Handler` and is designed to be embedded as a sub-handler:

```go
import relayserver "github.com/panyam/massrelay/web/server"

// Create and initialize the relay
relayApp := relayserver.NewRelayApp()
relayApp.Service.MaxPeersPerRoom = 20  // customize before Init()
relayApp.Init()

// Mount at /relay/ in your existing mux
mux.Handle("/relay/", http.StripPrefix("/relay", relayApp))
```

This gives you:
- `ws://yourserver/relay/ws/v1/{session_id}/sync` — WebSocket endpoint
- `http://yourserver/relay/api/v1/rooms/{session_id}` — REST query
- `http://yourserver/relay/health` — Health check

---

## TypeScript Client

### Installation

```bash
npm install @panyam/massrelay
```

Peer dependencies:
- `@bufbuild/protobuf ^2.0.0`
- `@panyam/servicekit-client ^0.0.6`

### Package Exports

| Import Path | Contents |
|-------------|----------|
| `@panyam/massrelay/client` | `CollabClient` class |
| `@panyam/massrelay/sync` | `SyncAdapter` interface, `SyncActions`, `SyncState`, `SyncConnection`, `OutgoingUpdate`, `CursorData`, `PeerCursor` |
| `@panyam/massrelay/url` | `parseConnectParam`, `resolveRelayUrl`, `buildConnectUrl`, `encodeJoinCode`, `decodeJoinCode` |
| `@panyam/massrelay/models` | Generated protobuf types (`PeerInfo`, etc.) |
| `@panyam/massrelay/services` | Generated service definitions |

The package is ESM-only (`"type": "module"`). Relative imports within massrelay use `.js` extensions.

### CollabClient

Framework-agnostic WebSocket client. No React dependency — can be used from any JavaScript runtime.

#### Constructor

```typescript
const client = new CollabClient(options?: CollabClientOptions);
```

**CollabClientOptions:**

| Option | Type | Description |
|--------|------|-------------|
| `onEvent` | `(event: any) => void` | Called for every event from the relay. Receives the raw protobuf JSON. |
| `onConnect` | `(clientId: string) => void` | Fires after `RoomJoined` is received. |
| `onDisconnect` | `() => void` | Fires on connection close (graceful or ungraceful). |
| `onPeerJoined` | `(peer: PeerInfo) => void` | New peer joined the room. Also fires for self on connect. |
| `onPeerLeft` | `(clientId: string) => void` | Peer left the room. |
| `onError` | `(error: Error) => void` | WebSocket-level error. |
| `onErrorEvent` | `(code: string, message: string) => void` | Relay-sent graceful error (`ROOM_FULL`, `PROTOCOL_VERSION_TOO_OLD`). |
| `onSessionEnded` | `(reason: string) => void` | Session terminated by relay (owner left). |
| `onOwnerChanged` | `(newOwnerClientId: string) => void` | Ownership transferred. |
| `onCredentialsChanged` | `(reason: string) => void` | Session password changed. |
| `onTitleChanged` | `(title: string) => void` | Drawing title changed. |
| `maxRetries` | number | Max auto-reconnect attempts (default: `5`). Currently unused — auto-reconnect is disabled. |
| `_grpcFactory` | `() => GRPCWSClient` | Override WebSocket client for testing. |

#### Properties (read-only)

| Property | Type | Description |
|----------|------|-------------|
| `clientId` | string | Relay-assigned client ID (empty before connect). |
| `sessionId` | string | Active session ID. |
| `isConnected` | boolean | `true` after `RoomJoined` received. |
| `isConnecting` | boolean | `true` while WebSocket handshake is in progress. |
| `isOwner` | boolean | `true` if this client owns the session. |
| `maxPeers` | number | Room's max participant limit (from relay). |
| `roomEncrypted` | boolean | `true` if the room has E2EE enabled. |
| `title` | string | Current drawing title. |

#### Methods

**`connect(relayUrl, sessionId, username, metadata, isOwner?, browserId?, clientHint?, encrypted?, title?)`**

Opens a WebSocket to the relay and sends a `JoinRoom` action.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `relayUrl` | string | (required) | Relay URL. Can be relative (e.g. `"/relay"`) — resolved via `resolveRelayUrl()`. |
| `sessionId` | string | (required) | Session to join. Empty string = create new session. |
| `username` | string | (required) | Display name. If empty, generates `"Anon-xxxx"`. |
| `metadata` | Record\<string, string\> | (required) | Application-defined key-value pairs. |
| `isOwner` | boolean | `false` | Whether this client owns the session. |
| `browserId` | string | `""` | Browser UUID for ownership transfer. |
| `clientHint` | string | `""` | Session reuse hint. |
| `encrypted` | boolean | `false` | Declare room as encrypted. |
| `title` | string | `""` | Drawing title. |

Throws if already connected.

**`disconnect()`**

Sends `LeaveRoom`, closes the WebSocket, resets all state, and fires `onDisconnect`. Safe to call multiple times.

**`send(action)`**

Sends a `CollabAction` to the relay. Automatically adds `clientId` and `timestamp`.

```typescript
client.send({
  sceneUpdate: {
    elements: [{ id: 'el1', version: 2, data: '...' }]
  }
});
```

Throws if not connected.

#### Behavior Notes

- **Page unload**: Automatically calls `disconnect()` on `beforeunload` to ensure graceful leave.
- **Self-as-peer**: On connect, `onPeerJoined` fires with the client's own info first, then existing peers.
- **Auto-reconnect**: Currently **disabled** to prevent phantom sessions after server restart. The `maxRetries` option is reserved for future use.
- **Error events**: `ROOM_FULL` and `PROTOCOL_VERSION_TOO_OLD` are delivered via `onErrorEvent` (not `onError`). The client sets `explicitDisconnect = true` so it won't attempt reconnection.

---

### SyncAdapter Interface

Tool-agnostic interface for editor-specific sync logic. Each editor tool (Excalidraw, Mermaid, etc.) implements this to provide diffing, merging, and cursor tracking. The sync orchestration layer handles timing (debounce, throttle) and routing without knowing the tool's data model.

```typescript
import type { SyncAdapter, OutgoingUpdate, CursorData, PeerCursor } from '@panyam/massrelay/sync';

interface SyncAdapter {
  readonly metadata: Record<string, string>;

  // Outgoing: compute diff since last sync. Return null if nothing changed.
  computeOutgoing(): OutgoingUpdate | null;

  // Incoming: apply a remote update, handling conflicts internally.
  applyRemote(fromClientId: string, payload: Record<string, unknown>): void;

  // Full scene snapshot for new joiners.
  getSceneSnapshot(): string;

  // Apply full scene received from an existing peer.
  applySceneInit(payload: string): void;

  // Cursor tracking.
  getCursorData(): CursorData | null;
  applyRemoteCursor(peer: PeerCursor): void;
  removePeerCursor(clientId: string): void;
}
```

**SyncActions** — returned by the sync orchestration hook:

```typescript
interface SyncActions {
  notifyLocalChange(): void;   // Call on every editor onChange. Resets debounce timer.
  notifyCursorMove(): void;    // Call on pointer move. Throttled internally.
  handleEvent(event: any): void; // Route incoming relay event to the adapter.
}
```

**SyncConnection** — minimal connection info the sync layer needs:

```typescript
interface SyncConnection {
  isConnected: boolean;
  clientId: string;
  isOwner: boolean;
  peers: Map<string, unknown>;
  send: (msg: Record<string, unknown>) => void;
}
```

---

### URL Helpers

```typescript
import {
  parseConnectParam,
  resolveRelayUrl,
  buildConnectUrl,
  encodeJoinCode,
  decodeJoinCode,
} from '@panyam/massrelay/url';
```

#### `parseConnectParam(search?: string): string | null`

Extract `?connect=<relay-url>` from a query string.

```typescript
parseConnectParam('?connect=ws://relay.example.com')
// → 'ws://relay.example.com'

parseConnectParam('?foo=bar')
// → null
```

#### `resolveRelayUrl(relayUrl: string): string`

Convert a potentially relative relay URL to a full WebSocket URL.

```typescript
// On page https://example.com/editor
resolveRelayUrl('/relay')
// → 'wss://example.com/relay'

resolveRelayUrl('ws://custom:8787')
// → 'ws://custom:8787' (unchanged)
```

#### `buildConnectUrl(pageUrl: string, relayUrl: string): string`

Append `?connect=<relayUrl>` to a page URL for sharing.

```typescript
buildConnectUrl('https://example.com/editor', 'ws://relay:8787')
// → 'https://example.com/editor?connect=ws%3A%2F%2Frelay%3A8787'
```

#### `encodeJoinCode(relayUrl: string, sessionId: string): string`

Create a join code for cross-origin sharing: `base64url(relayUrl):sessionId`.

```typescript
encodeJoinCode('wss://relay.example.com/relay', 'abc-123')
// → 'd3NzOi8vcmVsYXkuZXhhbXBsZS5jb20vcmVsYXk:abc-123'
```

#### `decodeJoinCode(code: string): { relayUrl: string; sessionId: string } | null`

Decode a join code back to relay URL and session ID. Returns `null` if malformed.

```typescript
decodeJoinCode('d3NzOi8vcmVsYXkuZXhhbXBsZS5jb20vcmVsYXk:abc-123')
// → { relayUrl: 'wss://relay.example.com/relay', sessionId: 'abc-123' }

decodeJoinCode('invalid')
// → null
```

---

## Go API

### CollabService

The core service that manages rooms and peer lifecycle.

```go
import "github.com/panyam/massrelay/services"

svc := services.NewCollabService()
svc.MaxPeersPerRoom = 10    // default
svc.ProtocolVersion = 2     // default
svc.LogPayloads = 0         // 0 = disabled
```

**Key methods:**

| Method | Description |
|--------|-------------|
| `HandleAction(ctx, *CollabAction) (*CollabEvent, error)` | Process a client action. Routes to join/leave/broadcast handlers. Returns a response event (or nil for broadcasts). |
| `GetOrCreateRoom(sessionId) *CollabRoom` | Get or create a room by session ID. Thread-safe. |
| `GetRoom(ctx, *GetRoomRequest) (*GetRoomResponse, error)` | Query room info (used by REST endpoint). |
| `ListRooms(ctx, *ListRoomsRequest) (*ListRoomsResponse, error)` | List all active rooms. |
| `FindSessionByHint(hint) string` | Resolve a client hint to a session ID. |
| `GetClientSendCh(sessionId, clientId) chan *CollabEvent` | Get a client's broadcast channel. |

### CollabRoom

In-memory room holding connected peers.

```go
type CollabRoom struct {
    SessionId      string
    Clients        map[string]*CollabClient
    Created        time.Time
    OwnerClientId  string
    OwnerBrowserId string
    Metadata       map[string]string  // application-defined key-value pairs
    Title          string  // drawing title
    Encrypted      bool    // E2EE enabled
}
```

**Methods:**

| Method | Description |
|--------|-------------|
| `AddClient(client)` | Add a peer. |
| `RemoveClient(clientId) *CollabClient` | Remove a peer, return it (or nil). |
| `GetPeerInfo() map[string]*PeerInfo` | Snapshot of all peers, keyed by client ID. |
| `ClientCount() int` | Number of connected peers. |
| `IsEmpty() bool` | True if no clients. |
| `BroadcastToAll(event)` | Non-blocking send to all. Drops if channel full. |
| `BroadcastExcept(event, exceptId)` | Broadcast to all except one peer. |
| `FindByBrowserId(browserId, excludeId) *CollabClient` | Find a same-browser tab. |
| `CloseAllClients()` | Close all channels and remove all clients. |

### RelayApp

HTTP application implementing `http.Handler`.

```go
import "github.com/panyam/massrelay/web/server"

app := server.NewRelayApp()
app.Service.MaxPeersPerRoom = 20
app.RateLimit = server.RateLimitConfig{...}
app.Init()

// Standalone
http.ListenAndServe(":8787", app)

// Embedded
mux.Handle("/relay/", http.StripPrefix("/relay", app))
```

---

## Wire Format

Messages are sent over WebSocket using the [servicekit](https://github.com/panyam/servicekit) envelope protocol:

```json
{"type": "data", "data": {"join": {"sessionId": "", "username": "Alice", ...}}}
```

The inner `data` field is the protobuf JSON representation with **camelCase** field names (standard protobuf JSON mapping). The Go server deserializes with `protojson.Unmarshal`.

**Field naming convention:**

| Proto definition | JSON wire format |
|-----------------|-----------------|
| `session_id` | `sessionId` |
| `client_id` | `clientId` |
| `is_owner` | `isOwner` |
| `room_joined` | `roomJoined` |
| `scene_update` | `sceneUpdate` |

**Oneof handling:** The active oneof field appears as a top-level key in the JSON object:

```json
// CollabAction with SceneUpdate
{
  "sceneUpdate": { "elements": [...] },
  "clientId": "uuid",
  "timestamp": 1710000000000
}

// CollabEvent with RoomJoined
{
  "roomJoined": { "clientId": "uuid", "room": { "sessionId": "uuid", "peers": {"peer-uuid": {...}} } },
  "eventId": "uuid",
  "fromClientId": "uuid",
  "serverTimestamp": 1710000000
}
```

---

## Security

- **No authentication**: The relay does not authenticate clients. Anyone with a session ID can join. Applications should implement access control at a higher layer.
- **No persistence**: No data is written to disk. Room state exists only in memory.
- **Optional E2EE**: Rooms can be declared encrypted by the owner. The relay enforces protocol version checks but never sees plaintext. Encryption/decryption is entirely client-side (AES-256-GCM with PBKDF2 key derivation is the recommended scheme).
- **Rate limiting**: Multi-layer rate limiting protects against connection floods and message spam (global + per-IP + per-client).
- **Participant limits**: Configurable per-room limits prevent resource exhaustion.
- **Origin allowlist**: WebSocket and CORS share the same origin checker (`RELAY_ALLOWED_ORIGINS`). Disallowed origins are rejected at WebSocket upgrade and receive no CORS headers.
- **Trusted proxy / anti-spoofing**: `RELAY_TRUSTED_PROXIES` controls which reverse proxies can set `X-Forwarded-For`. Direct clients cannot spoof their IP to bypass rate limits.
- **CORS**: Reflects allowed origins (not `*`). Disallowed origins get no CORS headers.
- **Panic recovery**: Middleware catches handler panics, logs a stack trace, and returns HTTP 500 — keeps the server alive.
- **Server timeouts**: `ReadHeaderTimeout` (10s, slowloris defense), `IdleTimeout` (120s), `MaxHeaderBytes` (64KB). `ReadTimeout`/`WriteTimeout` are intentionally not set to avoid killing long-lived WebSocket connections.
- **WebSocket keepalive**: servicekit sends 30s pings with a 5-minute pong timeout to detect dead connections.
- **Zombie cleanup**: Server-side `watchClose` goroutine detects ungraceful disconnects and cleans up.

---

## Limitations

- **No persistence** — Rooms exist only in memory. Server restart terminates all sessions.
- **Single-instance** — No clustering or room distribution. Suitable for single-server deployments.
- **No authentication** — Session IDs are the only access control. Applications must implement auth separately.
- **No auto-reconnect** — Currently disabled in the client to prevent phantom sessions. Clients must manually re-join.
- **Ownership model** — Ownership only transfers to same-browser tabs (matching `browserId`). Cross-browser ownership transfer is not supported.
- **Broadcast ordering** — Broadcasts are async and non-blocking. If a client's channel buffer (64 events) is full, events are dropped silently.
- **ListRooms not exposed** — The endpoint exists in code but is not registered, to prevent session enumeration.
