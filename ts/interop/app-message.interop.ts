import { afterEach, describe, expect, inject, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';

type Received = { kind: string; payload: Uint8Array; from: string };

interface Peer {
  client: CollabClient;
  clientId: string;
  received: Received[];
  errors: string[];
}

const open: CollabClient[] = [];

afterEach(() => {
  while (open.length) open.pop()!.disconnect();
});

function until(cond: () => boolean, what: string, ms = 3000): Promise<void> {
  return new Promise((ok, fail) => {
    const start = Date.now();
    const tick = () => {
      if (cond()) return ok();
      if (Date.now() - start > ms) return fail(new Error(`timed out waiting for ${what}`));
      setTimeout(tick, 10);
    };
    tick();
  });
}

const settle = () => new Promise((r) => setTimeout(r, 200));

async function join(sessionId: string, name: string): Promise<Peer> {
  const peer = { received: [], errors: [] } as unknown as Peer;
  let connected = '';
  peer.client = new CollabClient({
    onConnect: (id) => (connected = id),
    onAppMessage: (kind, payload, from) => peer.received.push({ kind, payload, from }),
    onErrorEvent: (code) => peer.errors.push(code),
  });
  open.push(peer.client);
  peer.client.connect(inject('relayUrl'), sessionId, name, {});
  await until(() => connected !== '', `${name} to join`);
  peer.clientId = connected;
  return peer;
}

describe('AppMessage against a real relay', () => {
  it('delivers an addressed message to its target only, naming the sender', async () => {
    const room = `design:${crypto.randomUUID()}`;
    const bridge = await join(room, 'bridge');
    const page1 = await join(room, 'page1');
    const page2 = await join(room, 'page2');

    bridge.client.sendAppMessage('tools/call', new Uint8Array([1, 2, 3]), { to: page2.clientId });
    await until(() => page2.received.length > 0, 'page2 to receive');
    await settle();

    expect(page2.received).toEqual([{ kind: 'tools/call', payload: new Uint8Array([1, 2, 3]), from: bridge.clientId }]);
    expect(page1.received).toEqual([]);
    expect(bridge.received).toEqual([]);
    expect(bridge.errors).toEqual([]);
  });

  it('fans an unaddressed message out to every other peer', async () => {
    const room = `design:${crypto.randomUUID()}`;
    const bridge = await join(room, 'bridge');
    const page1 = await join(room, 'page1');
    const page2 = await join(room, 'page2');

    bridge.client.sendAppMessage('roster', new Uint8Array([9]));
    await until(() => page1.received.length > 0 && page2.received.length > 0, 'both pages to receive');
    await settle();

    expect(page1.received.map((r) => r.from)).toEqual([bridge.clientId]);
    expect(page2.received.map((r) => r.from)).toEqual([bridge.clientId]);
    expect(bridge.received).toEqual([]);
  });

  it('answers PEER_NOT_FOUND for a peer that left, and delivers nothing', async () => {
    const room = `design:${crypto.randomUUID()}`;
    const bridge = await join(room, 'bridge');
    const page1 = await join(room, 'page1');
    const gone = await join(room, 'gone');
    const goneId = gone.clientId;
    gone.client.disconnect();
    await settle();

    bridge.client.sendAppMessage('tools/call', new Uint8Array([1]), { to: goneId });
    await until(() => bridge.errors.length > 0, 'bridge to get an error');
    await settle();

    expect(bridge.errors).toEqual(['PEER_NOT_FOUND']);
    expect(page1.received).toEqual([]);
  });
});
