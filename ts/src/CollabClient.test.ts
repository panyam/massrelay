import { describe, it, expect, vi } from 'vitest';
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
