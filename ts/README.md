# @panyam/massrelay

TypeScript client for the [Massrelay](https://github.com/panyam/massrelay) real-time collaboration relay server.

## Installation

```bash
npm install @panyam/massrelay
```

**Peer dependencies:**
- `@bufbuild/protobuf ^2.0.0`
- `@panyam/servicekit-client ^0.0.6`

## Exports

| Import Path | Contents |
|-------------|----------|
| `@panyam/massrelay/client` | `CollabClient` — WebSocket client for the relay |
| `@panyam/massrelay/sync` | `SyncAdapter` interface, `SyncActions`, `SyncConnection`, and related types |
| `@panyam/massrelay/url` | URL helpers: `resolveRelayUrl`, `encodeJoinCode`, `decodeJoinCode`, etc. |
| `@panyam/massrelay/models` | Generated protobuf types (`PeerInfo`, `CollabAction`, `CollabEvent`, etc.) |
| `@panyam/massrelay/services` | Generated service definitions |

This is an ESM package (`"type": "module"`).

## Quick Start

```typescript
import { CollabClient } from '@panyam/massrelay/client';

const client = new CollabClient({
  onConnect: (clientId) => console.log('Connected as', clientId),
  onPeerJoined: (peer) => console.log('Peer joined:', peer.username),
  onPeerLeft: (clientId) => console.log('Peer left:', clientId),
  onEvent: (event) => { /* handle scene/text/cursor updates */ },
  onDisconnect: () => console.log('Disconnected'),
  onErrorEvent: (code, msg) => console.error('Relay error:', code, msg),
});

// Join or create a session
client.connect(
  'ws://localhost:8787',         // relayUrl (or relative path like "/relay")
  '',                            // sessionId (empty = relay generates one)
  'Alice',                       // username
  { tool: 'whiteboard' },        // metadata (application-defined key-value pairs)
  true,                          // isOwner
);

// Send updates to peers
client.send({
  sceneUpdate: {
    elements: [
      { id: 'rect1', version: 1, data: '{"type":"rectangle",...}' }
    ]
  }
});

// Disconnect
client.disconnect();
```

## SyncAdapter

Implement this interface to integrate your editor with massrelay's sync layer:

```typescript
import type { SyncAdapter } from '@panyam/massrelay/sync';

class MyEditorSyncAdapter implements SyncAdapter {
  readonly metadata = { tool: 'my-editor' };

  computeOutgoing() { /* diff local changes */ }
  applyRemote(fromClientId, payload) { /* merge remote changes */ }
  getSceneSnapshot() { /* full state for new joiners */ }
  applySceneInit(payload) { /* load full state from peer */ }
  getCursorData() { /* current cursor position */ }
  applyRemoteCursor(peer) { /* show remote cursor */ }
  removePeerCursor(clientId) { /* hide disconnected cursor */ }
}
```

## URL Helpers

```typescript
import { resolveRelayUrl, encodeJoinCode, decodeJoinCode } from '@panyam/massrelay/url';

// Resolve relative relay URL to WebSocket URL
resolveRelayUrl('/relay')  // → 'wss://currenthost/relay'

// Create shareable join codes
const code = encodeJoinCode('wss://relay.example.com', 'session-123');
const parsed = decodeJoinCode(code);
// → { relayUrl: 'wss://relay.example.com', sessionId: 'session-123' }
```

## Full Documentation

See the [main README](https://github.com/panyam/massrelay#readme) for complete protocol reference, server configuration, message flows, and Go API docs.
