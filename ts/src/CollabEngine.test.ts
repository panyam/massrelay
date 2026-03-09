import { describe, it, expect, vi, beforeEach } from 'vitest';
import { CollabEngine } from './CollabEngine.js';
import type { CollabEngineState, TimerProvider } from './CollabEngine.js';
import { CollabClient } from './CollabClient.js';
import type { SyncAdapter, OutgoingUpdate, CursorData, PeerCursor } from './SyncAdapter.js';
import { deriveKey, encryptPayload, decryptPayload } from './crypto.js';

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
  simulatePeerJoined(peer: Record<string, any>): void;
  simulatePeerLeft(clientId: string): void;
  simulateEvent(event: any): void;
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
    client.options.onPeerJoined?.({
      clientId: data.clientId,
      username: mock._username,
      avatarUrl: '',
      clientType: 'browser',
      isActive: true,
    } as any);

    // Existing peers
    if (data.peers) {
      for (const peer of data.peers) {
        client.options.onPeerJoined?.(peer);
      }
    }

    // onEvent with roomJoined
    client.options.onEvent?.({
      roomJoined: {
        clientId: data.clientId,
        sessionId: data.sessionId || mock._sessionId,
        ownerClientId: data.ownerClientId || '',
        encrypted: data.encrypted ?? false,
        maxPeers: data.maxPeers ?? 0,
        title: data.title ?? '',
        peers: data.peers ?? [],
      },
    });
  };

  mock.simulatePeerJoined = (peer: Record<string, any>) => {
    client.options.onPeerJoined?.(peer as any);
  };

  mock.simulatePeerLeft = (clientId: string) => {
    client.options.onPeerLeft?.(clientId);
    // Also fire onEvent with peerLeft (as useSync expects)
    client.options.onEvent?.({ peerLeft: { clientId } });
  };

  mock.simulateEvent = (event: any) => {
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

  // 1. State machine lifecycle
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

  // 2. Two-peer sync
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
  });

  // 3. Scene init protocol
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

  // 4. Owner election (lowest clientId excluding requester)
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

  // 5. Encryption round-trip
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

  // 6. Cursor throttling
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

  // 7. Debounce batching
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

  // 8. Peer tracking
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

  // 9. Session ended
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

  // 10. Owner changed
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

  // 11. Credentials changed
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

  // 12. Title changed
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

  // 13. Error events
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

  // 14. Late-bound adapter
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

  // 15. Room info from RoomJoined
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
  });

  // 16. removePeerCursor on peerLeft
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
});
