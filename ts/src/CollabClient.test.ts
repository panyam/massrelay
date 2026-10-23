import { describe, it, expect } from 'vitest';
import { GRPCWSClient } from '@panyam/servicekit-client';
import { CollabClient } from './CollabClient.js';

async function connectedClient(received: Array<[string, Uint8Array, string]>) {
  const { client: grpc, controller } = GRPCWSClient.createMock();
  const client = new CollabClient({
    _grpcFactory: () => grpc,
    onAppMessage: (kind, payload, fromClientId) => received.push([kind, payload, fromClientId]),
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

  it('passes the sender client id to onAppMessage', async () => {
    const received: Array<[string, Uint8Array, string]> = [];
    const { controller } = await connectedClient(received);
    controller.simulateMessage({
      fromClientId: 'bridge-1',
      appMessage: { kind: 'tools/call', payload: 'AQI=', toClientId: 'me' },
    });

    expect(received).toEqual([['tools/call', new Uint8Array([1, 2]), 'bridge-1']]);
  });
});
