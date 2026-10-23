import { describe, it, expect, vi } from 'vitest';
import { GRPCWSClient } from '@panyam/servicekit-client';
import { CollabClient } from './CollabClient.js';

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
  it('sends to_client_id when addressed, and omits it otherwise', async () => {
    const { client, controller } = await connectedClient([]);
    client.sendAppMessage('tools/call', new Uint8Array([1, 2]), { to: 'page-2' });
    client.sendAppMessage('roster', new Uint8Array([3]));

    const sent = controller.sentMessages.slice(-2) as Array<{ appMessage: Record<string, unknown> }>;
    expect(sent[0].appMessage).toEqual({ kind: 'tools/call', payload: 'AQI=', toClientId: 'page-2' });
    expect(sent[1].appMessage).toEqual({ kind: 'roster', payload: 'Aw==' });
  });

  it('passes the sender and the addressee to onAppMessage', async () => {
    const received: Array<[string, Uint8Array, string, string]> = [];
    const { controller } = await connectedClient(received);
    controller.simulateMessage({
      fromClientId: 'bridge-1',
      appMessage: { kind: 'tools/call', payload: 'AQI=', toClientId: 'me' },
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
