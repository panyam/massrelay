import { describe, it, expect, vi, beforeEach } from 'vitest';
import { CollabEngine } from './CollabEngine.js';
import type { CollabEngineState, TimerProvider } from './CollabEngine.js';
import { CollabClient } from './CollabClient.js';
import type { SyncAdapter, OutgoingUpdate, CursorData, PeerCursor } from './SyncAdapter.js';
import { deriveKey, encryptPayload, decryptPayload } from './crypto.js';
import type { CollabEventJson, PeerInfoJson } from './gen/massrelay/v1/models/collab_pb.js';

/**
 * COLLAB TEST COVERAGE MATRIX
 *
 * User Flow                        | Unit Test (this file)            | E2E Test                                          | Browser-only (not tested here)
 * ---------------------------------|----------------------------------|---------------------------------------------------|-------------------------------
 * Owner starts sharing             | lifecycle: connect → RoomJoined  | test_share_owner::test_start_sharing                | SharePanel DOM, toolbar click
 * Follower joins session           | scene init: request + response   | test_join_follower::test_join_via_link               | JoinPage form, IndexedDB seed
 * Owner draws → follower sees      | two-peer sync: owner→follower    | test_collab_sync::test_owner_draws_follower_sees     | Canvas render, Excalidraw API
 * Follower draws → owner sees      | two-peer sync: follower→owner    | test_collab_sync::test_follower_draws_owner_sees     | Canvas render, Excalidraw API
 * Cursor visible across peers      | cursor round-trip                | test_collab_cursors::test_owner_sees_follower_cursor | Excalidraw collaborator overlay
 * Encrypted sharing + join         | encryption: round-trip, wrong key| test_encryption::test_encrypted_share_and_join       | Password input DOM, JoinPage
 * Owner stops sharing              | session ended                    | test_collab_sync::test_stop_sharing_disconnects_*    | SharePanel reverts to "Share"
 * Owner disconnect → transfer      | owner changed                   | (none)                                              | Tab detection, localStorage
 * Password change mid-session      | credentials changed              | (none)                                              | Reconnect prompt UI
 * Room full rejection              | error events: ROOM_FULL         | (none)                                              | Error message toast
 * Mermaid text sync                | text sync round-trip             | (none)                                              | Textarea render, onChange
 * Room title sync                  | title changed                   | (none)                                              | Title input, header update
 * Simultaneous edits               | concurrent edits                 | (none)                                              | Visual conflict resolution
 * 3+ peer owner election           | owner election                  | (none)                                              | N/A (transparent to user)
 * Late adapter (async editor load) | late-bound adapter               | (none)                                              | Excalidraw chunk loading
 * Debounce during drag             | debounce batching               | (none)                                              | Drag performance
 * Cursor throttle during fast move | cursor throttling               | (none)                                              | Pointer event frequency
 * Peer list updates                | peer tracking                   | test_collab_sync::test_peer_count_updates            | Badge DOM, colored dots
 * Cross-browser compatibility      | (N/A — protocol-level)          | test_cross_browser_collab::test_cross_browser_sync   | Firefox rendering
 *
 * Browser-only concerns NOT tested here:
 * - DOM rendering (SharePanel, CollabBadge, JoinPage, FloatingToolbar)
 * - Excalidraw canvas rendering and updateScene() visual output
 * - IndexedDB persistence (drawing storage)
 * - Clipboard API (copy join link)
 * - WebSocket transport (real GRPCWSClient)
 * - React component lifecycle (mount/unmount/rerender)
 * - localStorage/sessionStorage side effects
 *
 * These are covered by E2E tests (Playwright) and excaliframe unit tests (jsdom).
 */

// ─── Immediate timers for deterministic tests ───

function makeImmediateTimers(): TimerProvider & { flush(): void; pending: Array<() => void> } {
  const pending: Array<() => void> = [];
  return {
    pending,
    setTimeout(fn: () => void, _ms: number): number {
      pending.push(fn);
      return pending.length - 1;
    },
    clearTimeout(handle: unknown): void {
      const idx = handle as number;
      if (idx >= 0 && idx < pending.length) {
        pending[idx] = () => {}; // noop
      }
    },
    flush() {
      // Run all pending, drain queue
      while (pending.length > 0) {
        const fns = pending.splice(0);
        for (const fn of fns) fn();
      }
    },
  };
}

// ─── Mock SyncAdapter ───

function makeMockAdapter(metadata: Record<string, string> = { tool: 'excalidraw' }): SyncAdapter & {
  _outgoing: OutgoingUpdate | null;
  _cursorData: CursorData | null;
} {
  return {
    metadata,
    _outgoing: null,
    _cursorData: null,
    computeOutgoing() { return this._outgoing; },
    applyRemote: vi.fn(),
    getSceneSnapshot: vi.fn(() => '{"elements":[]}'),
    applySceneInit: vi.fn(),
    getCursorData() { return this._cursorData; },
    applyRemoteCursor: vi.fn(),
    removePeerCursor: vi.fn(),
  };
}

// ─── Mock CollabClient ───

function makeMockClient(): CollabClient & {
  _send: ReturnType<typeof vi.fn>;
  _connect: ReturnType<typeof vi.fn>;
  _disconnect: ReturnType<typeof vi.fn>;
  simulateRoomJoined(data: Record<string, any>): void;
  simulatePeerJoined(peer: PeerInfoJson): void;
  simulatePeerLeft(clientId: string): void;
  simulateEvent(event: CollabEventJson): void;
  simulateDisconnect(): void;
  simulateSessionEnded(reason?: string): void;
  simulateOwnerChanged(newOwnerClientId: string): void;
  simulateCredentialsChanged(reason: string): void;
  simulateTitleChanged(title: string): void;
  simulateErrorEvent(code: string, message: string): void;
  simulateError(message: string): void;
} {
  const sendFn = vi.fn();
  const connectFn = vi.fn();
  const disconnectFn = vi.fn();

  // Construct a real client but override its methods
  const client = new CollabClient();

  // Override connect to avoid real WS
  (client as any).connect = connectFn.mockImplementation(
    (relayUrl: string, sessionId: string, username: string, metadata: Record<string, string>, isOwner: boolean) => {
      (client as any)._relayUrl = relayUrl;
      (client as any)._sessionId = sessionId;
      (client as any)._username = username || 'Anon-test';
      (client as any)._metadata = metadata;
      (client as any)._isOwner = isOwner;
      (client as any)._isConnecting = true;
      (client as any).explicitDisconnect = false;
    }
  );

  // Override send
  (client as any).send = sendFn.mockImplementation((action: any) => {
    // no-op, just record
  });

  // Override disconnect
  (client as any).disconnect = disconnectFn.mockImplementation(() => {
    const wasConnected = (client as any)._isConnected;
    (client as any)._isConnected = false;
    (client as any)._isConnecting = false;
    (client as any)._clientId = '';
    (client as any)._isOwner = false;
    if (wasConnected) {
      client.options.onDisconnect?.();
    }
  });

  const mock = client as any;
  mock._send = sendFn;
  mock._connect = connectFn;
  mock._disconnect = disconnectFn;

  mock.simulateRoomJoined = (data: Record<string, any>) => {
    mock._clientId = data.clientId || '';
    mock._sessionId = data.sessionId || mock._sessionId;
    mock._isConnected = true;
    mock._isConnecting = false;
    client.options.onConnect?.(data.clientId);

    // Self as peer
    const selfPeer: PeerInfoJson = {
      clientId: data.clientId,
      username: mock._username,
      avatarUrl: '',
      clientType: 'browser',
      isActive: true,
      metadata: mock._metadata || {},
    };
    client.options.onPeerJoined?.(selfPeer);

    // Existing peers
    if (data.peers) {
      for (const peer of data.peers) {
        client.options.onPeerJoined?.(peer);
      }
    }

    // onEvent with roomJoined (Room fields nested under .room)
    // peers is now a map keyed by clientId (matching proto wire format)
    const peersMap: { [key: string]: PeerInfoJson } = {};
    for (const p of data.peers ?? []) {
      peersMap[p.clientId] = p;
    }
    client.options.onEvent?.({
      roomJoined: {
        clientId: data.clientId,
        maxPeers: data.maxPeers ?? 0,
        room: {
          sessionId: data.sessionId || mock._sessionId,
          ownerClientId: data.ownerClientId || '',
          encrypted: data.encrypted ?? false,
          title: data.title ?? '',
          peers: peersMap,
        },
      },
    });
  };

  mock.simulatePeerJoined = (peer: PeerInfoJson) => {
    client.options.onPeerJoined?.(peer);
  };

  mock.simulatePeerLeft = (clientId: string) => {
    client.options.onPeerLeft?.(clientId);
    // Also fire onEvent with peerLeft (as useSync expects)
    client.options.onEvent?.({ peerLeft: { clientId } });
  };

  mock.simulateEvent = (event: CollabEventJson) => {
    client.options.onEvent?.(event);
  };

  mock.simulateDisconnect = () => {
    mock._isConnected = false;
    mock._isConnecting = false;
    client.options.onDisconnect?.();
  };

  mock.simulateSessionEnded = (reason = '') => {
    client.options.onSessionEnded?.(reason);
  };

  mock.simulateOwnerChanged = (newOwnerClientId: string) => {
    mock._isOwner = newOwnerClientId === mock._clientId;
    client.options.onOwnerChanged?.(newOwnerClientId);
  };

  mock.simulateCredentialsChanged = (reason: string) => {
    client.options.onCredentialsChanged?.(reason);
  };

  mock.simulateTitleChanged = (title: string) => {
    client.options.onTitleChanged?.(title);
  };

  mock.simulateErrorEvent = (code: string, message: string) => {
    client.options.onErrorEvent?.(code, message);
  };

  mock.simulateError = (message: string) => {
    client.options.onError?.(new Error(message));
  };

  return mock;
}

// ─── Mock Relay (wires two clients together) ───

function makeMockRelay(
  clientA: ReturnType<typeof makeMockClient>,
  clientB: ReturnType<typeof makeMockClient>,
) {
  // Route sends from A to B's onEvent, and vice versa
  clientA._send.mockImplementation((action: any) => {
    const event = { ...action, fromClientId: (clientA as any)._clientId };
    clientB.options.onEvent?.(event);
  });
  clientB._send.mockImplementation((action: any) => {
    const event = { ...action, fromClientId: (clientB as any)._clientId };
    clientA.options.onEvent?.(event);
  });
}

// ─── Tests ───

describe('CollabEngine', () => {
  let timers: ReturnType<typeof makeImmediateTimers>;

  beforeEach(() => {
    timers = makeImmediateTimers();
  });

  /**
   * @flow User opens Share → "Connecting..." → "Sharing Active" → Stop Sharing
   * @browser SharePanel state indicators, toolbar click interactions
   * @e2e test_share_owner.py::test_start_sharing, test_share_owner.py::test_stop_sharing
   */
  describe('lifecycle', () => {
    it('starts in disconnected state', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });
      expect(engine.state.phase).toBe('disconnected');
      expect(engine.state.isInitialized).toBe(false);
    });

    it('transitions to connecting on connect()', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });
      const states: string[] = [];
      engine.on('stateChange', (s) => states.push(s.phase));

      engine.connect({ relayUrl: 'ws://test', sessionId: '', username: 'Alice', metadata: { tool: 'excalidraw' } });
      expect(engine.state.phase).toBe('connecting');
    });

    it('transitions to connected on RoomJoined', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      expect(engine.state.phase).toBe('connected');
      expect(engine.state.clientId).toBe('c1');
      expect(engine.state.sessionId).toBe('sess1');
    });

    it('transitions back to disconnected on disconnect()', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });
      engine.disconnect();

      expect(engine.state.phase).toBe('disconnected');
      expect(engine.state.clientId).toBe('');
    });

    it('dispose() disconnects and removes all listeners', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });
      const listener = vi.fn();
      engine.on('stateChange', listener);

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      listener.mockClear();
      engine.dispose();

      // Listener should not be called after dispose
      expect(engine.state.phase).toBe('disconnected');
    });
  });

  /**
   * @flow Owner draws rectangle → follower sees it (and vice versa)
   * @browser Canvas rendering via Excalidraw updateScene() / textarea onChange
   * @e2e test_collab_sync.py::test_owner_draws_follower_sees, test_collab_sync.py::test_follower_draws_owner_sees
   */
  describe('two-peer sync', () => {
    it('owner notifyLocalChange flushes to follower adapter', () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers });

      // Connect owner
      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: '', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'owner-1', sessionId: 'sess1', ownerClientId: 'owner-1' });

      // Connect follower
      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({
        clientId: 'follower-1',
        sessionId: 'sess1',
        ownerClientId: 'owner-1',
        peers: [{ clientId: 'owner-1', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      // Wire relay
      makeMockRelay(ownerClient, followerClient);

      // Owner makes a change
      ownerAdapter._outgoing = { type: 'sceneUpdate', payload: { elements: [{ id: 'el-1', data: '{"x":1}' }] } };
      ownerEngine.notifyLocalChange();
      timers.flush();

      // Follower should receive
      expect(followerAdapter.applyRemote).toHaveBeenCalledWith('owner-1', { elements: [{ id: 'el-1', data: '{"x":1}' }] });
    });

    it('follower notifyLocalChange flushes to owner adapter', () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers });

      // Connect owner
      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: '', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'owner-1', sessionId: 'sess1', ownerClientId: 'owner-1' });

      // Connect follower
      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({
        clientId: 'follower-1',
        sessionId: 'sess1',
        ownerClientId: 'owner-1',
        peers: [{ clientId: 'owner-1', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      // Wire relay
      makeMockRelay(ownerClient, followerClient);

      // Follower makes a change
      followerAdapter._outgoing = { type: 'sceneUpdate', payload: { elements: [{ id: 'el-2', data: '{"y":5}' }] } };
      followerEngine.notifyLocalChange();
      timers.flush();

      // Owner should receive
      expect(ownerAdapter.applyRemote).toHaveBeenCalledWith('follower-1', { elements: [{ id: 'el-2', data: '{"y":5}' }] });
    });
  });

  /**
   * @flow Follower joins mid-session, receives existing drawing
   * @browser Canvas populates with elements, Excalidraw updateScene()
   * @e2e test_join_follower.py::test_join_via_link
   */
  describe('scene init', () => {
    it('marks initialized immediately when first peer (no others)', () => {
      const client = makeMockClient();
      const adapter = makeMockAdapter();
      const engine = new CollabEngine({ client, adapter, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      expect(engine.state.isInitialized).toBe(true);
    });

    it('sends sceneInitRequest when joining with existing peers', () => {
      const client = makeMockClient();
      const adapter = makeMockAdapter();
      const engine = new CollabEngine({ client, adapter, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({
        clientId: 'follower-1',
        sessionId: 'sess1',
        ownerClientId: 'owner-1',
        peers: [{ clientId: 'owner-1', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      expect(client._send).toHaveBeenCalledWith({ sceneInitRequest: {} });
    });

    it('responds to sceneInitRequest and follower applies init', () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();
      ownerAdapter.getSceneSnapshot = vi.fn(() => '{"elements":[{"id":"a"}]}');

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers });

      // Owner connects first (alone)
      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'aaa-owner', sessionId: 'sess1', ownerClientId: 'aaa-owner' });

      // Add follower to owner's peer list
      ownerClient.simulatePeerJoined({ clientId: 'bbb-follower', username: 'Follower', avatarUrl: '', clientType: 'browser', isActive: true });

      // Wire relay
      makeMockRelay(ownerClient, followerClient);

      // Follower connects
      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({
        clientId: 'bbb-follower',
        sessionId: 'sess1',
        ownerClientId: 'aaa-owner',
        peers: [{ clientId: 'aaa-owner', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      // sceneInitRequest sent by follower → routed to owner via relay → owner responds
      // Owner should have called getSceneSnapshot
      expect(ownerAdapter.getSceneSnapshot).toHaveBeenCalled();
      // Follower should have applied init
      expect(followerAdapter.applySceneInit).toHaveBeenCalledWith('{"elements":[{"id":"a"}]}');
      expect(followerEngine.state.isInitialized).toBe(true);
    });
  });

  /**
   * @flow 3+ peers — only lowest-ID responds to scene init request (prevents duplicate snapshots)
   * @browser Transparent to user (prevents duplicate scene init responses)
   * @e2e None
   */
  describe('owner election', () => {
    it('lowest clientId among non-requesters responds to sceneInitRequest', () => {
      const client = makeMockClient();
      const adapter = makeMockAdapter();
      adapter.getSceneSnapshot = vi.fn(() => '{"snapshot":true}');

      const engine = new CollabEngine({ client, adapter, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'aaa-self', sessionId: 'sess1', ownerClientId: '' });

      // Add more peers
      client.simulatePeerJoined({ clientId: 'bbb-other', username: 'Bob', avatarUrl: '', clientType: 'browser', isActive: true });
      client.simulatePeerJoined({ clientId: 'ccc-requester', username: 'Charlie', avatarUrl: '', clientType: 'browser', isActive: true });

      client._send.mockClear();

      // ccc-requester sends sceneInitRequest
      client.simulateEvent({ sceneInitRequest: {}, fromClientId: 'ccc-requester' });

      // aaa-self is lowest → should respond
      expect(adapter.getSceneSnapshot).toHaveBeenCalled();
      expect(client._send).toHaveBeenCalledWith({ sceneInitResponse: { payload: '{"snapshot":true}' } });
    });

    it('non-lowest clientId does NOT respond', () => {
      const client = makeMockClient();
      const adapter = makeMockAdapter();

      const engine = new CollabEngine({ client, adapter, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Bob', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'bbb-self', sessionId: 'sess1', ownerClientId: '' });

      client.simulatePeerJoined({ clientId: 'aaa-other', username: 'Alice', avatarUrl: '', clientType: 'browser', isActive: true });
      client.simulatePeerJoined({ clientId: 'ccc-requester', username: 'Charlie', avatarUrl: '', clientType: 'browser', isActive: true });

      client._send.mockClear();

      client.simulateEvent({ sceneInitRequest: {}, fromClientId: 'ccc-requester' });

      // bbb-self is NOT lowest → should NOT respond
      expect(adapter.getSceneSnapshot).not.toHaveBeenCalled();
      const hasSendResponse = client._send.mock.calls.some(
        (call: any) => call[0]?.sceneInitResponse !== undefined,
      );
      expect(hasSendResponse).toBe(false);
    });
  });

  /**
   * @flow Owner enables password → content encrypted on wire → follower decrypts
   * @browser Password input DOM, JoinPage form, sessionStorage transfer
   * @e2e test_encryption.py::test_encrypted_share_and_join
   */
  describe('encryption', () => {
    it('encrypts outgoing and decrypts incoming', async () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();

      const key = await deriveKey('secret', 'sess1');

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers });

      ownerEngine.setEncryptionKey(key);
      followerEngine.setEncryptionKey(key);

      // Connect both
      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'owner-1', sessionId: 'sess1', ownerClientId: 'owner-1' });
      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({ clientId: 'follower-1', sessionId: 'sess1', ownerClientId: 'owner-1' });

      // Capture owner's send and manually route to follower
      let sentPayload: any = null;
      ownerClient._send.mockImplementation((action: any) => {
        sentPayload = action;
      });

      ownerAdapter._outgoing = { type: 'sceneUpdate', payload: { elements: [{ id: 'el-1', data: '{"x":42}' }] } };
      ownerEngine.notifyLocalChange();
      timers.flush();

      // Wait for async encryption
      await new Promise(r => setTimeout(r, 50));

      expect(sentPayload).not.toBeNull();
      // The data field should be encrypted (base64, not the original JSON)
      const encryptedData = sentPayload.sceneUpdate.elements[0].data;
      expect(encryptedData).not.toBe('{"x":42}');

      // Now route to follower
      followerClient.simulateEvent({
        sceneUpdate: sentPayload.sceneUpdate,
        fromClientId: 'owner-1',
      });

      // Wait for async decryption
      await new Promise(r => setTimeout(r, 50));

      expect(followerAdapter.applyRemote).toHaveBeenCalledWith('owner-1', {
        elements: [{ id: 'el-1', data: '{"x":42}' }],
      });
    });

    it('wrong key: applyRemote NOT called', async () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();

      const ownerKey = await deriveKey('correct', 'sess1');
      const followerKey = await deriveKey('wrong', 'sess1');

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers });

      ownerEngine.setEncryptionKey(ownerKey);
      followerEngine.setEncryptionKey(followerKey);

      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'owner-1', sessionId: 'sess1', ownerClientId: 'owner-1' });
      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({ clientId: 'follower-1', sessionId: 'sess1', ownerClientId: 'owner-1' });

      let sentPayload: any = null;
      ownerClient._send.mockImplementation((action: any) => { sentPayload = action; });

      ownerAdapter._outgoing = { type: 'sceneUpdate', payload: { elements: [{ id: 'el-1', data: 'secret' }] } };
      ownerEngine.notifyLocalChange();
      timers.flush();
      await new Promise(r => setTimeout(r, 50));

      // Route encrypted payload to follower with wrong key
      followerClient.simulateEvent({
        sceneUpdate: sentPayload.sceneUpdate,
        fromClientId: 'owner-1',
      });
      await new Promise(r => setTimeout(r, 50));

      expect(followerAdapter.applyRemote).not.toHaveBeenCalled();
    });
  });

  /**
   * @flow Rapid mouse movement → one cursor send per throttle interval
   * @browser Pointer events → smooth remote cursor via Excalidraw collaborator overlay
   * @e2e test_collab_cursors.py (validates cursor appearance, not throttle timing)
   */
  describe('cursor throttling', () => {
    it('only sends one cursor update per throttle interval', () => {
      const client = makeMockClient();
      const adapter = makeMockAdapter();
      adapter._cursorData = { x: 10, y: 20, tool: 'pointer' };

      const engine = new CollabEngine({ client, adapter, timers, cursorThrottleMs: 50 });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      client._send.mockClear();

      // Rapid cursor moves
      engine.notifyCursorMove();
      engine.notifyCursorMove();
      engine.notifyCursorMove();

      timers.flush();

      // Only one send (first call schedules, rest are no-ops because cursorPending=true)
      const cursorCalls = client._send.mock.calls.filter(
        (call: any) => call[0]?.cursorUpdate !== undefined,
      );
      expect(cursorCalls).toHaveLength(1);
      expect(cursorCalls[0][0].cursorUpdate).toEqual({ x: 10, y: 20, tool: 'pointer' });
    });
  });

  /**
   * @flow Drag/resize generates many onChange → batched into single sync message
   * @browser Drag performance, continuous edit responsiveness
   * @e2e None
   */
  describe('debounce', () => {
    it('multiple notifyLocalChange calls result in single flush', () => {
      const client = makeMockClient();
      const adapter = makeMockAdapter();
      let computeCount = 0;
      adapter.computeOutgoing = () => {
        computeCount++;
        return { type: 'sceneUpdate', payload: { elements: [] } };
      };

      const engine = new CollabEngine({ client, adapter, timers, outgoingDebounceMs: 100 });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      engine.notifyLocalChange();
      engine.notifyLocalChange();
      engine.notifyLocalChange();

      timers.flush();

      expect(computeCount).toBe(1);
    });
  });

  /**
   * @flow Peer joins → "2 people" badge → peer leaves → "1 person"
   * @browser SharePanel badge DOM, colored dots
   * @e2e test_collab_sync.py::test_peer_count_updates
   */
  describe('peer tracking', () => {
    it('updates peers map on join/leave', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      const joinedPeers: string[] = [];
      const leftPeers: string[] = [];
      engine.on('peerJoined', (p) => joinedPeers.push(p.clientId));
      engine.on('peerLeft', (id) => leftPeers.push(id));

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      expect(engine.state.peers.size).toBe(1);

      client.simulatePeerJoined({ clientId: 'c2', username: 'Bob', avatarUrl: '', clientType: 'browser', isActive: true });
      expect(engine.state.peers.size).toBe(2);
      expect(joinedPeers).toContain('c2');

      client.simulatePeerLeft('c2');
      expect(engine.state.peers.size).toBe(1);
      expect(leftPeers).toContain('c2');
    });
  });

  /**
   * @flow Owner stops sharing → follower gets "session ended" error
   * @browser Error message in SharePanel, UI reverts to pre-share state
   * @e2e test_collab_sync.py::test_stop_sharing_disconnects_follower
   */
  describe('session ended', () => {
    it('resets state and emits event', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });
      const endedReasons: string[] = [];
      engine.on('sessionEnded', (r) => endedReasons.push(r));

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'f1', sessionId: 'sess1', ownerClientId: 'o1' });

      client.simulateSessionEnded('owner left');

      expect(engine.state.phase).toBe('disconnected');
      expect(engine.state.error).toContain('owner ended');
      expect(endedReasons).toHaveLength(1);
    });
  });

  /**
   * @flow Owner's tab closes → another tab becomes owner via OwnerChanged
   * @browser Tab detection, localStorage ownership transfer
   * @e2e None
   */
  describe('owner changed', () => {
    it('updates ownerClientId and isOwner', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c0' });

      expect(engine.state.isOwner).toBe(false);

      client.simulateOwnerChanged('c1');
      expect(engine.state.ownerClientId).toBe('c1');
      expect(engine.state.isOwner).toBe(true);
    });
  });

  /**
   * @flow Owner changes password → peers get "reconnect required" prompt
   * @browser Reconnect prompt UI, password re-entry
   * @e2e None
   */
  describe('credentials changed', () => {
    it('resets state with password_changed error', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      client.simulateCredentialsChanged('password_changed');

      expect(engine.state.phase).toBe('disconnected');
      expect(engine.state.error).toContain('Password changed');
    });

    it('resets state with password_removed error', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      client.simulateCredentialsChanged('password_removed');

      expect(engine.state.phase).toBe('disconnected');
      expect(engine.state.error).toContain('Encryption was removed');
    });
  });

  /**
   * @flow Owner renames drawing → followers see new title in header
   * @browser Title input DOM, header title update
   * @e2e None
   */
  describe('title changed', () => {
    it('updates roomTitle and emits event', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });
      const titles: string[] = [];
      engine.on('titleChanged', (t) => titles.push(t));

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      client.simulateTitleChanged('New Title');

      expect(engine.state.roomTitle).toBe('New Title');
      expect(titles).toEqual(['New Title']);
    });
  });

  /**
   * @flow Room is full → "ROOM_FULL" error shown to joining user
   * @browser Error toast / message in SharePanel
   * @e2e None
   */
  describe('error events', () => {
    it('sets error on ErrorEvent', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateErrorEvent('ROOM_FULL', 'Room is full (10/10)');

      expect(engine.state.error).toBe('ROOM_FULL: Room is full (10/10)');
      expect(engine.state.phase).toBe('disconnected');
    });
  });

  /**
   * @flow Engine connects before editor chunk loads → adapter set later triggers scene init
   * @browser Async Excalidraw chunk loading via React.lazy
   * @e2e None
   */
  describe('late-bound adapter', () => {
    it('triggers scene init when adapter is set after connect', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      // No adapter yet — should not be initialized
      expect(engine.state.isInitialized).toBe(false);

      // Set adapter — should auto-initialize (first peer, no others)
      const adapter = makeMockAdapter();
      engine.setAdapter(adapter);

      expect(engine.state.isInitialized).toBe(true);
    });
  });

  /**
   * @flow RoomJoined carries session metadata (encrypted, maxPeers, title)
   * @browser SharePanel shows encrypted badge, peer limit info
   * @e2e None
   */
  describe('room info', () => {
    it('extracts encrypted, maxPeers, title from RoomJoined', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' }, isOwner: true });
      client.simulateRoomJoined({
        clientId: 'c1',
        sessionId: 'sess1',
        ownerClientId: 'c1',
        encrypted: true,
        maxPeers: 10,
        title: 'My Drawing',
      });

      expect(engine.state.roomEncrypted).toBe(true);
      expect(engine.state.maxPeers).toBe(10);
      expect(engine.state.roomTitle).toBe('My Drawing');
      expect(engine.state.isOwner).toBe(true);
    });

    it('self-peer carries metadata from connect params', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw', docType: 'design' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });

      const selfPeer = engine.state.peers.get('c1');
      expect(selfPeer).toBeDefined();
      expect(selfPeer!.metadata).toEqual({ tool: 'excalidraw', docType: 'design' });
    });

    it('existing peers carry metadata from server', () => {
      const client = makeMockClient();
      const engine = new CollabEngine({ client, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Bob', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({
        clientId: 'c2',
        sessionId: 'sess1',
        ownerClientId: 'c1',
        peers: [{ clientId: 'c1', username: 'Alice', metadata: { tool: 'excalidraw', role: 'owner' } }],
      });

      const remotePeer = engine.state.peers.get('c1');
      expect(remotePeer).toBeDefined();
      expect(remotePeer!.metadata).toEqual({ tool: 'excalidraw', role: 'owner' });
    });
  });

  /**
   * @flow Peer leaves → their cursor disappears from the canvas
   * @browser Excalidraw removes collaborator dot via updateScene({ collaborators })
   * @e2e None
   */
  describe('peer cursor cleanup', () => {
    it('calls removePeerCursor when peer leaves', () => {
      const client = makeMockClient();
      const adapter = makeMockAdapter();
      const engine = new CollabEngine({ client, adapter, timers });

      engine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Alice', metadata: { tool: 'excalidraw' } });
      client.simulateRoomJoined({ clientId: 'c1', sessionId: 'sess1', ownerClientId: 'c1' });
      client.simulatePeerJoined({ clientId: 'c2', username: 'Bob', avatarUrl: '', clientType: 'browser', isActive: true });

      client.simulatePeerLeft('c2');

      expect(adapter.removePeerCursor).toHaveBeenCalledWith('c2');
    });
  });

  /**
   * @flow Cursor moves from one peer to the other via relay
   * @browser Excalidraw collaborator overlay rendering
   * @e2e test_collab_cursors.py::test_owner_sees_follower_cursor
   */
  describe('cursor round-trip', () => {
    it('cursor moves from one peer to the other via relay', () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers, cursorThrottleMs: 0 });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers, cursorThrottleMs: 0 });

      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'owner-1', sessionId: 'sess1', ownerClientId: 'owner-1' });
      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({
        clientId: 'follower-1',
        sessionId: 'sess1',
        ownerClientId: 'owner-1',
        peers: [{ clientId: 'owner-1', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      makeMockRelay(ownerClient, followerClient);

      // Owner moves cursor
      ownerAdapter._cursorData = { x: 100, y: 200, tool: 'rectangle' };
      ownerEngine.notifyCursorMove();
      timers.flush();

      // Follower should receive via applyRemoteCursor
      expect(followerAdapter.applyRemoteCursor).toHaveBeenCalledWith(
        expect.objectContaining({ clientId: 'owner-1', x: 100, y: 200, tool: 'rectangle' }),
      );
    });

    it('cursorUpdate resolves username from peers map', () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers, cursorThrottleMs: 0 });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers, cursorThrottleMs: 0 });

      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'owner-1', sessionId: 'sess1', ownerClientId: 'owner-1' });

      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({
        clientId: 'follower-1',
        sessionId: 'sess1',
        ownerClientId: 'owner-1',
        peers: [{ clientId: 'owner-1', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      // Simulate cursor event from owner (with known username in peers map)
      followerClient.simulateEvent({
        cursorUpdate: { x: 50, y: 75, tool: 'pointer' },
        fromClientId: 'owner-1',
      });

      // Username should come from peers map, not fallback clientId.slice(0,6)
      expect(followerAdapter.applyRemoteCursor).toHaveBeenCalledWith(
        expect.objectContaining({ clientId: 'owner-1', username: 'Owner', x: 50, y: 75 }),
      );
    });
  });

  /**
   * @flow Mermaid text update flows through relay between adapters
   * @browser Textarea render, onChange callback
   * @e2e None (highest-priority gap — no Mermaid collab E2E exists)
   */
  describe('text sync', () => {
    it('text update flows through relay between mermaid adapters', () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter({ tool: 'mermaid' });
      const followerAdapter = makeMockAdapter({ tool: 'mermaid' });

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers });

      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Owner', metadata: { tool: 'mermaid' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'owner-1', sessionId: 'sess1', ownerClientId: 'owner-1' });
      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'mermaid' } });
      followerClient.simulateRoomJoined({
        clientId: 'follower-1',
        sessionId: 'sess1',
        ownerClientId: 'owner-1',
        peers: [{ clientId: 'owner-1', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      makeMockRelay(ownerClient, followerClient);

      // Owner sends text update
      ownerAdapter._outgoing = { type: 'textUpdate', payload: { text: 'flowchart TD\n    A --> B --> C', version: 1 } };
      ownerEngine.notifyLocalChange();
      timers.flush();

      // Follower should receive the text
      expect(followerAdapter.applyRemote).toHaveBeenCalledWith('owner-1', { text: 'flowchart TD\n    A --> B --> C', version: 1 });
    });
  });

  /**
   * @flow Both peers flush changes at the same time — both should receive each other's data
   * @browser Visual conflict resolution handled by adapter reconciliation
   * @e2e None
   */
  describe('concurrent edits', () => {
    it('both peers can flush changes simultaneously', () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers });

      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'owner-1', sessionId: 'sess1', ownerClientId: 'owner-1' });
      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({
        clientId: 'follower-1',
        sessionId: 'sess1',
        ownerClientId: 'owner-1',
        peers: [{ clientId: 'owner-1', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      makeMockRelay(ownerClient, followerClient);

      // Both peers have outgoing changes
      ownerAdapter._outgoing = { type: 'sceneUpdate', payload: { elements: [{ id: 'el-A', data: '{"owner":true}' }] } };
      followerAdapter._outgoing = { type: 'sceneUpdate', payload: { elements: [{ id: 'el-B', data: '{"follower":true}' }] } };

      // Both notify
      ownerEngine.notifyLocalChange();
      followerEngine.notifyLocalChange();
      timers.flush();

      // Owner should receive follower's data
      expect(ownerAdapter.applyRemote).toHaveBeenCalledWith('follower-1', { elements: [{ id: 'el-B', data: '{"follower":true}' }] });
      // Follower should receive owner's data
      expect(followerAdapter.applyRemote).toHaveBeenCalledWith('owner-1', { elements: [{ id: 'el-A', data: '{"owner":true}' }] });
    });
  });

  /**
   * @flow Encrypted scene init: follower joins → sceneInitRequest → owner sends encrypted snapshot → follower decrypts
   * @browser Password prompt, scene population
   * @e2e test_encryption.py::test_encrypted_share_and_join (covers full flow including scene init)
   */
  describe('encrypted scene init', () => {
    it('scene init snapshot is encrypted and decrypted', async () => {
      const ownerClient = makeMockClient();
      const followerClient = makeMockClient();
      const ownerAdapter = makeMockAdapter();
      const followerAdapter = makeMockAdapter();
      ownerAdapter.getSceneSnapshot = vi.fn(() => '{"elements":[{"id":"secret-el"}]}');

      const key = await deriveKey('password123', 'sess1');

      const ownerEngine = new CollabEngine({ client: ownerClient, adapter: ownerAdapter, timers });
      const followerEngine = new CollabEngine({ client: followerClient, adapter: followerAdapter, timers });

      ownerEngine.setEncryptionKey(key);
      followerEngine.setEncryptionKey(key);

      // Owner connects first (alone)
      ownerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Owner', metadata: { tool: 'excalidraw' }, isOwner: true });
      ownerClient.simulateRoomJoined({ clientId: 'aaa-owner', sessionId: 'sess1', ownerClientId: 'aaa-owner', encrypted: true });

      // Add follower to owner's peer list
      ownerClient.simulatePeerJoined({ clientId: 'bbb-follower', username: 'Follower', avatarUrl: '', clientType: 'browser', isActive: true });

      // Capture owner's sends to manually verify encryption and route
      const ownerSends: any[] = [];
      ownerClient._send.mockImplementation((action: any) => {
        ownerSends.push(action);
        // Route sceneInitResponse to follower
        if (action.sceneInitResponse) {
          followerClient.simulateEvent({
            sceneInitResponse: action.sceneInitResponse,
            fromClientId: 'aaa-owner',
          });
        }
      });

      // Follower connects (triggers sceneInitRequest → routed to owner)
      followerClient._send.mockImplementation((action: any) => {
        // Route sceneInitRequest to owner
        ownerClient.simulateEvent({ ...action, fromClientId: 'bbb-follower' });
      });

      followerEngine.connect({ relayUrl: 'ws://test', sessionId: 'sess1', username: 'Follower', metadata: { tool: 'excalidraw' } });
      followerClient.simulateRoomJoined({
        clientId: 'bbb-follower',
        sessionId: 'sess1',
        ownerClientId: 'aaa-owner',
        encrypted: true,
        peers: [{ clientId: 'aaa-owner', username: 'Owner', avatarUrl: '', clientType: 'browser', isActive: true }],
      });

      // Wait for async encryption/decryption
      await new Promise(r => setTimeout(r, 100));

      // Owner should have encrypted the snapshot
      const initResponse = ownerSends.find(s => s.sceneInitResponse);
      expect(initResponse).toBeDefined();
      expect(initResponse.sceneInitResponse.payload).not.toBe('{"elements":[{"id":"secret-el"}]}');

      // Follower should have decrypted and applied the scene init
      expect(followerAdapter.applySceneInit).toHaveBeenCalledWith('{"elements":[{"id":"secret-el"}]}');
      expect(followerEngine.state.isInitialized).toBe(true);
    });
  });
});
