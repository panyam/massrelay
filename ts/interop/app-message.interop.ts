import { afterEach, describe, expect, inject, it } from 'vitest';
import { CollabClient } from '../src/CollabClient.js';

type Received = { kind: string; payload: Uint8Array; from: string; to: string };

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
    onAppMessage: (kind, payload, from, to) => peer.received.push({ kind, payload, from, to }),
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

    expect(page2.received).toEqual([{ kind: 'tools/call', payload: new Uint8Array([1, 2, 3]), from: bridge.clientId, to: page2.clientId }]);
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

    expect(page1.received.map((r) => [r.from, r.to])).toEqual([[bridge.clientId, '']]);
    expect(page2.received.map((r) => [r.from, r.to])).toEqual([[bridge.clientId, '']]);
    expect(bridge.received).toEqual([]);
  });

  it('addresses several peers in one message, each seeing only its own id', async () => {
    const room = `design:${crypto.randomUUID()}`;
    const bridge = await join(room, 'bridge');
    const pages = [await join(room, 'page1'), await join(room, 'page2'), await join(room, 'page3')];
    const bystander = await join(room, 'bystander');

    bridge.client.sendAppMessage('tools/call', new Uint8Array([4]), { to: pages.map((p) => p.clientId) });
    await until(() => pages.every((p) => p.received.length > 0), 'every page to receive');
    await settle();

    for (const page of pages) {
      expect(page.received.map((r) => [r.from, r.to])).toEqual([[bridge.clientId, page.clientId]]);
    }
    expect(bystander.received).toEqual([]);
    expect(bridge.errors).toEqual([]);
  });

  it("keeps one sender's messages in order at every receiver", async () => {
    const room = `design:${crypto.randomUUID()}`;
    const bridge = await join(room, 'bridge');
    // All five at once, as a bridge opening pages would (issue 45).
    const pages = await Promise.all([1, 2, 3, 4, 5].map((i) => join(room, `page${i}`)));
    const to = pages.map((p) => p.clientId);

    // Batch A: one message per page, then batch B: one multi-peer message per round.
    pages.forEach((p, i) => bridge.client.sendAppMessage(`A${i + 1}`, new Uint8Array([i]), { to: p.clientId }));
    for (let i = 1; i <= 5; i++) bridge.client.sendAppMessage(`B${i}`, new Uint8Array([i]), { to });
    await until(() => pages.every((p) => p.received.length === 6), 'every page to receive all six');

    pages.forEach((p, i) => {
      expect(p.received.map((r) => r.kind)).toEqual([`A${i + 1}`, 'B1', 'B2', 'B3', 'B4', 'B5']);
    });
    expect(bridge.errors).toEqual([]);
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

    // One absent peer among several doesn't stop delivery to the rest.
    bridge.client.sendAppMessage('tools/call', new Uint8Array([3]), { to: [goneId, page1.clientId] });
    await until(() => page1.received.length > 0, 'page1 to receive despite the absent peer');
    await until(() => bridge.errors.length > 1, 'bridge to get a second error');
    expect(bridge.errors).toEqual(['PEER_NOT_FOUND', 'PEER_NOT_FOUND']);
    page1.received.length = 0;

    // The refused message doesn't cost the sender its place in the room.
    expect(bridge.client.isConnected).toBe(true);
    bridge.client.sendAppMessage('tools/call', new Uint8Array([2]), { to: page1.clientId });
    await until(() => page1.received.length > 0, 'page1 to receive after the error');
    expect(page1.received.map((r) => [r.from, r.to])).toEqual([[bridge.clientId, page1.clientId]]);
  });

  it('joins a room whose id contains a slash, and messages within it', async () => {
    const room = `design:${crypto.randomUUID()}/board.kicad_sch`;
    const bridge = await join(room, 'bridge');
    const page = await join(room, 'page');

    expect(bridge.client.sessionId).toBe(room);
    bridge.client.sendAppMessage('roster', new Uint8Array([7]));
    await until(() => page.received.length > 0, 'page to receive');

    expect(page.received.map((r) => r.from)).toEqual([bridge.clientId]);
  });
});
