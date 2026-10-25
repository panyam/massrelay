import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { GRPCWSClient } from '@panyam/servicekit-client';
import { CollabClient } from './CollabClient.js';
import type { PeerInfoJson } from './gen/massrelay/v1/models/collab_pb.js';

async function connectedClient(received: Array<[string, Uint8Array, string, string]>) {
  const { client: grpc, controller } = GRPCWSClient.createMock();
  const client = new CollabClient({
    _grpcFactory: () => grpc,
    onAppMessage: (kind, payload, fromClientId, toClientId) => received.push([kind, payload, fromClientId, toClientId]),
  });
  client.connect('ws://relay.test', 'design:abc', 'page', {});
  controller.simulateOpen();
  await Promise.resolve();
  controller.simulateMessage({ roomJoined: { clientId: 'me', room: { sessionId: 'design:abc' } } });
  return { client, controller };
}

describe('CollabClient AppMessage', () => {
  /** @flow Bridge calls a tool on one page → only that page's AppMessage is addressed */
  it('sends to_client_ids when addressed, and omits it otherwise', async () => {
    const { client, controller } = await connectedClient([]);
    client.sendAppMessage('tools/call', new Uint8Array([1, 2]), { to: 'page-2' });
    client.sendAppMessage('tools/call', new Uint8Array([1, 2]), { to: ['page-2', 'page-3'] });
    client.sendAppMessage('roster', new Uint8Array([3]));
    client.sendAppMessage('roster', new Uint8Array([3]), { to: [] });

    const sent = controller.sentMessages.slice(-4) as Array<{ appMessage: Record<string, unknown> }>;
    expect(sent[0].appMessage).toEqual({ kind: 'tools/call', payload: 'AQI=', toClientIds: ['page-2'] });
    expect(sent[1].appMessage).toEqual({ kind: 'tools/call', payload: 'AQI=', toClientIds: ['page-2', 'page-3'] });
    expect(sent[2].appMessage).toEqual({ kind: 'roster', payload: 'Aw==' });
    expect(sent[3].appMessage).toEqual({ kind: 'roster', payload: 'Aw==' });
  });

  it('passes the sender and the addressee to onAppMessage', async () => {
    const received: Array<[string, Uint8Array, string, string]> = [];
    const { controller } = await connectedClient(received);
    controller.simulateMessage({
      fromClientId: 'bridge-1',
      appMessage: { kind: 'tools/call', payload: 'AQI=', toClientIds: ['me'] },
    });
    controller.simulateMessage({
      fromClientId: 'bridge-1',
      appMessage: { kind: 'roster', payload: 'Aw==' },
    });

    expect(received).toEqual([
      ['tools/call', new Uint8Array([1, 2]), 'bridge-1', 'me'],
      ['roster', new Uint8Array([3]), 'bridge-1', ''],
    ]);
  });
});

function connectUrl(options: { token?: string }, sessionId: string): string {
  const { client: grpc } = GRPCWSClient.createMock();
  const connect = vi.spyOn(grpc, 'connect');
  new CollabClient({ ...options, _grpcFactory: () => grpc }).connect('ws://relay.test', sessionId, 'page', {});
  return connect.mock.calls[0][0];
}

describe('CollabClient connect URL', () => {
  it('sends the token as a query parameter, encoded', () => {
    expect(connectUrl({ token: 'a.b+c/d=' }, 'design:abc')).toBe('ws://relay.test/ws/v1/design%3Aabc/sync?token=a.b%2Bc%2Fd%3D');
  });

  it('leaves the token out when none is set', () => {
    expect(connectUrl({}, 'design:abc')).toBe('ws://relay.test/ws/v1/design%3Aabc/sync');
  });

  it('escapes a session id containing a slash into one path segment', () => {
    expect(connectUrl({}, 'design:demo/board.kicad_sch')).toBe('ws://relay.test/ws/v1/design%3Ademo%2Fboard.kicad_sch/sync');
  });
});

describe('CollabClient logging', () => {
  it('does not write to the console when events arrive', async () => {
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});
    try {
      const { controller } = await connectedClient([]);
      controller.simulateMessage({ fromClientId: 'bridge-1', appMessage: { kind: 'roster', payload: 'Aw==' } });
      expect(log).not.toHaveBeenCalled();
    } finally {
      log.mockRestore();
    }
  });
});

function trackedClient() {
  const { client: grpc, controller } = GRPCWSClient.createMock();
  const calls: string[] = [];
  const close = vi.spyOn(grpc, 'close');
  const client = new CollabClient({
    _grpcFactory: () => grpc,
    onConnect: () => calls.push('connect'),
    onDisconnect: () => calls.push('disconnect'),
    onErrorEvent: (code) => calls.push(`error:${code}`),
    onSessionEnded: (reason) => calls.push(`ended:${reason}`),
  });
  client.connect('ws://relay.test', 'design:abc', 'page', {});
  controller.simulateOpen();
  return { client, controller, calls, close };
}

const joined = { roomJoined: { clientId: 'me', room: { sessionId: 'design:abc' } } };

describe('CollabClient relay errors', () => {
  /** @flow Page replies to an agent that already left → that reply is refused, the page stays in the room */
  it('keeps the connection after an error that follows the join', async () => {
    const { client, controller, calls, close } = trackedClient();
    await Promise.resolve();
    controller.simulateMessage(joined);
    controller.simulateMessage({ error: { code: 'PEER_NOT_FOUND', message: 'No client agent-1 in this room' } });

    expect(calls).toEqual(['connect', 'error:PEER_NOT_FOUND']);
    expect(client.isConnected).toBe(true);
    expect(client.clientId).toBe('me');
    expect(close).not.toHaveBeenCalled();
    expect(() => client.sendAppMessage('tools/result', new Uint8Array([1]))).not.toThrow();
  });

  it('closes without onDisconnect when the join itself is refused', async () => {
    const { client, controller, calls, close } = trackedClient();
    await Promise.resolve();
    controller.simulateMessage({ error: { code: 'ROOM_FULL', message: 'Room is full (10/10)' } });

    expect(calls).toEqual(['error:ROOM_FULL']);
    expect(client.isConnected).toBe(false);
    expect(close).toHaveBeenCalledTimes(1);
  });

  it('calls onDisconnect once, before onSessionEnded, when the owner ends the session', async () => {
    const { client, controller, calls, close } = trackedClient();
    await Promise.resolve();
    controller.simulateMessage(joined);
    controller.simulateMessage({ sessionEnded: { reason: 'owner stopped sharing' } });
    controller.simulateClose();

    expect(calls).toEqual(['connect', 'disconnect', 'ended:owner stopped sharing']);
    expect(client.isConnected).toBe(false);
    expect(close).toHaveBeenCalledTimes(1);
  });
});

function rosterClient() {
  const { client: grpc, controller } = GRPCWSClient.createMock();
  const joinedPeers: PeerInfoJson[] = [];
  const client = new CollabClient({
    _grpcFactory: () => grpc,
    onPeerJoined: (peer) => joinedPeers.push(peer),
  });
  client.connect('ws://relay.test', 'design:abc', 'page', {});
  controller.simulateOpen();
  return { client, controller, joinedPeers };
}

const bridgeIdentity = { subject: 'bridge-1', clientId: 'mcpkit-host' };

describe('CollabClient peer identity', () => {
  /** @flow Page joins a room the host's bridge is already in → it can tell the bridge is relay-vouched */
  it('passes identity to onPeerJoined for peers already in the room', async () => {
    const { controller, joinedPeers } = rosterClient();
    await Promise.resolve();
    controller.simulateMessage({ roomJoined: { clientId: 'me', room: { sessionId: 'design:abc', peers: {
      bridge: { clientId: 'bridge', username: 'host', identity: bridgeIdentity },
      anon: { clientId: 'anon', username: 'guest' },
    } } } });

    const byId = Object.fromEntries(joinedPeers.map((p) => [p.clientId, p]));
    expect(byId.bridge.identity).toEqual(bridgeIdentity);
    expect(byId.anon.identity).toBeUndefined();
    expect(byId.me.identity).toBeUndefined();
  });

  it('passes identity to onPeerJoined for a peer that joins later', async () => {
    const { controller, joinedPeers } = rosterClient();
    await Promise.resolve();
    controller.simulateMessage(joined);
    controller.simulateMessage({ peerJoined: { peer: { clientId: 'bridge', identity: bridgeIdentity } } });

    expect(joinedPeers.at(-1)).toMatchObject({ clientId: 'bridge', identity: bridgeIdentity });
  });

  it('looks a sender up by client id until it leaves', async () => {
    const { client, controller } = rosterClient();
    await Promise.resolve();
    controller.simulateMessage({ roomJoined: { clientId: 'me', room: { sessionId: 'design:abc', peers: {
      anon: { clientId: 'anon' },
    } } } });
    controller.simulateMessage({ peerJoined: { peer: { clientId: 'bridge', identity: bridgeIdentity } } });

    expect(client.getPeer('bridge')?.identity?.subject).toBe('bridge-1');
    expect(client.getPeer('anon')?.identity?.subject).toBeUndefined();
    expect(client.getPeer('me')?.clientId).toBe('me');
    expect(client.getPeer('nobody')).toBeUndefined();

    controller.simulateMessage({ peerLeft: { clientId: 'bridge' } });
    expect(client.getPeer('bridge')).toBeUndefined();
  });

  it('keeps isOwner current in the roster when ownership moves', async () => {
    const { client, controller } = rosterClient();
    await Promise.resolve();
    controller.simulateMessage({ roomJoined: { clientId: 'me', room: { sessionId: 'design:abc', peers: {
      bridge: { clientId: 'bridge', isOwner: true },
    } } } });
    controller.simulateMessage({ ownerChanged: { newOwnerClientId: 'me' } });

    expect(client.getPeer('bridge')?.isOwner).toBeFalsy();
    expect(client.getPeer('me')?.isOwner).toBe(true);
  });

  it('forgets the roster when the connection closes', async () => {
    const { client, controller } = rosterClient();
    await Promise.resolve();
    controller.simulateMessage(joined);
    controller.simulateMessage({ peerJoined: { peer: { clientId: 'bridge', identity: bridgeIdentity } } });
    controller.simulateClose();

    expect(client.getPeer('bridge')).toBeUndefined();
  });
});

type Controller = ReturnType<typeof GRPCWSClient.createMock>['controller'];

/** A client whose every connection gets its own mock socket, so a reconnect
 * shows up as a new entry in sockets. rooms answers the reconnect room check:
 * an HTTP status, or 'down' for a relay that refuses the connection. */
function reconnectingClient(opts: { rooms?: Array<number | 'down'>; options?: Partial<ConstructorParameters<typeof CollabClient>[0]> } = {}) {
  const sockets: Array<{ controller: Controller; url: string }> = [];
  const calls: string[] = [];
  const rooms = [...(opts.rooms ?? [])];
  const fetched: string[] = [];
  const client = new CollabClient({
    retryDelayMs: 100,
    _grpcFactory: () => {
      const { client: grpc, controller } = GRPCWSClient.createMock();
      const entry = { controller, url: '' };
      const connect = grpc.connect.bind(grpc);
      grpc.connect = (url: string) => {
        entry.url = url;
        return connect(url);
      };
      sockets.push(entry);
      return grpc;
    },
    _fetch: (async (url: string) => {
      fetched.push(url);
      const r = rooms.shift() ?? 404;
      if (r === 'down') throw new TypeError('fetch failed');
      return new Response(null, { status: r });
    }) as typeof fetch,
    onConnect: (id) => calls.push(`connect:${id}`),
    onDisconnect: () => calls.push('disconnect'),
    onSessionEnded: (reason) => calls.push(`ended:${reason}`),
    onReconnecting: (n) => calls.push(`reconnecting:${n}`),
    onErrorEvent: (code) => calls.push(`error:${code}`),
    ...opts.options,
  });
  return { client, sockets, calls, fetched };
}

async function openAndJoin(c: Controller, clientId: string) {
  c.simulateOpen();
  await vi.advanceTimersByTimeAsync(0);
  c.simulateMessage({ roomJoined: { clientId, room: { sessionId: 'design:abc' } } });
}

function joinOf(c: Controller) {
  return (c.sentMessages.find((m) => (m as { join?: unknown }).join) as { join: { sessionId: string } }).join;
}

describe('CollabClient reconnect', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  /** @flow Relay evicts a stalled tab as a slow consumer → it checks the room is alive and rejoins the same session */
  it('rejoins the same session after a slow_consumer eviction', async () => {
    const { client, sockets, calls, fetched } = reconnectingClient({ rooms: [200] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');

    sockets[0].controller.simulateMessage({ sessionEnded: { reason: 'slow_consumer' } });
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'reconnecting:1']);
    expect(sockets).toHaveLength(1);

    await vi.advanceTimersByTimeAsync(100);
    expect(fetched).toEqual(['http://relay.test/api/v1/rooms/design%3Aabc']);
    expect(sockets).toHaveLength(2);
    await openAndJoin(sockets[1].controller, 'me-2');

    expect(joinOf(sockets[1].controller).sessionId).toBe('design:abc');
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'reconnecting:1', 'connect:me-2']);
    expect(client.isConnected).toBe(true);
    expect(client.clientId).toBe('me-2');
  });

  /** @flow Relay restarts → the client finds the room gone and tells the app instead of recreating it */
  it('ends with session_gone when the room no longer exists', async () => {
    const { client, sockets, calls } = reconnectingClient({ rooms: ['down', 404] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');

    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(100);
    await vi.advanceTimersByTimeAsync(200);

    expect(calls).toEqual(['connect:me-1', 'disconnect', 'reconnecting:1', 'reconnecting:2', 'ended:session_gone']);
    expect(sockets).toHaveLength(1);
    expect(client.isConnecting).toBe(false);
    await vi.advanceTimersByTimeAsync(60000);
    expect(calls.at(-1)).toBe('ended:session_gone');
  });

  it('gives up with reconnect_failed after maxRetries unreachable attempts', async () => {
    const { client, sockets, calls, fetched } = reconnectingClient({ rooms: ['down', 'down', 'down'], options: { maxRetries: 2 } });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');

    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(60000);

    expect(fetched).toHaveLength(2);
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'reconnecting:1', 'reconnecting:2', 'ended:reconnect_failed']);
  });

  it('backs off exponentially between attempts', async () => {
    const { client, sockets, fetched } = reconnectingClient({ rooms: ['down', 'down', 200] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');

    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(99);
    expect(fetched).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetched).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(199);
    expect(fetched).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetched).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(400);
    expect(fetched).toHaveLength(3);
    expect(sockets).toHaveLength(2);
  });

  it('does not reconnect after disconnect()', async () => {
    const { client, sockets, calls, fetched } = reconnectingClient({ rooms: [200] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');

    client.disconnect();
    sockets[0].controller.simulateClose();
    await vi.advanceTimersByTimeAsync(60000);

    expect(fetched).toEqual([]);
    expect(sockets).toHaveLength(1);
    expect(calls).toEqual(['connect:me-1', 'disconnect']);
  });

  it('does not reconnect when the owner ends the session', async () => {
    const { client, sockets, calls, fetched } = reconnectingClient({ rooms: [200] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');

    sockets[0].controller.simulateMessage({ sessionEnded: { reason: 'owner_disconnected' } });
    sockets[0].controller.simulateClose();
    await vi.advanceTimersByTimeAsync(60000);

    expect(fetched).toEqual([]);
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'ended:owner_disconnected']);
  });

  it('does not reconnect a client that never joined', async () => {
    const { client, sockets, fetched } = reconnectingClient({ rooms: [200] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(60000);

    expect(fetched).toEqual([]);
    expect(sockets).toHaveLength(1);
  });

  it('keeps the old behavior with reconnect: false', async () => {
    const { client, sockets, calls, fetched } = reconnectingClient({ rooms: [200], options: { reconnect: false } });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');

    sockets[0].controller.simulateMessage({ sessionEnded: { reason: 'slow_consumer' } });
    await vi.advanceTimersByTimeAsync(60000);

    expect(fetched).toEqual([]);
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'ended:slow_consumer']);
  });

  it('stops when the rejoin is refused', async () => {
    const { client, sockets, calls } = reconnectingClient({ rooms: [200, 200] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');

    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(100);
    sockets[1].controller.simulateOpen();
    await vi.advanceTimersByTimeAsync(0);
    sockets[1].controller.simulateMessage({ error: { code: 'ROOM_FULL', message: 'Room is full (2/2)' } });
    sockets[1].controller.simulateClose();
    await vi.advanceTimersByTimeAsync(60000);

    expect(sockets).toHaveLength(2);
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'reconnecting:1', 'error:ROOM_FULL']);
  });

  /** @flow A rejoin after the original token expired → the app's getToken supplies a fresh one */
  it('asks getToken for a token on every connection', async () => {
    let n = 0;
    const { client, sockets } = reconnectingClient({ rooms: [200], options: { getToken: async () => `tok-${++n}` } });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await vi.advanceTimersByTimeAsync(0);
    await openAndJoin(sockets[0].controller, 'me-1');

    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(100);

    expect(sockets.map((s) => s.url)).toEqual([
      'ws://relay.test/ws/v1/design%3Aabc/sync?token=tok-1',
      'ws://relay.test/ws/v1/design%3Aabc/sync?token=tok-2',
    ]);
  });

  it('asks a rejoin to join only an existing room', async () => {
    const { client, sockets } = reconnectingClient({ rooms: [200] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');
    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(100);
    await openAndJoin(sockets[1].controller, 'me-2');

    expect(joinOf(sockets[0].controller)).toMatchObject({ mustExist: false });
    expect(joinOf(sockets[1].controller)).toMatchObject({ mustExist: true });
  });

  /** @flow The room's last peer leaves between the room check and the rejoin → the relay refuses it, and the app hears the session is gone */
  it('ends with session_gone when the rejoin finds no room', async () => {
    const { client, sockets, calls } = reconnectingClient({ rooms: [200] });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');
    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(100);
    sockets[1].controller.simulateOpen();
    await vi.advanceTimersByTimeAsync(0);
    sockets[1].controller.simulateMessage({ error: { code: 'ROOM_NOT_FOUND', message: 'No room design:abc on this relay' } });
    await vi.advanceTimersByTimeAsync(60000);

    expect(calls).toEqual(['connect:me-1', 'disconnect', 'reconnecting:1', 'error:ROOM_NOT_FOUND', 'ended:session_gone']);
    expect(sockets).toHaveLength(2);
  });

  it('ends with token_expired when it has no way to get a fresh token', async () => {
    const { client, sockets, calls, fetched } = reconnectingClient({ rooms: [200], options: { token: 'static' } });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await openAndJoin(sockets[0].controller, 'me-1');
    sockets[0].controller.simulateMessage({ sessionEnded: { reason: 'token_expired' } });
    sockets[0].controller.simulateClose();
    await vi.advanceTimersByTimeAsync(60000);

    expect(fetched).toEqual([]);
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'ended:token_expired']);
  });

  /** @flow The relay ends a connection at its token's exp → a client with getToken rejoins on a fresh token */
  it('reconnects on a fresh token after token_expired with getToken', async () => {
    let n = 0;
    const { client, sockets, calls } = reconnectingClient({ rooms: [200], options: { getToken: () => `tok-${++n}` } });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await vi.advanceTimersByTimeAsync(0);
    await openAndJoin(sockets[0].controller, 'me-1');
    sockets[0].controller.simulateMessage({ sessionEnded: { reason: 'token_expired' } });
    await vi.advanceTimersByTimeAsync(100);
    await openAndJoin(sockets[1].controller, 'me-2');

    expect(sockets[1].url).toBe('ws://relay.test/ws/v1/design%3Aabc/sync?token=tok-2');
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'reconnecting:1', 'connect:me-2']);
  });

  it('counts a getToken failure as a failed attempt', async () => {
    let fail = false;
    const errors: string[] = [];
    const { client, sockets, calls } = reconnectingClient({
      rooms: [200, 200],
      options: {
        getToken: async () => {
          if (fail) {
            fail = false;
            throw new Error('token endpoint down');
          }
          return 'tok';
        },
        onError: (e) => errors.push(e.message),
      },
    });
    client.connect('ws://relay.test', 'design:abc', 'page', {});
    await vi.advanceTimersByTimeAsync(0);
    await openAndJoin(sockets[0].controller, 'me-1');

    fail = true;
    sockets[0].controller.simulateClose(1006);
    await vi.advanceTimersByTimeAsync(100);
    expect(errors).toEqual(['token endpoint down']);
    expect(sockets).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(200);
    expect(sockets).toHaveLength(2);
    expect(calls).toEqual(['connect:me-1', 'disconnect', 'reconnecting:1', 'reconnecting:2']);
  });
});
